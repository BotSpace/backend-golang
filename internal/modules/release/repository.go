package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// ---- Release ----------------------------------------------------------

func (r *pgRepo) CreateRelease(ctx context.Context, projectID, ownerID uuid.UUID, version int32) (*Release, error) {
	return mapRelease(r.queries.CreateRelease(ctx, sqlcdb.CreateReleaseParams{
		ID:        uuid.New(),
		ProjectID: projectID,
		OwnerID:   ownerID,
		Version:   version,
	}))
}

func (r *pgRepo) GetReleaseForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Release, error) {
	return mapRelease(r.queries.GetReleaseForOwner(ctx, sqlcdb.GetReleaseForOwnerParams{ID: id, OwnerID: ownerID}))
}

func (r *pgRepo) ListReleasesForProject(ctx context.Context, projectID, ownerID uuid.UUID) ([]Release, error) {
	rows, err := r.queries.ListReleasesForProject(ctx, sqlcdb.ListReleasesForProjectParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	out := make([]Release, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCRelease(row)
	}
	return out, nil
}

func (r *pgRepo) LatestReleaseVersion(ctx context.Context, projectID, ownerID uuid.UUID) (int32, error) {
	v, err := r.queries.LatestReleaseVersion(ctx, sqlcdb.LatestReleaseVersionParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return 0, fmt.Errorf("latest release version: %w", err)
	}
	return v, nil
}

func (r *pgRepo) UpdateReleaseDeployResult(ctx context.Context, id uuid.UUID, serviceID string, status Status) (*Release, error) {
	return mapRelease(r.queries.UpdateReleaseDeployResult(ctx, sqlcdb.UpdateReleaseDeployResultParams{
		ID:        id,
		ServiceID: strPtr(serviceID),
		Status:    string(status),
	}))
}

func (r *pgRepo) UpdateReleaseStatusForOwner(ctx context.Context, id, ownerID uuid.UUID, status Status) (*Release, error) {
	return mapRelease(r.queries.UpdateReleaseStatusForOwner(ctx, sqlcdb.UpdateReleaseStatusForOwnerParams{
		ID:      id,
		OwnerID: ownerID,
		Status:  string(status),
	}))
}

func (r *pgRepo) StopActiveReleasesExcept(ctx context.Context, projectID, excludeID uuid.UUID) ([]Release, error) {
	rows, err := r.queries.StopActiveReleasesExcept(ctx, sqlcdb.StopActiveReleasesExceptParams{
		ProjectID: projectID,
		ExcludeID: excludeID,
	})
	if err != nil {
		return nil, fmt.Errorf("stop active releases: %w", err)
	}
	out := make([]Release, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCRelease(row)
	}
	return out, nil
}

// ---- Debug --------------------------------------------------------------

func (r *pgRepo) GetDebugForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	return mapDebug(r.queries.GetDebugForOwner(ctx, sqlcdb.GetDebugForOwnerParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) GetOrCreateDebugForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (*Debug, error) {
	return mapDebug(r.queries.GetOrCreateDebugForOwner(ctx, sqlcdb.GetOrCreateDebugForOwnerParams{
		ID:        uuid.New(),
		ProjectID: projectID,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) UpdateDebugDeployResult(ctx context.Context, id uuid.UUID, serviceID string, status Status) (*Debug, error) {
	return mapDebug(r.queries.UpdateDebugDeployResult(ctx, sqlcdb.UpdateDebugDeployResultParams{
		ID:        id,
		ServiceID: strPtr(serviceID),
		Status:    string(status),
	}))
}

func (r *pgRepo) UpdateDebugStatusForOwner(ctx context.Context, id, ownerID uuid.UUID, status Status) (*Debug, error) {
	return mapDebug(r.queries.UpdateDebugStatusForOwner(ctx, sqlcdb.UpdateDebugStatusForOwnerParams{
		ID:      id,
		OwnerID: ownerID,
		Status:  string(status),
	}))
}

// ---- Environment ----------------------------------------------------------

func (r *pgRepo) UpsertEnvironment(ctx context.Context, in UpsertEnvironmentInput) (*Environment, error) {
	return mapEnvironment(r.queries.UpsertEnviromentForOwner(ctx, sqlcdb.UpsertEnviromentForOwnerParams{
		ID:        uuid.New(),
		ProjectID: in.ProjectID,
		OwnerID:   in.OwnerID,
		UserID:    in.UserID,
		Key:       in.Key,
		Value:     in.Value,
		Secret:    in.Secret,
	}))
}

func (r *pgRepo) ListEnvironmentsForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]Environment, error) {
	rows, err := r.queries.ListEnviromentsForOwner(ctx, sqlcdb.ListEnviromentsForOwnerParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	out := make([]Environment, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCEnvironment(row)
	}
	return out, nil
}

func (r *pgRepo) GetEnvironmentForOwner(ctx context.Context, key string, projectID, ownerID uuid.UUID) (*Environment, error) {
	return mapEnvironment(r.queries.GetEnviromentForOwner(ctx, sqlcdb.GetEnviromentForOwnerParams{
		Key:       key,
		ProjectID: projectID,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) DeleteEnvironmentForOwner(ctx context.Context, key string, projectID, ownerID uuid.UUID) error {
	rows, err := r.queries.DeleteEnviromentForOwner(ctx, sqlcdb.DeleteEnviromentForOwnerParams{
		Key:       key,
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return fmt.Errorf("delete environment: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ListEnvironmentsByProject(ctx context.Context, projectID uuid.UUID) ([]Environment, error) {
	rows, err := r.queries.ListEnviromentsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list environments by project: %w", err)
	}
	out := make([]Environment, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCEnvironment(row)
	}
	return out, nil
}

func (r *pgRepo) GetProjectFlowForOwner(ctx context.Context, projectID, ownerID uuid.UUID) (json.RawMessage, error) {
	flow, err := r.queries.GetProjectFlowForOwner(ctx, sqlcdb.GetProjectFlowForOwnerParams{
		ID:      projectID,
		OwnerID: ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project flow: %w", err)
	}
	return json.RawMessage(flow), nil
}

func (r *pgRepo) ProjectOwnerID(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error) {
	ownerID, err := r.queries.ReleaseGetProjectOwnerID(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get project owner id: %w", err)
	}
	return ownerID, nil
}

func (r *pgRepo) GetProjectWebhookUUIDs(ctx context.Context, projectID, ownerID uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	row, err := r.queries.GetProjectWebhookUUIDsForOwner(ctx, sqlcdb.GetProjectWebhookUUIDsForOwnerParams{
		ID:      projectID,
		OwnerID: ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, database.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("get project webhook uuids: %w", err)
	}
	return row.ReleaseWebhookUuid, row.DebugWebhookUuid, nil
}

// ---- mapping helpers --------------------------------------------------

func mapRelease(row sqlcdb.Relase, err error) (*Release, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query release: %w", err)
	}
	rel := fromSQLCRelease(row)
	return &rel, nil
}

func mapDebug(row sqlcdb.Debug, err error) (*Debug, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query debug: %w", err)
	}
	d := fromSQLCDebug(row)
	return &d, nil
}

func mapEnvironment(row sqlcdb.Enviroment, err error) (*Environment, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query environment: %w", err)
	}
	e := fromSQLCEnvironment(row)
	return &e, nil
}

func fromSQLCRelease(r sqlcdb.Relase) Release {
	return Release{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		Version:   r.Version,
		Flow:      json.RawMessage(r.Flow),
		ServiceID: deref(r.ServiceID),
		Status:    Status(r.Status),
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func fromSQLCDebug(d sqlcdb.Debug) Debug {
	return Debug{
		ID:        d.ID,
		ProjectID: d.ProjectID,
		FlowHash:  deref(d.FlowHash),
		ServiceID: deref(d.ServiceID),
		Status:    Status(d.Status),
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

func fromSQLCEnvironment(e sqlcdb.Enviroment) Environment {
	return Environment{
		ID:        e.ID,
		ProjectID: e.ProjectID,
		UserID:    e.UserID,
		Key:       e.Key,
		Value:     e.Value,
		Secret:    e.Secret,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
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
