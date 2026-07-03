package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries (queries/plugin.sql). Project-module
// ownership is enforced entirely in SQL (joined against `projects`) so this
// adapter never has to import the project package.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

const uniqueViolation = "23505"

func (r *pgRepo) CreateModule(ctx context.Context, in CreateModuleInput) (*Module, error) {
	row, err := r.queries.CreateModule(ctx, sqlcdb.CreateModuleParams{
		ID:          uuid.New(),
		Slug:        in.Slug,
		Name:        in.Name,
		Description: strPtr(in.Description),
		Icon:        strPtr(in.Icon),
		GithubUrl:   strPtr(in.GithubURL),
		Image:       in.Image,
		Port:        portOrDefault(in.Port),
		AuthToken:   strPtr(in.AuthToken),
		Active:      in.Active,
		IsDefault:   in.IsDefault,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("create module: %w", err)
	}
	m := moduleFromSQLC(row)
	return &m, nil
}

func (r *pgRepo) GetModuleBySlug(ctx context.Context, slug string) (*Module, error) {
	return mapModule(r.queries.GetModuleBySlug(ctx, slug))
}

func (r *pgRepo) GetModuleByID(ctx context.Context, id uuid.UUID) (*Module, error) {
	return mapModule(r.queries.GetModuleByID(ctx, id))
}

func (r *pgRepo) UpdateModule(ctx context.Context, id uuid.UUID, in UpdateModuleInput) (*Module, error) {
	return mapModule(r.queries.UpdateModule(ctx, sqlcdb.UpdateModuleParams{
		ID:          id,
		Name:        in.Name,
		Description: in.Description,
		Icon:        in.Icon,
		GithubUrl:   in.GithubURL,
		Image:       in.Image,
		Port:        in.Port,
		AuthToken:   in.AuthToken,
		Active:      in.Active,
		IsDefault:   in.IsDefault,
	}))
}

func (r *pgRepo) DeleteModule(ctx context.Context, id uuid.UUID) error {
	rows, err := r.queries.DeleteModule(ctx, id)
	if err != nil {
		return fmt.Errorf("delete module: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ListModules(ctx context.Context, f ModuleListFilter) ([]Module, error) {
	rows, err := r.queries.ListModules(ctx, sqlcdb.ListModulesParams{
		Search: strPtr(f.Search),
		Active: f.Active,
		Limit:  f.Limit,
		Offset: f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list modules: %w", err)
	}
	out := make([]Module, len(rows))
	for i, row := range rows {
		out[i] = moduleFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountModules(ctx context.Context, f ModuleListFilter) (int64, error) {
	n, err := r.queries.CountModules(ctx, sqlcdb.CountModulesParams{
		Search: strPtr(f.Search),
		Active: f.Active,
	})
	if err != nil {
		return 0, fmt.Errorf("count modules: %w", err)
	}
	return n, nil
}

func (r *pgRepo) ListModulesWithGithubURL(ctx context.Context) ([]Module, error) {
	rows, err := r.queries.ListModulesWithGithubURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("list modules with github url: %w", err)
	}
	out := make([]Module, len(rows))
	for i, row := range rows {
		out[i] = moduleFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) UpdateModuleManifest(ctx context.Context, id uuid.UUID, manifest json.RawMessage) (*Module, error) {
	return mapModule(r.queries.UpdateModuleManifest(ctx, sqlcdb.UpdateModuleManifestParams{
		ID:            id,
		ManifestCache: manifest,
	}))
}

func (r *pgRepo) GetProjectModuleForOwner(ctx context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error) {
	return mapProjectModule(r.queries.GetProjectModuleForOwner(ctx, sqlcdb.GetProjectModuleForOwnerParams{
		ProjectID: projectID,
		ModuleID:  moduleID,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) EnsureProjectModuleForOwner(ctx context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error) {
	return mapProjectModule(r.queries.EnsureProjectModuleForOwner(ctx, sqlcdb.EnsureProjectModuleForOwnerParams{
		ProjectID: projectID,
		ModuleID:  moduleID,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) UpdateProjectModuleDeployState(ctx context.Context, id uuid.UUID, in DeployStateInput) (*ProjectModule, error) {
	return mapProjectModule(r.queries.UpdateProjectModuleDeployState(ctx, sqlcdb.UpdateProjectModuleDeployStateParams{
		ID:        id,
		ServiceID: strPtr(in.ServiceID),
		Endpoint:  strPtr(in.Endpoint),
		Status:    in.Status,
	}))
}

func (r *pgRepo) SetProjectModuleDisabledForOwner(ctx context.Context, projectID, moduleID, ownerID uuid.UUID) (*ProjectModule, error) {
	return mapProjectModule(r.queries.SetProjectModuleDisabledForOwner(ctx, sqlcdb.SetProjectModuleDisabledForOwnerParams{
		ProjectID: projectID,
		ModuleID:  moduleID,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) ListProjectModulesForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectModule, error) {
	rows, err := r.queries.ListProjectModulesForOwner(ctx, sqlcdb.ListProjectModulesForOwnerParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list project modules: %w", err)
	}
	out := make([]ProjectModule, len(rows))
	for i, row := range rows {
		out[i] = projectModuleFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) ListEnabledProjectModulesForOwner(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectModule, error) {
	rows, err := r.queries.ListEnabledProjectModulesForOwner(ctx, sqlcdb.ListEnabledProjectModulesForOwnerParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list enabled project modules: %w", err)
	}
	out := make([]ProjectModule, len(rows))
	for i, row := range rows {
		out[i] = projectModuleFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) CountEnabledProjectModulesExcluding(ctx context.Context, moduleID, excludeProjectID uuid.UUID) (int64, error) {
	n, err := r.queries.CountEnabledProjectModulesExcluding(ctx, sqlcdb.CountEnabledProjectModulesExcludingParams{
		ModuleID:         moduleID,
		ExcludeProjectID: excludeProjectID,
	})
	if err != nil {
		return 0, fmt.Errorf("count enabled project modules: %w", err)
	}
	return n, nil
}

func (r *pgRepo) BulkMarkProjectModulesRunning(ctx context.Context, moduleID uuid.UUID, serviceID, endpoint string) (int64, error) {
	rows, err := r.queries.BulkMarkProjectModulesRunning(ctx, sqlcdb.BulkMarkProjectModulesRunningParams{
		ModuleID:  moduleID,
		ServiceID: strPtr(serviceID),
		Endpoint:  strPtr(endpoint),
	})
	if err != nil {
		return 0, fmt.Errorf("bulk mark project modules running: %w", err)
	}
	return rows, nil
}

func mapModule(row sqlcdb.Module, err error) (*Module, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query module: %w", err)
	}
	m := moduleFromSQLC(row)
	return &m, nil
}

func mapProjectModule(row sqlcdb.ProjectModule, err error) (*ProjectModule, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query project module: %w", err)
	}
	pm := projectModuleFromSQLC(row)
	return &pm, nil
}

func moduleFromSQLC(r sqlcdb.Module) Module {
	return Module{
		ID:                r.ID,
		Slug:              r.Slug,
		Name:              r.Name,
		Description:       deref(r.Description),
		Icon:              deref(r.Icon),
		GithubURL:         deref(r.GithubUrl),
		Image:             r.Image,
		Port:              r.Port,
		AuthToken:         deref(r.AuthToken),
		ManifestCache:     json.RawMessage(r.ManifestCache),
		ManifestFetchedAt: r.ManifestFetchedAt,
		Active:            r.Active,
		IsDefault:         r.IsDefault,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func projectModuleFromSQLC(r sqlcdb.ProjectModule) ProjectModule {
	return ProjectModule{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		ModuleID:  r.ModuleID,
		Enabled:   r.Enabled,
		Config:    json.RawMessage(r.Config),
		ServiceID: deref(r.ServiceID),
		Endpoint:  deref(r.Endpoint),
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func portOrDefault(p int32) int32 {
	if p <= 0 {
		return 8100
	}
	return p
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
