package disksnap

import (
	"context"

	sqlc "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRepo struct {
	q *sqlc.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{q: sqlc.New(pool)}
}

func (r *pgRepo) ListReleasedProjectsForDisk(ctx context.Context) ([]ReleasedProject, error) {
	rows, err := r.q.ListReleasedProjectsForDisk(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReleasedProject, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReleasedProject{ID: row.ID, LegacyID: row.LegacyID})
	}
	return out, nil
}

func (r *pgRepo) SumProjectFileBytes(ctx context.Context, projectID uuid.UUID) (int64, error) {
	return r.q.SumProjectFileBytes(ctx, projectID)
}

func (r *pgRepo) InsertDiskSnapshot(ctx context.Context, projectID uuid.UUID, filesBytes, cmsBytes int64) error {
	return r.q.InsertDiskSnapshot(ctx, sqlc.InsertDiskSnapshotParams{
		ProjectID:      projectID,
		DiskBytes:      filesBytes + cmsBytes,
		DiskFilesBytes: filesBytes,
		DiskCmsBytes:   cmsBytes,
	})
}
