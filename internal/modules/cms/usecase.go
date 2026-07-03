package cms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// slugRe matches a Mongo-safe collection identifier: lowercase snake_case,
// starting with a letter (mirrors Django's SlugField usage for `name`).
var slugRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// DocValidationError is returned by document write methods when the payload
// fails field validation. Handlers render Fields via httpx.ErrorWithDetails.
type DocValidationError struct {
	Fields ValidationErrors
}

func (e *DocValidationError) Error() string { return "document validation failed" }

// FieldCreate is the usecase-facing "add field" payload. RelationCollection
// is referenced by slug (as a client would name it), resolved to an id
// internally — callers never need to know a collection's UUID.
type FieldCreate struct {
	Name                   string
	Label                  string
	Type                   string
	Required               bool
	Default                json.RawMessage
	Validators             json.RawMessage
	RelationCollectionSlug string
	Cascade                string
	Index                  string
	Order                  int32
}

// FieldPatch carries optional field-update values; nil pointers are unchanged.
type FieldPatch struct {
	Label                  *string
	Required               *bool
	Default                json.RawMessage
	Validators             json.RawMessage
	RelationCollectionSlug *string
	ClearRelation          bool
	Cascade                *string
	Index                  *string
	Order                  *int32
}

// Usecase exposes owner-scoped CMS operations: collection/field schema CRUD
// (Postgres) and document CRUD (Mongo), validated against the schema.
type Usecase interface {
	CreateCollection(ctx context.Context, ownerID, projectID uuid.UUID, in CreateCollectionInput) (*Collection, error)
	GetCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string) (*Collection, error)
	ListCollections(ctx context.Context, ownerID, projectID uuid.UUID) ([]Collection, error)
	UpdateCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string, in UpdateCollectionInput) (*Collection, error)
	DeleteCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string) error

	AddField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, in FieldCreate) (*Field, error)
	UpdateField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, fieldName string, in FieldPatch) (*Field, error)
	RemoveField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, fieldName string) error

	InsertDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, doc map[string]any) (map[string]any, error)
	FindDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, q DocumentQuery) (DocumentList, error)
	FindOneDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string) (map[string]any, error)
	CountDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, filter map[string]any) (int64, error)
	UpdateDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string, patch map[string]any) (map[string]any, error)
	DeleteDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string) error
	// RestoreDocument undoes a soft-delete (Django's restore()).
	RestoreDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string) error
	// InsertDocuments bulk-inserts, each validated against the schema
	// independently; the whole batch is rejected (no partial insert) if any
	// document fails validation, since Mongo's insertMany has no
	// transactional rollback across a shared collection here.
	InsertDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, docs []map[string]any) ([]map[string]any, error)
	// AggregateDocuments runs a caller-supplied pipeline, mandatorily scoped
	// to this project's tenant + alive documents (see mongo.go Aggregate).
	AggregateDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, pipeline []map[string]any, limit int64) ([]map[string]any, error)

	CreateExportTask(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string) (*ExportTask, error)
	GetExportTask(ctx context.Context, ownerID, taskID uuid.UUID) (*ExportTask, error)
}

type usecase struct {
	repo Repository
	docs DocumentRepository
	// sharedDBName is the single Mongo database every project's documents
	// live in (Django's CMS_SHARED_DB / botspace_cms). Empty falls back to
	// "botspace_cms" — see resolveMongoTenant.
	sharedDBName string
}

// NewUsecase wires the Postgres schema repository and the Mongo document
// repository behind a single owner-scoped API. sharedDBName is the shared
// CMS Mongo database name (config: engine.mongo_db_name / APP_ENGINE_MONGO_DB_NAME,
// default "botspace_cms") — the same database Django and the deployed engine
// pods use, so dashboard reads/writes see the exact same documents.
func NewUsecase(repo Repository, docs DocumentRepository, sharedDBName string) Usecase {
	return &usecase{repo: repo, docs: docs, sharedDBName: sharedDBName}
}

// ── Collection ───────────────────────────────────────────────────────────

func (u *usecase) CreateCollection(ctx context.Context, ownerID, projectID uuid.UUID, in CreateCollectionInput) (*Collection, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, httpx.BadRequest("cms.invalid_name", "name is required")
	}
	if !slugRe.MatchString(in.Slug) {
		return nil, httpx.BadRequest("cms.invalid_slug", "slug must be lowercase snake_case, starting with a letter")
	}
	in.OwnerID = ownerID
	in.ProjectID = projectID
	coll, err := u.repo.CreateCollection(ctx, in)
	if err != nil {
		return nil, mapNotFound(err, "project.not_found", "project not found")
	}
	return coll, nil
}

func (u *usecase) GetCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string) (*Collection, error) {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, slug)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	fields, err := u.repo.ListFields(ctx, coll.ID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	coll.Fields = fields
	return coll, nil
}

func (u *usecase) ListCollections(ctx context.Context, ownerID, projectID uuid.UUID) ([]Collection, error) {
	colls, err := u.repo.ListCollections(ctx, ownerID, projectID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	for i := range colls {
		fields, err := u.repo.ListFields(ctx, colls[i].ID)
		if err != nil {
			return nil, httpx.Internal(err)
		}
		colls[i].Fields = fields
	}
	return colls, nil
}

func (u *usecase) UpdateCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string, in UpdateCollectionInput) (*Collection, error) {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, slug)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	updated, err := u.repo.UpdateCollection(ctx, ownerID, coll.ID, in)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	return updated, nil
}

func (u *usecase) DeleteCollection(ctx context.Context, ownerID, projectID uuid.UUID, slug string) error {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, slug)
	if err != nil {
		return mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	if coll.System {
		return httpx.Forbidden("cms.system_collection", "system collection cannot be deleted")
	}
	if err := u.repo.DeleteCollection(ctx, ownerID, coll.ID); err != nil {
		return mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	// Best-effort: drop this project's documents from the shared Mongo
	// collection so no orphaned documents linger. The schema row is already
	// gone at this point, so a Mongo hiccup here does not leave
	// Postgres/Mongo inconsistent from the caller's view.
	dbName, tenant, err := u.resolveMongoTenant(ctx, ownerID, projectID)
	if err != nil {
		return nil
	}
	_ = u.docs.DropCollection(ctx, dbName, coll.Slug, tenant)
	return nil
}

// ── Field ────────────────────────────────────────────────────────────────

func (u *usecase) AddField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, in FieldCreate) (*Field, error) {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	if !contains(ValidFieldKinds, in.Type) {
		return nil, httpx.BadRequest("cms.invalid_field_type", fmt.Sprintf("unknown field type %q", in.Type))
	}

	var relationID *uuid.UUID
	if in.Type == FieldRelation {
		if in.RelationCollectionSlug == "" {
			return nil, httpx.BadRequest("cms.relation_target_required", "relation fields require relation_collection")
		}
		target, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, in.RelationCollectionSlug)
		if err != nil {
			return nil, mapNotFound(err, "cms.relation_target_not_found", "relation target collection not found")
		}
		relationID = &target.ID
	}

	cascade := in.Cascade
	if cascade == "" {
		cascade = CascadeRestrict
	}
	index := in.Index
	if index == "" {
		index = IndexNone
	}

	field, err := u.repo.CreateField(ctx, CreateFieldInput{
		OwnerID:              ownerID,
		CollectionID:         coll.ID,
		Name:                 in.Name,
		Label:                in.Label,
		Type:                 in.Type,
		Required:             in.Required,
		Default:              in.Default,
		Validators:           in.Validators,
		RelationCollectionID: relationID,
		Cascade:              cascade,
		Index:                index,
		Order:                in.Order,
	})
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	_ = u.repo.BumpSchemaVersion(ctx, coll.ID)
	return field, nil
}

func (u *usecase) UpdateField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, fieldName string, in FieldPatch) (*Field, error) {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	existing, err := u.repo.GetFieldByName(ctx, ownerID, coll.ID, fieldName)
	if err != nil {
		return nil, mapNotFound(err, "cms.field_not_found", "field not found")
	}

	relationID := existing.RelationCollectionID
	if in.ClearRelation {
		relationID = nil
	} else if in.RelationCollectionSlug != nil {
		target, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, *in.RelationCollectionSlug)
		if err != nil {
			return nil, mapNotFound(err, "cms.relation_target_not_found", "relation target collection not found")
		}
		relationID = &target.ID
	}

	field, err := u.repo.UpdateField(ctx, ownerID, existing.ID, UpdateFieldInput{
		Label:                in.Label,
		Required:             in.Required,
		Default:              in.Default,
		Validators:           in.Validators,
		RelationCollectionID: relationID,
		ClearRelation:        in.ClearRelation,
		Cascade:              in.Cascade,
		Index:                in.Index,
		Order:                in.Order,
	})
	if err != nil {
		return nil, mapNotFound(err, "cms.field_not_found", "field not found")
	}
	_ = u.repo.BumpSchemaVersion(ctx, coll.ID)
	return field, nil
}

func (u *usecase) RemoveField(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, fieldName string) error {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	existing, err := u.repo.GetFieldByName(ctx, ownerID, coll.ID, fieldName)
	if err != nil {
		return mapNotFound(err, "cms.field_not_found", "field not found")
	}
	if err := u.repo.DeleteField(ctx, ownerID, existing.ID); err != nil {
		return mapNotFound(err, "cms.field_not_found", "field not found")
	}
	_ = u.repo.BumpSchemaVersion(ctx, coll.ID)
	return nil
}

// ── Document ─────────────────────────────────────────────────────────────

func (u *usecase) InsertDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, doc map[string]any) (map[string]any, error) {
	coll, fields, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, err
	}
	clean, verrs := validateDocument(fields, doc, false)
	if verrs != nil {
		return nil, &DocValidationError{Fields: verrs}
	}
	if err := u.checkRelations(ctx, ownerID, dbName, tenant, fields, clean); err != nil {
		return nil, err
	}
	id, err := u.docs.InsertOne(ctx, dbName, coll.Slug, tenant, clean)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	clean["_id"] = id
	return clean, nil
}

func (u *usecase) FindDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, q DocumentQuery) (DocumentList, error) {
	coll, _, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return DocumentList{}, err
	}
	list, err := u.docs.Find(ctx, dbName, coll.Slug, tenant, q)
	if err != nil {
		return DocumentList{}, httpx.Internal(err)
	}
	return list, nil
}

func (u *usecase) FindOneDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string) (map[string]any, error) {
	coll, _, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, err
	}
	doc, err := u.docs.FindOne(ctx, dbName, coll.Slug, tenant, docID, false)
	if errors.Is(err, database.ErrNotFound) {
		return nil, httpx.NotFound("cms.document_not_found", "document not found")
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return doc, nil
}

func (u *usecase) CountDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, filter map[string]any) (int64, error) {
	coll, _, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return 0, err
	}
	n, err := u.docs.Count(ctx, dbName, coll.Slug, tenant, filter, false)
	if err != nil {
		return 0, httpx.Internal(err)
	}
	return n, nil
}

func (u *usecase) UpdateDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string, patch map[string]any) (map[string]any, error) {
	coll, fields, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, err
	}
	clean, verrs := validateDocument(fields, patch, true)
	if verrs != nil {
		return nil, &DocValidationError{Fields: verrs}
	}
	if err := u.checkRelations(ctx, ownerID, dbName, tenant, fields, clean); err != nil {
		return nil, err
	}
	doc, err := u.docs.UpdateOne(ctx, dbName, coll.Slug, tenant, docID, clean)
	if errors.Is(err, database.ErrNotFound) {
		return nil, httpx.NotFound("cms.document_not_found", "document not found")
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return doc, nil
}

func (u *usecase) DeleteDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string) error {
	coll, _, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return err
	}
	ok, err := u.docs.SoftDeleteOne(ctx, dbName, coll.Slug, tenant, docID)
	if err != nil {
		return httpx.Internal(err)
	}
	if !ok {
		return httpx.NotFound("cms.document_not_found", "document not found")
	}
	return nil
}

func (u *usecase) RestoreDocument(ctx context.Context, ownerID, projectID uuid.UUID, collSlug, docID string) error {
	coll, _, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return err
	}
	ok, err := u.docs.RestoreOne(ctx, dbName, coll.Slug, tenant, docID)
	if err != nil {
		return httpx.Internal(err)
	}
	if !ok {
		return httpx.NotFound("cms.document_not_found", "document not found or not deleted")
	}
	return nil
}

func (u *usecase) InsertDocuments(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, docs []map[string]any) ([]map[string]any, error) {
	coll, fields, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, err
	}
	clean := make([]map[string]any, len(docs))
	for i, doc := range docs {
		c, verrs := validateDocument(fields, doc, false)
		if verrs != nil {
			return nil, &DocValidationError{Fields: verrs}
		}
		if err := u.checkRelations(ctx, ownerID, dbName, tenant, fields, c); err != nil {
			return nil, err
		}
		clean[i] = c
	}
	ids, err := u.docs.InsertMany(ctx, dbName, coll.Slug, tenant, clean)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	for i, id := range ids {
		clean[i]["_id"] = id
	}
	return clean, nil
}

func (u *usecase) AggregateDocuments(
	ctx context.Context, ownerID, projectID uuid.UUID, collSlug string, pipeline []map[string]any, limit int64,
) ([]map[string]any, error) {
	coll, _, dbName, tenant, err := u.loadContext(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, err
	}
	out, err := u.docs.Aggregate(ctx, dbName, coll.Slug, tenant, pipeline, limit)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return out, nil
}

// ── Export task ──────────────────────────────────────────────────────────

func (u *usecase) CreateExportTask(ctx context.Context, ownerID, projectID uuid.UUID, collSlug string) (*ExportTask, error) {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, collSlug)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	task, err := u.repo.CreateExportTask(ctx, ownerID, coll.ID)
	if err != nil {
		return nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	return task, nil
}

func (u *usecase) GetExportTask(ctx context.Context, ownerID, taskID uuid.UUID) (*ExportTask, error) {
	task, err := u.repo.GetExportTask(ctx, ownerID, taskID)
	if err != nil {
		return nil, mapNotFound(err, "cms.export_task_not_found", "export task not found")
	}
	return task, nil
}

// ── shared helpers ───────────────────────────────────────────────────────

// loadContext resolves a collection + its field schema + its project's Mongo
// tenant coordinates (shared db name + `_project_id` scoping value) in one
// place — every document operation needs all four.
func (u *usecase) loadContext(
	ctx context.Context, ownerID, projectID uuid.UUID, slug string,
) (*Collection, []Field, string, any, error) {
	coll, err := u.repo.GetCollectionBySlug(ctx, ownerID, projectID, slug)
	if err != nil {
		return nil, nil, "", nil, mapNotFound(err, "cms.collection_not_found", "collection not found")
	}
	fields, err := u.repo.ListFields(ctx, coll.ID)
	if err != nil {
		return nil, nil, "", nil, httpx.Internal(err)
	}
	dbName, tenant, err := u.resolveMongoTenant(ctx, ownerID, projectID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	return coll, fields, dbName, tenant, nil
}

// resolveMongoTenant resolves the shared Mongo database name (see
// mongo.go/domain.go) and the `_project_id` scoping value for a project:
//   - if the project was imported from Django (projects.legacy_id set —
//     backfilled by scripts/backfill_legacy_project_id from the ETL's
//     uuid5(NS,"project:<pk>") mapping), the value is that legacy int, since
//     that's what the real production documents already carry;
//   - otherwise (a project created after the Go cutover, never existed in
//     Django) the value is the project's own UUID string — self-consistent
//     because it's both stamped and filtered by this same code path, even
//     though it differs in *type* from legacy int-tagged documents.
func (u *usecase) resolveMongoTenant(ctx context.Context, ownerID, projectID uuid.UUID) (string, any, error) {
	legacyID, err := u.repo.ProjectMongoTenant(ctx, ownerID, projectID)
	if err != nil {
		return "", nil, mapNotFound(err, "project.not_found", "project not found")
	}
	dbName := u.sharedDBName
	if dbName == "" {
		dbName = "botspace_cms"
	}
	if legacyID != nil {
		return dbName, int64(*legacyID), nil
	}
	return dbName, projectID.String(), nil
}

// checkRelations verifies every RELATION field's value(s) point at an
// existing document in the referenced collection. Dangling relation
// *configuration* (the target collection itself was deleted) does not block
// writes — only a missing *document* does.
func (u *usecase) checkRelations(
	ctx context.Context, ownerID uuid.UUID, dbName string, tenant any, fields []Field, clean map[string]any,
) error {
	verrs := ValidationErrors{}
	for _, f := range fields {
		if f.Type != FieldRelation || f.RelationCollectionID == nil {
			continue
		}
		v, ok := clean[f.Name]
		if !ok {
			continue
		}
		target, err := u.repo.GetCollectionByID(ctx, ownerID, *f.RelationCollectionID)
		if err != nil {
			continue
		}
		for _, id := range toStringSlice(v) {
			if id == "" {
				continue
			}
			if _, err := u.docs.FindOne(ctx, dbName, target.Slug, tenant, id, false); errors.Is(err, database.ErrNotFound) {
				verrs[f.Name] = fmt.Sprintf("related document %q not found in %q", id, target.Slug)
			}
		}
	}
	if len(verrs) > 0 {
		return &DocValidationError{Fields: verrs}
	}
	return nil
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func mapNotFound(err error, code, msg string) error {
	if errors.Is(err, database.ErrNotFound) {
		return httpx.NotFound(code, msg)
	}
	return httpx.Internal(err)
}
