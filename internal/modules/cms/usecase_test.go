package cms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/botspace/backend/internal/shared/database"
	"github.com/botspace/backend/internal/shared/httpx"
	"github.com/google/uuid"
)

// ── pure validation tests (no repo/Mongo at all) ───────────────────────────

func textField(name string, required bool) Field {
	return Field{Name: name, Type: FieldText, Required: required}
}

func TestValidateDocument_RequiredMissing(t *testing.T) {
	fields := []Field{textField("title", true)}
	_, errs := validateDocument(fields, map[string]any{}, false)
	if errs == nil || errs["title"] != "required" {
		t.Fatalf("expected required error for title, got %v", errs)
	}
}

func TestValidateDocument_PartialSkipsRequired(t *testing.T) {
	fields := []Field{textField("title", true)}
	out, errs := validateDocument(fields, map[string]any{}, true)
	if errs != nil {
		t.Fatalf("partial update must not enforce required, got %v", errs)
	}
	if len(out) != 0 {
		t.Fatalf("expected no output fields, got %v", out)
	}
}

func TestValidateDocument_TypeCoercion(t *testing.T) {
	fields := []Field{
		{Name: "age", Type: FieldInt, Required: true},
		{Name: "active", Type: FieldBool, Required: true},
		{Name: "email", Type: FieldEmail, Required: true},
	}
	out, errs := validateDocument(fields, map[string]any{
		"age": "42", "active": "yes", "email": "USER@Example.com",
	}, false)
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if out["age"] != int64(42) {
		t.Errorf("age not coerced to int64: %#v", out["age"])
	}
	if out["active"] != true {
		t.Errorf("active not coerced to bool: %#v", out["active"])
	}
	if out["email"] != "user@example.com" {
		t.Errorf("email not normalized: %#v", out["email"])
	}
}

func TestValidateDocument_InvalidValueReportsFieldError(t *testing.T) {
	fields := []Field{{Name: "age", Type: FieldInt, Required: true}}
	_, errs := validateDocument(fields, map[string]any{"age": "not-a-number"}, false)
	if errs == nil || errs["age"] == "" {
		t.Fatalf("expected a coercion error for age, got %v", errs)
	}
}

func TestValidateDocument_UnknownFieldsPassThroughExceptUnderscored(t *testing.T) {
	fields := []Field{textField("title", false)}
	out, errs := validateDocument(fields, map[string]any{
		"title": "hi", "telegram_id": float64(123), "_internal": "nope",
	}, false)
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if out["telegram_id"] != float64(123) {
		t.Errorf("expected pass-through field to survive: %#v", out)
	}
	if _, ok := out["_internal"]; ok {
		t.Errorf("underscore-prefixed keys must not pass through: %#v", out)
	}
}

func TestCoerceField_EnumChoices(t *testing.T) {
	rules := map[string]any{"choices": []any{"a", "b"}}
	if _, err := coerceField(FieldEnum, "a", rules); err != nil {
		t.Fatalf("expected valid choice to pass: %v", err)
	}
	if _, err := coerceField(FieldEnum, "z", rules); err == nil {
		t.Fatal("expected invalid choice to fail")
	}
}

func TestCoerceField_GeoRange(t *testing.T) {
	if _, err := coerceField(FieldGeo, "10,20", nil); err != nil {
		t.Fatalf("expected valid geo to pass: %v", err)
	}
	if _, err := coerceField(FieldGeo, "999,20", nil); err == nil {
		t.Fatal("expected out-of-range geo to fail")
	}
}

func TestCoerceField_ArrayOfInts(t *testing.T) {
	rules := map[string]any{"array_kind": FieldInt}
	out, err := coerceField(FieldArray, []any{"1", "2", "3"}, rules)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr, ok := out.([]any)
	if !ok || len(arr) != 3 || arr[0] != int64(1) {
		t.Fatalf("expected coerced int array, got %#v", out)
	}
}

func TestValidateDocument_DefaultAppliedWhenMissing(t *testing.T) {
	def, _ := json.Marshal("draft")
	fields := []Field{{Name: "status", Type: FieldText, Required: false, Default: def}}
	out, errs := validateDocument(fields, map[string]any{}, false)
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if out["status"] != "draft" {
		t.Fatalf("expected default value applied, got %#v", out["status"])
	}
}

// ── fake repositories for usecase-level tests (no Postgres/Mongo) ─────────

type fakeRepo struct {
	owners      map[uuid.UUID]uuid.UUID // projectID -> ownerID
	mongoDBs    map[uuid.UUID]string    // projectID -> mongo_database
	legacyIDs   map[uuid.UUID]*int32    // projectID -> Django legacy pk (nil = none)
	collections map[uuid.UUID]*Collection
	fields      map[uuid.UUID]*Field
	exports     map[uuid.UUID]*ExportTask
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		owners:      map[uuid.UUID]uuid.UUID{},
		mongoDBs:    map[uuid.UUID]string{},
		legacyIDs:   map[uuid.UUID]*int32{},
		collections: map[uuid.UUID]*Collection{},
		fields:      map[uuid.UUID]*Field{},
		exports:     map[uuid.UUID]*ExportTask{},
	}
}

func (r *fakeRepo) addProject(ownerID uuid.UUID) uuid.UUID {
	id := uuid.New()
	r.owners[id] = ownerID
	r.mongoDBs[id] = "proj_" + id.String()[:8]
	return id
}

func (r *fakeRepo) CreateCollection(_ context.Context, in CreateCollectionInput) (*Collection, error) {
	if r.owners[in.ProjectID] != in.OwnerID {
		return nil, database.ErrNotFound
	}
	c := &Collection{
		ID: uuid.New(), ProjectID: in.ProjectID, Name: in.Name, Slug: in.Slug,
		Description: in.Description, PrimaryField: in.PrimaryField, SchemaVersion: 1,
	}
	r.collections[c.ID] = c
	return c, nil
}

func (r *fakeRepo) GetCollectionBySlug(_ context.Context, ownerID, projectID uuid.UUID, slug string) (*Collection, error) {
	for _, c := range r.collections {
		if c.ProjectID == projectID && c.Slug == slug && r.owners[projectID] == ownerID {
			return c, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) GetCollectionByID(_ context.Context, ownerID, id uuid.UUID) (*Collection, error) {
	c, ok := r.collections[id]
	if !ok || r.owners[c.ProjectID] != ownerID {
		return nil, database.ErrNotFound
	}
	return c, nil
}

func (r *fakeRepo) ListCollections(_ context.Context, ownerID, projectID uuid.UUID) ([]Collection, error) {
	var out []Collection
	for _, c := range r.collections {
		if c.ProjectID == projectID && r.owners[projectID] == ownerID {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdateCollection(_ context.Context, ownerID, id uuid.UUID, in UpdateCollectionInput) (*Collection, error) {
	c, ok := r.collections[id]
	if !ok || r.owners[c.ProjectID] != ownerID {
		return nil, database.ErrNotFound
	}
	if in.Name != nil {
		c.Name = *in.Name
	}
	if in.Description != nil {
		c.Description = *in.Description
	}
	if in.PrimaryField != nil {
		c.PrimaryField = *in.PrimaryField
	}
	return c, nil
}

func (r *fakeRepo) BumpSchemaVersion(_ context.Context, id uuid.UUID) error {
	if c, ok := r.collections[id]; ok {
		c.SchemaVersion++
	}
	return nil
}

func (r *fakeRepo) DeleteCollection(_ context.Context, ownerID, id uuid.UUID) error {
	c, ok := r.collections[id]
	if !ok || r.owners[c.ProjectID] != ownerID || c.System {
		return database.ErrNotFound
	}
	delete(r.collections, id)
	return nil
}

func (r *fakeRepo) ProjectMongoDatabase(_ context.Context, ownerID, projectID uuid.UUID) (string, error) {
	if r.owners[projectID] != ownerID {
		return "", database.ErrNotFound
	}
	return r.mongoDBs[projectID], nil
}

// ProjectMongoTenant: the fake never has a Django legacy id, so callers fall
// back to the project's own UUID string as the tenant key (matches
// usecase.resolveMongoTenant's nil-legacyID branch).
func (r *fakeRepo) ProjectMongoTenant(_ context.Context, ownerID, projectID uuid.UUID) (*int32, error) {
	if r.owners[projectID] != ownerID {
		return nil, database.ErrNotFound
	}
	return r.legacyIDs[projectID], nil
}

func (r *fakeRepo) CreateField(_ context.Context, in CreateFieldInput) (*Field, error) {
	c, ok := r.collections[in.CollectionID]
	if !ok || r.owners[c.ProjectID] != in.OwnerID {
		return nil, database.ErrNotFound
	}
	f := &Field{
		ID: uuid.New(), CollectionID: in.CollectionID, Name: in.Name, Label: in.Label, Type: in.Type,
		Required: in.Required, Default: in.Default, Validators: in.Validators,
		RelationCollectionID: in.RelationCollectionID, Cascade: in.Cascade, Index: in.Index, Order: in.Order,
	}
	r.fields[f.ID] = f
	return f, nil
}

func (r *fakeRepo) GetFieldByName(_ context.Context, ownerID, collectionID uuid.UUID, name string) (*Field, error) {
	c, ok := r.collections[collectionID]
	if !ok || r.owners[c.ProjectID] != ownerID {
		return nil, database.ErrNotFound
	}
	for _, f := range r.fields {
		if f.CollectionID == collectionID && f.Name == name {
			return f, nil
		}
	}
	return nil, database.ErrNotFound
}

func (r *fakeRepo) ListFields(_ context.Context, collectionID uuid.UUID) ([]Field, error) {
	var out []Field
	for _, f := range r.fields {
		if f.CollectionID == collectionID {
			out = append(out, *f)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdateField(_ context.Context, ownerID, id uuid.UUID, in UpdateFieldInput) (*Field, error) {
	f, ok := r.fields[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	c, ok := r.collections[f.CollectionID]
	if !ok || r.owners[c.ProjectID] != ownerID {
		return nil, database.ErrNotFound
	}
	if in.Label != nil {
		f.Label = *in.Label
	}
	if in.Required != nil {
		f.Required = *in.Required
	}
	if in.ClearRelation {
		f.RelationCollectionID = nil
	} else if in.RelationCollectionID != nil {
		f.RelationCollectionID = in.RelationCollectionID
	}
	return f, nil
}

func (r *fakeRepo) DeleteField(_ context.Context, ownerID, id uuid.UUID) error {
	f, ok := r.fields[id]
	if !ok {
		return database.ErrNotFound
	}
	c, ok := r.collections[f.CollectionID]
	if !ok || r.owners[c.ProjectID] != ownerID {
		return database.ErrNotFound
	}
	delete(r.fields, id)
	return nil
}

func (r *fakeRepo) CreateExportTask(_ context.Context, ownerID, collectionID uuid.UUID) (*ExportTask, error) {
	c, ok := r.collections[collectionID]
	if !ok || r.owners[c.ProjectID] != ownerID {
		return nil, database.ErrNotFound
	}
	t := &ExportTask{ID: uuid.New(), CollectionID: collectionID, OwnerID: ownerID, Status: ExportPending}
	r.exports[t.ID] = t
	return t, nil
}

func (r *fakeRepo) GetExportTask(_ context.Context, ownerID, id uuid.UUID) (*ExportTask, error) {
	t, ok := r.exports[id]
	if !ok || t.OwnerID != ownerID {
		return nil, database.ErrNotFound
	}
	return t, nil
}

func (r *fakeRepo) UpdateExportTaskStatus(_ context.Context, id uuid.UUID, status string, progress int32, downloadURL, errMsg *string) (*ExportTask, error) {
	t, ok := r.exports[id]
	if !ok {
		return nil, database.ErrNotFound
	}
	t.Status = status
	t.Progress = progress
	if downloadURL != nil {
		t.DownloadURL = downloadURL
	}
	if errMsg != nil {
		t.ErrorMessage = *errMsg
	}
	return t, nil
}

// fakeDocs is an in-memory DocumentRepository — no real Mongo required.
// Every document is stamped with `_project_id` (tenant) and every read
// filters by it, the same way the real Mongo-backed repo does, so tests can
// verify cross-tenant isolation on the shared "db/coll" bucket.
type fakeDocs struct {
	byColl map[string]map[string]map[string]any // "db/coll" -> id -> doc
	seq    int
}

func newFakeDocs() *fakeDocs { return &fakeDocs{byColl: map[string]map[string]map[string]any{}} }

func (r *fakeDocs) key(dbName, collName string) string { return dbName + "/" + collName }

func (r *fakeDocs) bucket(dbName, collName string) map[string]map[string]any {
	k := r.key(dbName, collName)
	if r.byColl[k] == nil {
		r.byColl[k] = map[string]map[string]any{}
	}
	return r.byColl[k]
}

func tenantEq(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) }

func (r *fakeDocs) InsertOne(_ context.Context, dbName, collName string, tenant any, doc map[string]any) (string, error) {
	r.seq++
	id := uuid.New().String()
	cp := map[string]any{}
	for k, v := range doc {
		cp[k] = v
	}
	cp["_id"] = id
	cp["_project_id"] = tenant
	r.bucket(dbName, collName)[id] = cp
	return id, nil
}

func (r *fakeDocs) InsertMany(ctx context.Context, dbName, collName string, tenant any, docs []map[string]any) ([]string, error) {
	ids := make([]string, len(docs))
	for i, d := range docs {
		id, _ := r.InsertOne(ctx, dbName, collName, tenant, d)
		ids[i] = id
	}
	return ids, nil
}

func (r *fakeDocs) Find(_ context.Context, dbName, collName string, tenant any, q DocumentQuery) (DocumentList, error) {
	b := r.bucket(dbName, collName)
	items := make([]map[string]any, 0, len(b))
	for _, d := range b {
		if !tenantEq(d["_project_id"], tenant) {
			continue
		}
		items = append(items, d)
	}
	return DocumentList{Items: items, Total: int64(len(items))}, nil
}

func (r *fakeDocs) FindOne(_ context.Context, dbName, collName string, tenant any, id string, _ bool) (map[string]any, error) {
	d, ok := r.bucket(dbName, collName)[id]
	if !ok || !tenantEq(d["_project_id"], tenant) {
		return nil, database.ErrNotFound
	}
	return d, nil
}

func (r *fakeDocs) Count(_ context.Context, dbName, collName string, tenant any, _ map[string]any, _ bool) (int64, error) {
	list, _ := r.Find(context.Background(), dbName, collName, tenant, DocumentQuery{})
	return int64(len(list.Items)), nil
}

func (r *fakeDocs) UpdateOne(_ context.Context, dbName, collName string, tenant any, id string, patch map[string]any) (map[string]any, error) {
	d, ok := r.bucket(dbName, collName)[id]
	if !ok || !tenantEq(d["_project_id"], tenant) {
		return nil, database.ErrNotFound
	}
	for k, v := range patch {
		d[k] = v
	}
	return d, nil
}

func (r *fakeDocs) SoftDeleteOne(_ context.Context, dbName, collName string, tenant any, id string) (bool, error) {
	b := r.bucket(dbName, collName)
	d, ok := b[id]
	if !ok || !tenantEq(d["_project_id"], tenant) {
		return false, nil
	}
	delete(b, id)
	return true, nil
}

func (r *fakeDocs) RestoreOne(_ context.Context, _, _ string, _ any, _ string) (bool, error) {
	return true, nil
}

func (r *fakeDocs) Aggregate(_ context.Context, _, _ string, _ any, _ []map[string]any, _ int64) ([]map[string]any, error) {
	return nil, nil
}

func (r *fakeDocs) DropCollection(_ context.Context, dbName, collName string, tenant any) error {
	b := r.bucket(dbName, collName)
	for id, d := range b {
		if tenantEq(d["_project_id"], tenant) {
			delete(b, id)
		}
	}
	return nil
}

// ── usecase-level tests (fake repo + fake docs, still no real DB) ─────────

func status(err error) int {
	if ae, ok := httpx.AsAppError(err); ok {
		return ae.Status
	}
	return 0
}

// isValidationError reports whether err is a *DocValidationError — document
// write methods return this instead of an httpx.AppError so handlers can
// render per-field details (see handler.go's writeUcErr).
func isValidationError(err error) bool {
	var ve *DocValidationError
	return errors.As(err, &ve)
}

func TestUsecase_CreateCollection_RejectsBadSlug(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, newFakeDocs(), "test_shared_db")
	ownerID := uuid.New()
	projectID := repo.addProject(ownerID)

	_, err := uc.CreateCollection(context.Background(), ownerID, projectID, CreateCollectionInput{Name: "Posts", Slug: "Not-A-Slug!"})
	if status(err) != 400 {
		t.Fatalf("expected 400 for invalid slug, got %v", err)
	}
}

func TestUsecase_InsertDocument_ValidatesAgainstSchema(t *testing.T) {
	repo := newFakeRepo()
	docs := newFakeDocs()
	uc := NewUsecase(repo, docs, "test_shared_db")
	ctx := context.Background()
	ownerID := uuid.New()
	projectID := repo.addProject(ownerID)

	coll, err := uc.CreateCollection(ctx, ownerID, projectID, CreateCollectionInput{Name: "Posts", Slug: "posts"})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if _, err := uc.AddField(ctx, ownerID, projectID, coll.Slug, FieldCreate{Name: "title", Type: FieldText, Required: true}); err != nil {
		t.Fatalf("add field: %v", err)
	}

	if _, err := uc.InsertDocument(ctx, ownerID, projectID, coll.Slug, map[string]any{}); !isValidationError(err) {
		t.Fatalf("expected a validation error for missing required field, got %v", err)
	}

	doc, err := uc.InsertDocument(ctx, ownerID, projectID, coll.Slug, map[string]any{"title": "hello"})
	if err != nil {
		t.Fatalf("insert with valid payload: %v", err)
	}
	if doc["title"] != "hello" || doc["_id"] == nil {
		t.Fatalf("unexpected inserted document: %#v", doc)
	}
}

func TestUsecase_InsertDocument_RelationMustExist(t *testing.T) {
	repo := newFakeRepo()
	docs := newFakeDocs()
	uc := NewUsecase(repo, docs, "test_shared_db")
	ctx := context.Background()
	ownerID := uuid.New()
	projectID := repo.addProject(ownerID)

	authors, err := uc.CreateCollection(ctx, ownerID, projectID, CreateCollectionInput{Name: "Authors", Slug: "authors"})
	if err != nil {
		t.Fatalf("create authors: %v", err)
	}
	posts, err := uc.CreateCollection(ctx, ownerID, projectID, CreateCollectionInput{Name: "Posts", Slug: "posts"})
	if err != nil {
		t.Fatalf("create posts: %v", err)
	}
	if _, err := uc.AddField(ctx, ownerID, projectID, posts.Slug, FieldCreate{
		Name: "author", Type: FieldRelation, Required: true, RelationCollectionSlug: authors.Slug,
	}); err != nil {
		t.Fatalf("add relation field: %v", err)
	}

	// Points at a nonexistent author -> rejected.
	if _, err := uc.InsertDocument(ctx, ownerID, projectID, posts.Slug, map[string]any{"author": "000000000000000000000000"}); !isValidationError(err) {
		t.Fatalf("expected a validation error for dangling relation, got %v", err)
	}

	// Insert a real author, then reference it -> accepted.
	author, err := uc.InsertDocument(ctx, ownerID, projectID, authors.Slug, map[string]any{})
	if err != nil {
		t.Fatalf("insert author: %v", err)
	}
	authorID, _ := author["_id"].(string)
	if _, err := uc.InsertDocument(ctx, ownerID, projectID, posts.Slug, map[string]any{"author": authorID}); err != nil {
		t.Fatalf("expected valid relation to be accepted: %v", err)
	}
}

// TestUsecase_DocumentsAreScopedByProjectTenant is the item-1 regression
// test: with a SHARED Mongo db+collection (two projects writing to the same
// "db/coll" bucket, as botspace_cms now works — see mongo.go/usecase.go
// resolveMongoTenant), one project must never see, count, update, or delete
// another project's documents, and a project with a legacy_id (imported from
// Django) must be reachable by that exact int tenant key.
func TestUsecase_DocumentsAreScopedByProjectTenant(t *testing.T) {
	repo := newFakeRepo()
	docs := newFakeDocs()
	uc := NewUsecase(repo, docs, "botspace_cms")
	ctx := context.Background()

	ownerA, ownerB := uuid.New(), uuid.New()
	projA := repo.addProject(ownerA)
	projB := repo.addProject(ownerB)
	// Force both projects onto the SAME shared Mongo database name (as the
	// real shared-DB deployment does) so the only isolation boundary left is
	// the `_project_id` tenant filter under test.
	repo.mongoDBs[projA] = "botspace_cms"
	repo.mongoDBs[projB] = "botspace_cms"

	collA, err := uc.CreateCollection(ctx, ownerA, projA, CreateCollectionInput{Name: "Orders", Slug: "orders"})
	if err != nil {
		t.Fatalf("create collection A: %v", err)
	}
	if _, err := uc.CreateCollection(ctx, ownerB, projB, CreateCollectionInput{Name: "Orders", Slug: "orders"}); err != nil {
		t.Fatalf("create collection B: %v", err)
	}

	docA, err := uc.InsertDocument(ctx, ownerA, projA, collA.Slug, map[string]any{"title": "A's order"})
	if err != nil {
		t.Fatalf("insert doc A: %v", err)
	}
	if _, err := uc.InsertDocument(ctx, ownerB, projB, "orders", map[string]any{"title": "B's order"}); err != nil {
		t.Fatalf("insert doc B: %v", err)
	}

	// A's list must only contain A's document.
	listA, err := uc.FindDocuments(ctx, ownerA, projA, collA.Slug, DocumentQuery{})
	if err != nil {
		t.Fatalf("find documents A: %v", err)
	}
	if listA.Total != 1 || listA.Items[0]["title"] != "A's order" {
		t.Fatalf("expected only A's 1 document, got %#v", listA)
	}

	// A must not be able to read B's document by id, even knowing its exact
	// Mongo _id (cross-tenant document-id guessing must 404, not leak data).
	docBID, _ := func() (string, bool) {
		listB, _ := uc.FindDocuments(ctx, ownerB, projB, "orders", DocumentQuery{})
		if len(listB.Items) == 0 {
			return "", false
		}
		id, ok := listB.Items[0]["_id"].(string)
		return id, ok
	}()
	if docBID == "" {
		t.Fatal("expected B to have a document id")
	}
	if _, err := uc.FindOneDocument(ctx, ownerA, projA, collA.Slug, docBID); status(err) != 404 {
		t.Fatalf("expected 404 reading B's doc as A, got %v", err)
	}

	// A's count/update/delete must not touch B's document either.
	if n, err := uc.CountDocuments(ctx, ownerA, projA, collA.Slug, nil); err != nil || n != 1 {
		t.Fatalf("expected count=1 for A, got %d err=%v", n, err)
	}
	if err := uc.DeleteDocument(ctx, ownerA, projA, collA.Slug, docBID); status(err) != 404 {
		t.Fatalf("expected 404 deleting B's doc as A, got %v", err)
	}

	docAID, _ := docA["_id"].(string)
	if _, err := uc.FindOneDocument(ctx, ownerB, projB, "orders", docAID); status(err) != 404 {
		t.Fatalf("expected 404 reading A's doc as B, got %v", err)
	}
}

// TestUsecase_ResolveMongoTenant_PrefersLegacyID mirrors the real
// resolveMongoTenant behaviour directly: a project with a Django legacy_id
// must be stamped/filtered with that int (matching pre-existing imported
// Mongo documents' `_project_id`), not the project's Go UUID.
func TestUsecase_ResolveMongoTenant_PrefersLegacyID(t *testing.T) {
	repo := newFakeRepo()
	docs := newFakeDocs()
	uc := &usecase{repo: repo, docs: docs, sharedDBName: "botspace_cms"}
	ctx := context.Background()

	owner := uuid.New()
	proj := repo.addProject(owner)
	legacy := int32(1408)
	repo.legacyIDs[proj] = &legacy

	dbName, tenant, err := uc.resolveMongoTenant(ctx, owner, proj)
	if err != nil {
		t.Fatalf("resolveMongoTenant: %v", err)
	}
	if dbName != "botspace_cms" {
		t.Fatalf("expected shared db name, got %q", dbName)
	}
	if tenant != int64(1408) {
		t.Fatalf("expected tenant to be the legacy int64 1408, got %#v (%T)", tenant, tenant)
	}
}

func TestUsecase_DeleteCollection_BlocksSystemCollections(t *testing.T) {
	repo := newFakeRepo()
	uc := NewUsecase(repo, newFakeDocs(), "test_shared_db")
	ctx := context.Background()
	ownerID := uuid.New()
	projectID := repo.addProject(ownerID)

	coll, err := uc.CreateCollection(ctx, ownerID, projectID, CreateCollectionInput{Name: "Users", Slug: "users"})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	repo.collections[coll.ID].System = true

	if err := uc.DeleteCollection(ctx, ownerID, projectID, coll.Slug); status(err) != 403 {
		t.Fatalf("expected 403 deleting a system collection, got %v", err)
	}
}
