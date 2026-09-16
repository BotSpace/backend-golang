package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) Create(ctx context.Context, in CreateInput) (*Project, error) {
	row, err := r.queries.CreateProject(ctx, sqlcdb.CreateProjectParams{
		ID:          uuid.New(),
		Name:        in.Name,
		Description: strPtr(in.Description),
		Username:    strPtr(in.Username),
		OwnerID:     in.OwnerID,
		Flow:        flowOrEmpty(in.Flow),
		IsTemplate:  in.IsTemplate,
	})
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	p := fromSQLC(row)
	return &p, nil
}

func (r *pgRepo) GetForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Project, error) {
	return mapOne(r.queries.GetProjectForOwner(ctx, sqlcdb.GetProjectForOwnerParams{ID: id, OwnerID: ownerID}))
}

func (r *pgRepo) Update(ctx context.Context, id, ownerID uuid.UUID, in UpdateInput) (*Project, error) {
	return mapOne(r.queries.UpdateProject(ctx, sqlcdb.UpdateProjectParams{
		ID:          id,
		OwnerID:     ownerID,
		Name:        in.Name,
		Description: in.Description,
		Username:    in.Username,
		Flow:        in.Flow, // nil -> COALESCE keeps existing
	}))
}

// releaseBotTokenKey is the environment key the deploy pipeline reads —
// mirrors Django's EnviromentModel key="RELEASE_BOT_TOKEN" (bot.py:39,
// kubernetes_service.py:680-685).
const releaseBotTokenKey = "RELEASE_BOT_TOKEN"

func (r *pgRepo) SetReleaseBotToken(ctx context.Context, projectID, ownerID uuid.UUID, token string) error {
	_, err := r.queries.UpsertEnviromentForOwner(ctx, sqlcdb.UpsertEnviromentForOwnerParams{
		ID:        uuid.New(),
		UserID:    ownerID,
		Key:       releaseBotTokenKey,
		Value:     token,
		Secret:    true,
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return database.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("set release bot token: %w", err)
	}
	return nil
}

// GetDebugForOwner reuses the release module's generated debug-table query —
// see the Repository interface doc comment for why this reaches across
// modules via raw SQL instead of a Go import.
func (r *pgRepo) GetDebugForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (*ProjectDebug, error) {
	row, err := r.queries.GetDebugForOwner(ctx, sqlcdb.GetDebugForOwnerParams{ProjectID: projectID, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get debug for owner: %w", err)
	}
	d := &ProjectDebug{
		ID:        row.ID,
		ServiceID: strVal(row.ServiceID),
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	return d, nil
}

// UpdateDebugStatus persists a reconciled live status (see
// release.reconcileDebug); serviceID is re-sent unchanged since the
// underlying query writes both columns together.
func (r *pgRepo) UpdateDebugStatus(ctx context.Context, id uuid.UUID, serviceID, status string) (*ProjectDebug, error) {
	row, err := r.queries.UpdateDebugDeployResult(ctx, sqlcdb.UpdateDebugDeployResultParams{
		ID:        id,
		ServiceID: strPtr(serviceID),
		Status:    status,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update debug status: %w", err)
	}
	return &ProjectDebug{
		ID:        row.ID,
		ServiceID: strVal(row.ServiceID),
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// ActiveReleaseServiceIDsForOwner reuses the release module's generated
// query — see the Repository interface doc comment for why this reaches
// across modules via raw SQL instead of a Go import.
func (r *pgRepo) ActiveReleaseServiceIDsForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]string, error) {
	rows, err := r.queries.ListReleaseServiceIDsForOwner(ctx, sqlcdb.ListReleaseServiceIDsForOwnerParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list active release service ids: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, sid := range rows {
		if sid != nil && *sid != "" {
			out = append(out, *sid)
		}
	}
	return out, nil
}

// GetReleaseBotToken reuses the release module's generated
// ListEnviromentsByProject query (same cross-module SQL-only convention as
// GetDebugForOwner above) rather than adding a dedicated single-row query —
// project env lists are small (a handful of KEY=VALUE rows per project).
func (r *pgRepo) GetReleaseBotToken(ctx context.Context, projectID uuid.UUID) (string, error) {
	envs, err := r.queries.ListEnviromentsByProject(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("list environment: %w", err)
	}
	for _, e := range envs {
		if e.Key == releaseBotTokenKey {
			return e.Value, nil
		}
	}
	return "", nil
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *pgRepo) SoftDelete(ctx context.Context, id, ownerID uuid.UUID) error {
	rows, err := r.queries.SoftDeleteProject(ctx, sqlcdb.SoftDeleteProjectParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("soft delete project: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) List(ctx context.Context, f ListFilter) ([]Project, error) {
	rows, err := r.queries.ListProjectsForOwner(ctx, sqlcdb.ListProjectsForOwnerParams{
		OwnerID: f.OwnerID,
		Search:  strPtr(f.Search),
		Limit:   f.Limit,
		Offset:  f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]Project, len(rows))
	for i, row := range rows {
		out[i] = fromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) Count(ctx context.Context, ownerID uuid.UUID, search string) (int64, error) {
	n, err := r.queries.CountProjectsForOwner(ctx, sqlcdb.CountProjectsForOwnerParams{
		OwnerID: ownerID,
		Search:  strPtr(search),
	})
	if err != nil {
		return 0, fmt.Errorf("count projects: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListAdmin(ctx context.Context, f AdminListFilter) ([]AdminProjectSummary, error) {
	rows, err := r.queries.ListProjectsAdmin(ctx, sqlcdb.ListProjectsAdminParams{
		Search:        strPtr(f.Search),
		OwnerID:       f.OwnerID,
		IsTemplate:    f.IsTemplate,
		HasRelease:    f.HasRelease,
		CreatedAfter:  f.CreatedAfter,
		CreatedBefore: f.CreatedBefore,
		Ordering:      strPtr(f.Ordering),
		Limit:         f.Limit,
		Offset:        f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list projects admin: %w", err)
	}
	out := make([]AdminProjectSummary, len(rows))
	for i, row := range rows {
		out[i] = AdminProjectSummary{
			Project:       fromSQLC(row.Project),
			OwnerPhone:    deref(row.OwnerPhone),
			ReleasesCount: row.ReleasesCount,
			ReleaseStatus: row.ReleaseStatus,
		}
	}
	return out, nil
}

func (r *pgRepo) CountAdmin(ctx context.Context, f AdminListFilter) (int64, error) {
	n, err := r.queries.CountProjectsAdmin(ctx, sqlcdb.CountProjectsAdminParams{
		Search:        strPtr(f.Search),
		OwnerID:       f.OwnerID,
		IsTemplate:    f.IsTemplate,
		HasRelease:    f.HasRelease,
		CreatedAfter:  f.CreatedAfter,
		CreatedBefore: f.CreatedBefore,
	})
	if err != nil {
		return 0, fmt.Errorf("count projects admin: %w", err)
	}
	return n, nil
}

func (r *pgRepo) GetAdmin(ctx context.Context, id uuid.UUID) (*AdminProjectDetail, error) {
	row, err := r.queries.GetProjectAdmin(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query project admin: %w", err)
	}
	releaseRows, err := r.queries.ListReleasesForProjectAdmin(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list releases for project admin: %w", err)
	}
	releases := make([]AdminRelease, len(releaseRows))
	for i, rr := range releaseRows {
		releases[i] = AdminRelease{
			ID: rr.ID, Version: rr.Version, Status: rr.Status,
			ServiceID: deref(rr.ServiceID), CreatedAt: rr.CreatedAt,
		}
	}
	return &AdminProjectDetail{
		Project:    fromSQLC(row.Project),
		OwnerPhone: deref(row.OwnerPhone),
		OwnerEmail: deref(row.OwnerEmail),
		Releases:   releases,
	}, nil
}

func (r *pgRepo) ListMonitoring(ctx context.Context, f AdminListFilter) ([]MonitoringProject, error) {
	rows, err := r.queries.AdminMonitoringProjects(ctx, sqlcdb.AdminMonitoringProjectsParams{
		Search:        strPtr(f.Search),
		OwnerID:       f.OwnerID,
		IsTemplate:    f.IsTemplate,
		HasRelease:    f.HasRelease,
		CreatedAfter:  f.CreatedAfter,
		CreatedBefore: f.CreatedBefore,
		Limit:         f.Limit,
		Offset:        f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list monitoring projects: %w", err)
	}
	out := make([]MonitoringProject, len(rows))
	for i, row := range rows {
		out[i] = MonitoringProject{
			Project:       fromSQLC(row.Project),
			OwnerPhone:    deref(row.OwnerPhone),
			ReleaseStatus: row.ReleaseStatus,
			CPUCores:      row.CpuCores,
			MemoryBytes:   row.MemoryBytes,
			DiskBytes:     row.DiskBytes,
			NetBytes:      row.NetBytes,
		}
	}
	return out, nil
}

func (r *pgRepo) DiskHistory(ctx context.Context, projectID uuid.UUID, since time.Time) ([]MetricPoint, error) {
	rows, err := r.queries.ProjectDiskHistory(ctx, sqlcdb.ProjectDiskHistoryParams{
		ProjectID: projectID,
		Since:     since,
	})
	if err != nil {
		return nil, fmt.Errorf("project disk history: %w", err)
	}
	out := make([]MetricPoint, len(rows))
	for i, row := range rows {
		out[i] = MetricPoint{Timestamp: row.CreatedAt.Unix(), Value: float64(row.DiskBytes)}
	}
	return out, nil
}

func mapOne(row sqlcdb.Project, err error) (*Project, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query project: %w", err)
	}
	p := fromSQLC(row)
	return &p, nil
}

func fromSQLC(r sqlcdb.Project) Project {
	return Project{
		ID:                 r.ID,
		UUID:               r.Uuid,
		Name:               r.Name,
		Description:        deref(r.Description),
		Username:           deref(r.Username),
		OwnerID:            r.OwnerID,
		Flow:               json.RawMessage(r.Flow),
		IsTemplate:         r.IsTemplate,
		ReleaseWebhookUUID: r.ReleaseWebhookUuid,
		DebugWebhookUUID:   r.DebugWebhookUuid,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		LegacyID:           r.LegacyID,
	}
}

func flowOrEmpty(f json.RawMessage) []byte {
	if len(f) == 0 {
		return []byte(`{"nodes":[],"edges":[]}`)
	}
	return f
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
