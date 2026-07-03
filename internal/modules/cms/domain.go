// Package cms ports Django's cms app: collection/field SCHEMA definitions
// live in Postgres, the DOCUMENTS themselves live in MongoDB — one Mongo
// database per project (projects.mongo_database), one Mongo collection per
// collection.slug. See mongo.go for the document side.
package cms

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Field kinds (mirrors Django's FieldKind TextChoices, rule #6: enum as Go
// consts, no separate lookup table).
const (
	FieldText     = "text"
	FieldLongText = "longtext"
	FieldInt      = "int"
	FieldFloat    = "float"
	FieldBool     = "bool"
	FieldDateTime = "datetime"
	FieldDate     = "date"
	FieldEmail    = "email"
	FieldURL      = "url"
	FieldPhone    = "phone"
	FieldEnum     = "enum"
	FieldJSON     = "json"
	FieldArray    = "array"
	FieldRelation = "relation"
	FieldFile     = "file"
	FieldImage    = "image"
	FieldAudio    = "audio"
	FieldVideo    = "video"
	FieldGeo      = "geo"
)

// ValidFieldKinds enumerates every accepted Field.Type value.
var ValidFieldKinds = []string{
	FieldText, FieldLongText, FieldInt, FieldFloat, FieldBool, FieldDateTime, FieldDate,
	FieldEmail, FieldURL, FieldPhone, FieldEnum, FieldJSON, FieldArray, FieldRelation,
	FieldFile, FieldImage, FieldAudio, FieldVideo, FieldGeo,
}

// Cascade modes for a RELATION field (mirrors Django's CascadeMode).
const (
	CascadeCascade  = "cascade"
	CascadeRestrict = "restrict"
	CascadeSetNull  = "set_null"
)

// Index kinds applied on Mongo for a field (mirrors Django's IndexKind).
const (
	IndexNone   = "none"
	IndexAsc    = "asc"
	IndexDesc   = "desc"
	IndexText   = "text"
	IndexHashed = "hashed"
	IndexUnique = "unique"
	IndexTTL    = "ttl"
)

// Export task lifecycle (mirrors a Celery/async job status column).
const (
	ExportPending    = "pending"
	ExportProcessing = "processing"
	ExportDone       = "done"
	ExportFailed     = "failed"
)

// Collection is a schema (like a "table") inside a project. Documents that
// belong to it live in Mongo, in the project's database, under a collection
// named Slug.
type Collection struct {
	ID            uuid.UUID
	ProjectID     uuid.UUID
	Name          string
	Slug          string
	Description   string
	PrimaryField  string
	System        bool
	SchemaVersion int32
	Fields        []Field
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Field is one column of a Collection's schema.
type Field struct {
	ID                   uuid.UUID
	CollectionID         uuid.UUID
	Name                 string
	Label                string
	Type                 string
	Required             bool
	Default              json.RawMessage
	Validators           json.RawMessage
	RelationCollectionID *uuid.UUID
	Cascade              string
	Index                string
	Order                int32
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// IsRelation reports whether this field references another collection.
func (f Field) IsRelation() bool { return f.Type == FieldRelation }

// ExportTask tracks an async Excel export of a collection's documents.
type ExportTask struct {
	ID           uuid.UUID
	CollectionID uuid.UUID
	OwnerID      uuid.UUID
	Status       string
	Progress     int32
	DownloadURL  *string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateCollectionInput is the owner-scoped collection create payload.
type CreateCollectionInput struct {
	OwnerID      uuid.UUID
	ProjectID    uuid.UUID
	Name         string
	Slug         string
	Description  string
	PrimaryField string
	// System marks an undeletable, engine-managed schema (Django's
	// Collection.system, set by core/apps/cms/services/system_collections.py
	// for the `users`/`chat` collections). The public HTTP create handler
	// never sets this — only internal provisioning (project.Usecase.Create)
	// does, via the cms.Usecase — so it can never be forged by a caller.
	System bool
}

// UpdateCollectionInput carries optional fields; nil pointers are unchanged.
type UpdateCollectionInput struct {
	Name         *string
	Description  *string
	PrimaryField *string
}

// CreateFieldInput is the owner-scoped field create payload.
type CreateFieldInput struct {
	OwnerID              uuid.UUID
	CollectionID         uuid.UUID
	Name                 string
	Label                string
	Type                 string
	Required             bool
	Default              json.RawMessage
	Validators           json.RawMessage
	RelationCollectionID *uuid.UUID
	Cascade              string
	Index                string
	Order                int32
}

// UpdateFieldInput carries optional fields; nil pointers are unchanged.
type UpdateFieldInput struct {
	Label                *string
	Required             *bool
	Default              json.RawMessage
	Validators           json.RawMessage
	RelationCollectionID *uuid.UUID
	ClearRelation        bool
	Cascade              *string
	Index                *string
	Order                *int32
}

// Repository is the Postgres persistence port for collection/field/export
// task SCHEMA data. Every read/write is scoped through projects.owner_id
// (rule #4) so a caller can never touch another owner's schema.
type Repository interface {
	CreateCollection(ctx context.Context, in CreateCollectionInput) (*Collection, error)
	GetCollectionBySlug(ctx context.Context, ownerID, projectID uuid.UUID, slug string) (*Collection, error)
	GetCollectionByID(ctx context.Context, ownerID, id uuid.UUID) (*Collection, error)
	ListCollections(ctx context.Context, ownerID, projectID uuid.UUID) ([]Collection, error)
	UpdateCollection(ctx context.Context, ownerID, id uuid.UUID, in UpdateCollectionInput) (*Collection, error)
	BumpSchemaVersion(ctx context.Context, id uuid.UUID) error
	// DeleteCollection soft-deletes the schema row. It fails with
	// database.ErrNotFound both when the collection is missing/foreign AND
	// when it is a system collection (can_delete == false), since the SQL
	// guard filters on system = FALSE.
	DeleteCollection(ctx context.Context, ownerID, id uuid.UUID) error
	// ProjectMongoDatabase resolves the Mongo database name for a project,
	// verifying ownership. Empty string means the project has not been
	// provisioned a dedicated Mongo database yet.
	//
	// Deprecated: documents now live in one shared Mongo database (see
	// ProjectMongoTenant / migration 000033) matching Django's
	// CMS_SHARED_DB, not a per-project database. Kept only because the
	// mongo_database column may still be read by other code paths; CMS
	// itself no longer calls this.
	ProjectMongoDatabase(ctx context.Context, ownerID, projectID uuid.UUID) (string, error)
	// ProjectMongoTenant resolves the CMS Mongo tenant key for a project,
	// verifying ownership: legacy_id (the Django project pk) when the
	// project was imported from Django, else nil (the caller then falls
	// back to the project's own UUID string — see usecase.go
	// resolveMongoTenant).
	ProjectMongoTenant(ctx context.Context, ownerID, projectID uuid.UUID) (legacyID *int32, err error)

	CreateField(ctx context.Context, in CreateFieldInput) (*Field, error)
	GetFieldByName(ctx context.Context, ownerID, collectionID uuid.UUID, name string) (*Field, error)
	ListFields(ctx context.Context, collectionID uuid.UUID) ([]Field, error)
	UpdateField(ctx context.Context, ownerID, id uuid.UUID, in UpdateFieldInput) (*Field, error)
	DeleteField(ctx context.Context, ownerID, id uuid.UUID) error

	CreateExportTask(ctx context.Context, ownerID, collectionID uuid.UUID) (*ExportTask, error)
	GetExportTask(ctx context.Context, ownerID, id uuid.UUID) (*ExportTask, error)
	UpdateExportTaskStatus(ctx context.Context, id uuid.UUID, status string, progress int32, downloadURL, errMsg *string) (*ExportTask, error)
}

// DocumentQuery narrows/paginates/sorts a document listing.
type DocumentQuery struct {
	Filter         map[string]any
	Sort           []DocumentSort
	Limit          int64
	Skip           int64
	IncludeDeleted bool
}

// DocumentSort is one `{field: direction}` sort key; Asc false means descending.
type DocumentSort struct {
	Field string
	Asc   bool
}

// DocumentList is a page of documents plus the total matching count.
type DocumentList struct {
	Items []map[string]any
	Total int64
}

// DocumentRepository is the MongoDB persistence port for document data.
// dbName is now always the shared CMS database (see usecase.go
// resolveMongoTenant); collName is the Collection's Slug. tenant is the
// mandatory `_project_id` scoping value — every method stamps/filters by it
// so one project can never see another's documents in the shared collection
// (mirrors Django's services/mongo.py `_scoped()`/`_tenant()`).
type DocumentRepository interface {
	InsertOne(ctx context.Context, dbName, collName string, tenant any, doc map[string]any) (string, error)
	InsertMany(ctx context.Context, dbName, collName string, tenant any, docs []map[string]any) ([]string, error)
	Find(ctx context.Context, dbName, collName string, tenant any, q DocumentQuery) (DocumentList, error)
	FindOne(ctx context.Context, dbName, collName string, tenant any, id string, includeDeleted bool) (map[string]any, error)
	Count(ctx context.Context, dbName, collName string, tenant any, filter map[string]any, includeDeleted bool) (int64, error)
	UpdateOne(ctx context.Context, dbName, collName string, tenant any, id string, patch map[string]any) (map[string]any, error)
	SoftDeleteOne(ctx context.Context, dbName, collName string, tenant any, id string) (bool, error)
	// RestoreOne un-deletes a soft-deleted document.
	RestoreOne(ctx context.Context, dbName, collName string, tenant any, id string) (bool, error)
	Aggregate(ctx context.Context, dbName, collName string, tenant any, pipeline []map[string]any, limit int64) ([]map[string]any, error)
	// DropCollection removes this tenant's documents from the shared Mongo
	// collection — called when a Collection (schema) is deleted. It cannot
	// drop the collection outright since other projects' documents live in
	// the same collection.
	DropCollection(ctx context.Context, dbName, collName string, tenant any) error
}
