package cms

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

// pgRepo wraps sqlc-generated queries for the collection/field/export_task
// tables. Document data (Mongo) is a separate adapter — see mongo.go.
type pgRepo struct {
	queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
	return &pgRepo{queries: sqlcdb.New(pool)}
}

// ── Collection ────────────────────────────────────────────────────────────

func (r *pgRepo) CreateCollection(ctx context.Context, in CreateCollectionInput) (*Collection, error) {
	row, err := r.queries.CmsCreateCollection(ctx, sqlcdb.CmsCreateCollectionParams{
		ID:           uuid.New(),
		ProjectID:    in.ProjectID,
		OwnerID:      in.OwnerID,
		Name:         in.Name,
		Slug:         in.Slug,
		Description:  in.Description,
		PrimaryField: in.PrimaryField,
		System:       false,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// INSERT ... SELECT ... WHERE EXISTS produced no row: the project
		// does not belong to the caller (or does not exist).
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}
	c := collectionFromSQLC(row)
	return &c, nil
}

func (r *pgRepo) GetCollectionBySlug(ctx context.Context, ownerID, projectID uuid.UUID, slug string) (*Collection, error) {
	return mapCollection(r.queries.CmsGetCollectionBySlugForOwner(ctx, sqlcdb.CmsGetCollectionBySlugForOwnerParams{
		ProjectID: projectID,
		Slug:      slug,
		OwnerID:   ownerID,
	}))
}

func (r *pgRepo) GetCollectionByID(ctx context.Context, ownerID, id uuid.UUID) (*Collection, error) {
	return mapCollection(r.queries.CmsGetCollectionByIDForOwner(ctx, sqlcdb.CmsGetCollectionByIDForOwnerParams{
		ID:      id,
		OwnerID: ownerID,
	}))
}

func (r *pgRepo) ListCollections(ctx context.Context, ownerID, projectID uuid.UUID) ([]Collection, error) {
	rows, err := r.queries.CmsListCollectionsForProject(ctx, sqlcdb.CmsListCollectionsForProjectParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	out := make([]Collection, len(rows))
	for i, row := range rows {
		out[i] = collectionFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) UpdateCollection(ctx context.Context, ownerID, id uuid.UUID, in UpdateCollectionInput) (*Collection, error) {
	return mapCollection(r.queries.CmsUpdateCollection(ctx, sqlcdb.CmsUpdateCollectionParams{
		ID:           id,
		OwnerID:      ownerID,
		Name:         in.Name,
		Description:  in.Description,
		PrimaryField: in.PrimaryField,
	}))
}

func (r *pgRepo) BumpSchemaVersion(ctx context.Context, id uuid.UUID) error {
	_, err := r.queries.CmsBumpCollectionSchemaVersion(ctx, id)
	if err != nil {
		return fmt.Errorf("bump schema version: %w", err)
	}
	return nil
}

func (r *pgRepo) DeleteCollection(ctx context.Context, ownerID, id uuid.UUID) error {
	rows, err := r.queries.CmsSoftDeleteCollection(ctx, sqlcdb.CmsSoftDeleteCollectionParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("soft delete collection: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

func (r *pgRepo) ProjectMongoDatabase(ctx context.Context, ownerID, projectID uuid.UUID) (string, error) {
	db, err := r.queries.CmsGetProjectMongoDatabaseForOwner(ctx, sqlcdb.CmsGetProjectMongoDatabaseForOwnerParams{
		ProjectID: projectID,
		OwnerID:   ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", database.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get project mongo database: %w", err)
	}
	return deref(db), nil
}

// ── Field ─────────────────────────────────────────────────────────────────

func (r *pgRepo) CreateField(ctx context.Context, in CreateFieldInput) (*Field, error) {
	row, err := r.queries.CmsCreateField(ctx, sqlcdb.CmsCreateFieldParams{
		ID:                   uuid.New(),
		CollectionID:         in.CollectionID,
		OwnerID:              in.OwnerID,
		Name:                 in.Name,
		Label:                in.Label,
		Type:                 in.Type,
		Required:             in.Required,
		DefaultValue:         rawOrNil(in.Default),
		Validators:           validatorsOrEmpty(in.Validators),
		RelationCollectionID: in.RelationCollectionID,
		Cascade:              in.Cascade,
		IndexKind:            in.Index,
		SortOrder:            in.Order,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("create field: %w", err)
	}
	f := fieldFromSQLC(row)
	return &f, nil
}

func (r *pgRepo) GetFieldByName(ctx context.Context, ownerID, collectionID uuid.UUID, name string) (*Field, error) {
	return mapField(r.queries.CmsGetFieldByNameForOwner(ctx, sqlcdb.CmsGetFieldByNameForOwnerParams{
		CollectionID: collectionID,
		Name:         name,
		OwnerID:      ownerID,
	}))
}

func (r *pgRepo) ListFields(ctx context.Context, collectionID uuid.UUID) ([]Field, error) {
	rows, err := r.queries.CmsListFieldsForCollection(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list fields: %w", err)
	}
	out := make([]Field, len(rows))
	for i, row := range rows {
		out[i] = fieldFromSQLC(row)
	}
	return out, nil
}

func (r *pgRepo) UpdateField(ctx context.Context, ownerID, id uuid.UUID, in UpdateFieldInput) (*Field, error) {
	relationID := in.RelationCollectionID
	// ClearRelation explicitly nulls the FK; sqlc's COALESCE(narg, col)
	// pattern can't distinguish "not provided" from "set to null" for a
	// pointer arg, so a plain field update leaves it untouched by default.
	// A dedicated clear is handled by the usecase re-issuing narg=nil only
	// when ClearRelation is set (see usecase.go UpdateField).
	_ = relationID
	return mapField(r.queries.CmsUpdateField(ctx, sqlcdb.CmsUpdateFieldParams{
		ID:                   id,
		OwnerID:              ownerID,
		Label:                in.Label,
		Required:             in.Required,
		DefaultValue:         rawOrNil(in.Default),
		Validators:           rawOrNil(in.Validators),
		RelationCollectionID: in.RelationCollectionID,
		Cascade:              in.Cascade,
		IndexKind:            in.Index,
		SortOrder:            in.Order,
	}))
}

func (r *pgRepo) DeleteField(ctx context.Context, ownerID, id uuid.UUID) error {
	rows, err := r.queries.CmsDeleteField(ctx, sqlcdb.CmsDeleteFieldParams{ID: id, OwnerID: ownerID})
	if err != nil {
		return fmt.Errorf("delete field: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

// ── Export task ───────────────────────────────────────────────────────────

func (r *pgRepo) CreateExportTask(ctx context.Context, ownerID, collectionID uuid.UUID) (*ExportTask, error) {
	row, err := r.queries.CmsCreateExportTask(ctx, sqlcdb.CmsCreateExportTaskParams{
		ID:           uuid.New(),
		CollectionID: collectionID,
		OwnerID:      ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("create export task: %w", err)
	}
	t := exportTaskFromSQLC(row)
	return &t, nil
}

func (r *pgRepo) GetExportTask(ctx context.Context, ownerID, id uuid.UUID) (*ExportTask, error) {
	row, err := r.queries.CmsGetExportTaskForOwner(ctx, sqlcdb.CmsGetExportTaskForOwnerParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get export task: %w", err)
	}
	t := exportTaskFromSQLC(row)
	return &t, nil
}

func (r *pgRepo) UpdateExportTaskStatus(
	ctx context.Context, id uuid.UUID, status string, progress int32, downloadURL, errMsg *string,
) (*ExportTask, error) {
	row, err := r.queries.CmsUpdateExportTaskStatus(ctx, sqlcdb.CmsUpdateExportTaskStatusParams{
		ID:           id,
		Status:       status,
		Progress:     int16(progress),
		DownloadUrl:  downloadURL,
		ErrorMessage: errMsg,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update export task status: %w", err)
	}
	t := exportTaskFromSQLC(row)
	return &t, nil
}

// ── mapping helpers ───────────────────────────────────────────────────────

func mapCollection(row sqlcdb.Collection, err error) (*Collection, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query collection: %w", err)
	}
	c := collectionFromSQLC(row)
	return &c, nil
}

func mapField(row sqlcdb.Field, err error) (*Field, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query field: %w", err)
	}
	f := fieldFromSQLC(row)
	return &f, nil
}

func collectionFromSQLC(r sqlcdb.Collection) Collection {
	return Collection{
		ID:            r.ID,
		ProjectID:     r.ProjectID,
		Name:          r.Name,
		Slug:          r.Slug,
		Description:   r.Description,
		PrimaryField:  r.PrimaryField,
		System:        r.System,
		SchemaVersion: r.SchemaVersion,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func fieldFromSQLC(r sqlcdb.Field) Field {
	return Field{
		ID:                   r.ID,
		CollectionID:         r.CollectionID,
		Name:                 r.Name,
		Label:                r.Label,
		Type:                 r.Type,
		Required:             r.Required,
		Default:              json.RawMessage(r.DefaultValue),
		Validators:           json.RawMessage(r.Validators),
		RelationCollectionID: r.RelationCollectionID,
		Cascade:              r.Cascade,
		Index:                r.IndexKind,
		Order:                r.SortOrder,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
	}
}

func exportTaskFromSQLC(r sqlcdb.CmsExportTask) ExportTask {
	return ExportTask{
		ID:           r.ID,
		CollectionID: r.CollectionID,
		OwnerID:      r.OwnerID,
		Status:       r.Status,
		Progress:     int32(r.Progress),
		DownloadURL:  r.DownloadUrl,
		ErrorMessage: r.ErrorMessage,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

func rawOrNil(b json.RawMessage) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func validatorsOrEmpty(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
