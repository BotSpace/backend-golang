package file

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo wraps sqlc-generated queries. sqlc handles SQL <-> Go mapping; this
// adapter only translates domain <-> persistence models and pgx errors into
// domain errors. The usecase never sees sqlc, so it stays trivially testable.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

func (r *pgRepo) CreateFile(ctx context.Context, in CreateFileInput) (*File, error) {
	row, err := r.queries.CreateFile(ctx, sqlcdb.CreateFileParams{
		ID:           uuid.New(),
		Uuid:         uuid.New(),
		ProjectID:    in.ProjectID,
		OwnerID:      in.OwnerID,
		FilePath:     in.FilePath,
		OriginalName: in.OriginalName,
		Filesize:     in.FileSize,
		Filetype:     in.FileType,
		ExpiresAt:    in.ExpiresAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// INSERT ... SELECT ... WHERE EXISTS produced no row: the project
		// does not belong to the caller (or does not exist).
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}
	f := fromSQLCFile(row)
	return &f, nil
}

func (r *pgRepo) GetFileByUUID(ctx context.Context, uid uuid.UUID) (*File, error) {
	return mapOneFile(r.queries.GetFileByUUID(ctx, uid))
}

func (r *pgRepo) GetFileByUUIDForOwner(ctx context.Context, uid, ownerID uuid.UUID) (*File, error) {
	return mapOneFile(r.queries.GetFileByUUIDForOwner(ctx, sqlcdb.GetFileByUUIDForOwnerParams{
		Uuid: uid, OwnerID: ownerID,
	}))
}

func (r *pgRepo) UpdateFileByUUIDForOwner(ctx context.Context, uid, ownerID uuid.UUID, in UpdateFileInput) (*File, error) {
	return mapOneFile(r.queries.UpdateFileByUUIDForOwner(ctx, sqlcdb.UpdateFileByUUIDForOwnerParams{
		Uuid:     uid,
		OwnerID:  ownerID,
		Filetype: in.FileType,
	}))
}

func (r *pgRepo) DeleteFileByUUIDForOwner(ctx context.Context, uid, ownerID uuid.UUID) (*File, error) {
	return mapOneFile(r.queries.DeleteFileByUUIDForOwner(ctx, sqlcdb.DeleteFileByUUIDForOwnerParams{
		Uuid: uid, OwnerID: ownerID,
	}))
}

func (r *pgRepo) ListFiles(ctx context.Context, f ListFileFilter) ([]File, error) {
	rows, err := r.queries.ListFiles(ctx, sqlcdb.ListFilesParams{
		OwnerID:   f.OwnerID,
		ProjectID: f.ProjectID,
		Filetype:  f.FileType,
		Limit:     f.Limit,
		Offset:    f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	out := make([]File, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCFile(row)
	}
	return out, nil
}

func (r *pgRepo) CountFiles(ctx context.Context, f ListFileFilter) (int64, error) {
	n, err := r.queries.CountFiles(ctx, sqlcdb.CountFilesParams{
		OwnerID:   f.OwnerID,
		ProjectID: f.ProjectID,
		Filetype:  f.FileType,
	})
	if err != nil {
		return 0, fmt.Errorf("count files: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CreateResource(ctx context.Context, in CreateResourceInput) (*Resource, error) {
	row, err := r.queries.CreateResource(ctx, sqlcdb.CreateResourceParams{
		ID:        uuid.New(),
		ProjectID: in.ProjectID,
		OwnerID:   in.OwnerID,
		Hostname:  in.Hostname,
		Ram:       in.RAM,
		Cpu:       in.CPU,
		NetRx:     in.NetRX,
		NetTx:     in.NetTX,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}
	res := fromSQLCResource(row)
	return &res, nil
}

func (r *pgRepo) GetResourceForOwner(ctx context.Context, id, ownerID uuid.UUID) (*Resource, error) {
	return mapOneResource(r.queries.GetResourceForOwner(ctx, sqlcdb.GetResourceForOwnerParams{
		ID: id, OwnerID: ownerID,
	}))
}

func (r *pgRepo) ListResources(ctx context.Context, f ListResourceFilter) ([]Resource, error) {
	rows, err := r.queries.ListResources(ctx, sqlcdb.ListResourcesParams{
		OwnerID:   f.OwnerID,
		ProjectID: f.ProjectID,
		Limit:     f.Limit,
		Offset:    f.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	out := make([]Resource, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCResource(row)
	}
	return out, nil
}

func (r *pgRepo) CountResources(ctx context.Context, f ListResourceFilter) (int64, error) {
	n, err := r.queries.CountResources(ctx, sqlcdb.CountResourcesParams{
		OwnerID:   f.OwnerID,
		ProjectID: f.ProjectID,
	})
	if err != nil {
		return 0, fmt.Errorf("count resources: %w", err)
	}
	return n, nil
}

func (r *pgRepo) IsProjectOwnedBy(ctx context.Context, projectID, ownerID uuid.UUID) (bool, error) {
	ok, err := r.queries.FileProjectOwnedByOwner(ctx, sqlcdb.FileProjectOwnedByOwnerParams{
		ProjectID: projectID, OwnerID: ownerID,
	})
	if err != nil {
		return false, fmt.Errorf("check project ownership: %w", err)
	}
	return ok, nil
}

func (r *pgRepo) SumFileSize(ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID) (int64, error) {
	total, err := r.queries.SumFileSizeForOwner(ctx, sqlcdb.SumFileSizeForOwnerParams{
		OwnerID: ownerID, ProjectID: projectID,
	})
	if err != nil {
		return 0, fmt.Errorf("sum file size: %w", err)
	}
	return total, nil
}

func (r *pgRepo) LatestResourceByHostname(
	ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time,
) ([]Resource, error) {
	rows, err := r.queries.LatestResourceByHostnameForOwner(ctx, sqlcdb.LatestResourceByHostnameForOwnerParams{
		OwnerID: ownerID, ProjectID: projectID, Since: since,
	})
	if err != nil {
		return nil, fmt.Errorf("latest resource by hostname: %w", err)
	}
	out := make([]Resource, len(rows))
	for i, row := range rows {
		out[i] = fromSQLCResource(row)
	}
	return out, nil
}

func (r *pgRepo) ResourceHourly(
	ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time,
) ([]HourPoint, error) {
	rows, err := r.queries.ResourceHourlyForOwner(ctx, sqlcdb.ResourceHourlyForOwnerParams{
		OwnerID: ownerID, ProjectID: projectID, Since: since,
	})
	if err != nil {
		return nil, fmt.Errorf("resource hourly: %w", err)
	}
	out := make([]HourPoint, len(rows))
	for i, row := range rows {
		out[i] = HourPoint{Hour: row.Hour, RAM: row.Ram, CPU: row.Cpu, NetRX: row.NetRx, NetTX: row.NetTx}
	}
	return out, nil
}

func (r *pgRepo) StorageHourly(
	ctx context.Context, ownerID uuid.UUID, projectID *uuid.UUID, since time.Time,
) ([]StorageHourPoint, error) {
	rows, err := r.queries.StorageHourlyForOwner(ctx, sqlcdb.StorageHourlyForOwnerParams{
		OwnerID: ownerID, ProjectID: projectID, Since: since,
	})
	if err != nil {
		return nil, fmt.Errorf("storage hourly: %w", err)
	}
	out := make([]StorageHourPoint, len(rows))
	for i, row := range rows {
		out[i] = StorageHourPoint{Hour: row.Hour, Storage: row.Storage}
	}
	return out, nil
}

func mapOneFile(row sqlcdb.File, err error) (*File, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query file: %w", err)
	}
	f := fromSQLCFile(row)
	return &f, nil
}

func mapOneResource(row sqlcdb.Resource, err error) (*Resource, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query resource: %w", err)
	}
	res := fromSQLCResource(row)
	return &res, nil
}

func fromSQLCFile(r sqlcdb.File) File {
	return File{
		ID:           r.ID,
		UUID:         r.Uuid,
		ProjectID:    r.ProjectID,
		OwnerID:      r.UserID,
		FilePath:     r.FilePath,
		OriginalName: r.OriginalName,
		FileSize:     r.Filesize,
		FileType:     r.Filetype,
		ExpiresAt:    r.ExpiresAt,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

func fromSQLCResource(r sqlcdb.Resource) Resource {
	return Resource{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		OwnerID:   r.UserID,
		Hostname:  r.Hostname,
		RAM:       r.Ram,
		CPU:       r.Cpu,
		NetRX:     r.NetRx,
		NetTX:     r.NetTx,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}
