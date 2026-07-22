package cms

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/botspace/backend/internal/shared/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Internal bookkeeping fields stamped on every document, mirroring Django's
// services/mongo.py (`_created_at`/`_updated_at`/`_version`/`_deleted_at`).
// All projects' documents live in ONE shared Mongo database (matches
// Django's CMS_SHARED_DB / botspace_cms — see usecase.go resolveMongoTenant),
// so `_project_id` is the tenant boundary: every read/write is scoped by it,
// mirroring Django's services/mongo.py `_scoped()`/`_tenant()`.
const (
	fieldCreatedAt = "_created_at"
	fieldUpdatedAt = "_updated_at"
	fieldVersion   = "_version"
	fieldDeletedAt = "_deleted_at"
	fieldProjectID = "_project_id"
)

// MongoClient wraps the shared driver client. It owns only the connection —
// every call takes an explicit dbName/collName, resolved by the usecase from
// Repository.ProjectMongoDatabase + Collection.Slug.
type MongoClient struct {
	client *mongo.Client
}

// NewMongoClient creates a reconnecting MongoDB client using APP_MONGO_URL.
// mongo.Connect is intentionally lazy: routes must remain registered when
// Mongo starts after this process (for example after a Kubernetes eviction).
// The driver performs server selection on each operation and automatically
// recovers when Mongo becomes reachable.
func NewMongoClient(ctx context.Context) (*MongoClient, error) {
	uri := os.Getenv("APP_MONGO_URL")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect mongo: %w", err)
	}
	return &MongoClient{client: client}, nil
}

// Close disconnects the underlying client. Call during graceful shutdown.
func (m *MongoClient) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}

func (m *MongoClient) coll(dbName, collName string) *mongo.Collection {
	return m.client.Database(dbName).Collection(collName)
}

// mongoDocRepo adapts MongoClient to the DocumentRepository port.
type mongoDocRepo struct {
	client *MongoClient
}

// NewMongoDocumentRepository builds the Mongo-backed DocumentRepository.
func NewMongoDocumentRepository(client *MongoClient) DocumentRepository {
	return &mongoDocRepo{client: client}
}

func (r *mongoDocRepo) InsertOne(ctx context.Context, dbName, collName string, tenant any, doc map[string]any) (string, error) {
	res, err := r.client.coll(dbName, collName).InsertOne(ctx, stampCreate(doc, tenant))
	if err != nil {
		return "", fmt.Errorf("mongo insert one: %w", err)
	}
	return idToString(res.InsertedID), nil
}

func (r *mongoDocRepo) InsertMany(ctx context.Context, dbName, collName string, tenant any, docs []map[string]any) ([]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	payload := make([]interface{}, len(docs))
	for i, d := range docs {
		payload[i] = stampCreate(d, tenant)
	}
	res, err := r.client.coll(dbName, collName).InsertMany(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("mongo insert many: %w", err)
	}
	ids := make([]string, len(res.InsertedIDs))
	for i, v := range res.InsertedIDs {
		ids[i] = idToString(v)
	}
	return ids, nil
}

func (r *mongoDocRepo) Find(ctx context.Context, dbName, collName string, tenant any, q DocumentQuery) (DocumentList, error) {
	filter := aliveFilter(tenantFilter(tenant, q.Filter), q.IncludeDeleted)

	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	findOpts := options.Find().SetLimit(limit).SetSkip(q.Skip)
	if len(q.Sort) > 0 {
		sort := bson.D{}
		for _, s := range q.Sort {
			dir := -1
			if s.Asc {
				dir = 1
			}
			sort = append(sort, bson.E{Key: s.Field, Value: dir})
		}
		findOpts.SetSort(sort)
	}

	cur, err := r.client.coll(dbName, collName).Find(ctx, filter, findOpts)
	if err != nil {
		return DocumentList{}, fmt.Errorf("mongo find: %w", err)
	}
	defer cur.Close(ctx)

	items := make([]map[string]any, 0)
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return DocumentList{}, fmt.Errorf("mongo decode: %w", err)
		}
		items = append(items, serialize(raw))
	}
	if err := cur.Err(); err != nil {
		return DocumentList{}, fmt.Errorf("mongo cursor: %w", err)
	}

	total, err := r.client.coll(dbName, collName).CountDocuments(ctx, filter)
	if err != nil {
		return DocumentList{}, fmt.Errorf("mongo count: %w", err)
	}
	return DocumentList{Items: items, Total: total}, nil
}

func (r *mongoDocRepo) FindOne(ctx context.Context, dbName, collName string, tenant any, id string, includeDeleted bool) (map[string]any, error) {
	oid, err := coerceID(id)
	if err != nil {
		return nil, fmt.Errorf("invalid document id: %w", err)
	}
	filter := aliveFilter(tenantFilter(tenant, map[string]any{"_id": oid}), includeDeleted)

	var raw bson.M
	err = r.client.coll(dbName, collName).FindOne(ctx, filter).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mongo find one: %w", err)
	}
	return serialize(raw), nil
}

func (r *mongoDocRepo) Count(ctx context.Context, dbName, collName string, tenant any, filter map[string]any, includeDeleted bool) (int64, error) {
	n, err := r.client.coll(dbName, collName).CountDocuments(ctx, aliveFilter(tenantFilter(tenant, filter), includeDeleted))
	if err != nil {
		return 0, fmt.Errorf("mongo count: %w", err)
	}
	return n, nil
}

func (r *mongoDocRepo) UpdateOne(ctx context.Context, dbName, collName string, tenant any, id string, patch map[string]any) (map[string]any, error) {
	oid, err := coerceID(id)
	if err != nil {
		return nil, fmt.Errorf("invalid document id: %w", err)
	}
	filter := aliveFilter(tenantFilter(tenant, map[string]any{"_id": oid}), false)

	set := bson.M{}
	for k, v := range patch {
		set[k] = v
	}
	set[fieldUpdatedAt] = time.Now().UTC()
	update := bson.M{"$set": set, "$inc": bson.M{fieldVersion: 1}}

	res, err := r.client.coll(dbName, collName).UpdateOne(ctx, filter, update)
	if err != nil {
		return nil, fmt.Errorf("mongo update one: %w", err)
	}
	if res.MatchedCount == 0 {
		return nil, database.ErrNotFound
	}
	return r.FindOne(ctx, dbName, collName, tenant, id, false)
}

func (r *mongoDocRepo) SoftDeleteOne(ctx context.Context, dbName, collName string, tenant any, id string) (bool, error) {
	oid, err := coerceID(id)
	if err != nil {
		return false, fmt.Errorf("invalid document id: %w", err)
	}
	filter := aliveFilter(tenantFilter(tenant, map[string]any{"_id": oid}), false)
	res, err := r.client.coll(dbName, collName).UpdateOne(
		ctx, filter, bson.M{"$set": bson.M{fieldDeletedAt: time.Now().UTC()}},
	)
	if err != nil {
		return false, fmt.Errorf("mongo soft delete: %w", err)
	}
	return res.ModifiedCount > 0, nil
}

// RestoreOne un-deletes a soft-deleted document (mirrors Django's restore()).
func (r *mongoDocRepo) RestoreOne(ctx context.Context, dbName, collName string, tenant any, id string) (bool, error) {
	oid, err := coerceID(id)
	if err != nil {
		return false, fmt.Errorf("invalid document id: %w", err)
	}
	filter := tenantFilter(tenant, bson.M{"_id": oid, fieldDeletedAt: bson.M{"$ne": nil}})
	res, err := r.client.coll(dbName, collName).UpdateOne(
		ctx, filter, bson.M{"$set": bson.M{fieldDeletedAt: nil}},
	)
	if err != nil {
		return false, fmt.Errorf("mongo restore: %w", err)
	}
	return res.ModifiedCount > 0, nil
}

func (r *mongoDocRepo) Aggregate(ctx context.Context, dbName, collName string, tenant any, pipeline []map[string]any, limit int64) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	// Tenant/soft-delete guard is prepended so a caller-supplied pipeline can
	// never see another tenant's or soft-deleted documents (mirrors Django's
	// mandatory $match in services/mongo.py aggregate()).
	stages := make([]bson.M, 0, len(pipeline)+1)
	stages = append(stages, bson.M{"$match": bson.M{fieldProjectID: tenant, fieldDeletedAt: nil}})
	for _, s := range pipeline {
		m := bson.M{}
		for k, v := range s {
			m[k] = v
		}
		stages = append(stages, m)
	}

	cur, err := r.client.coll(dbName, collName).Aggregate(ctx, stages)
	if err != nil {
		return nil, fmt.Errorf("mongo aggregate: %w", err)
	}
	defer cur.Close(ctx)

	out := make([]map[string]any, 0)
	for cur.Next(ctx) && int64(len(out)) < limit {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return nil, fmt.Errorf("mongo decode: %w", err)
		}
		out = append(out, serialize(raw))
	}
	return out, cur.Err()
}

// DropCollection removes only this tenant's documents from the shared Mongo
// collection — mirrors Django's drop_collection(), which deletes by
// `_project_id` rather than dropping the collection outright (it's shared by
// every other project too).
func (r *mongoDocRepo) DropCollection(ctx context.Context, dbName, collName string, tenant any) error {
	if _, err := r.client.coll(dbName, collName).DeleteMany(ctx, bson.M{fieldProjectID: tenant}); err != nil {
		return fmt.Errorf("mongo drop collection (tenant delete): %w", err)
	}
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────

// stampCreate mirrors Django's _stamp_create(): bookkeeping fields plus the
// tenant field, so every inserted document is scoped to its project even
// though it shares a Mongo collection with every other project.
func stampCreate(doc map[string]any, tenant any) map[string]any {
	now := time.Now().UTC()
	out := make(map[string]any, len(doc)+5)
	for k, v := range doc {
		out[k] = v
	}
	if _, ok := out[fieldCreatedAt]; !ok {
		out[fieldCreatedAt] = now
	}
	if _, ok := out[fieldUpdatedAt]; !ok {
		out[fieldUpdatedAt] = now
	}
	if _, ok := out[fieldVersion]; !ok {
		out[fieldVersion] = 1
	}
	out[fieldDeletedAt] = nil
	out[fieldProjectID] = tenant
	return out
}

func coerceID(id string) (primitive.ObjectID, error) {
	return primitive.ObjectIDFromHex(id)
}

// tenantFilter copies filter and adds the mandatory `_project_id` guard —
// the single chokepoint (mirrors Django's `_tenant()`/`_scoped()`) that keeps
// one project from ever seeing another's documents in the shared collection.
func tenantFilter(tenant any, filter map[string]any) bson.M {
	f := bson.M{fieldProjectID: tenant}
	for k, v := range filter {
		f[k] = v
	}
	return f
}

// aliveFilter copies filter and adds the soft-delete guard unless the caller
// explicitly opted into seeing deleted rows.
func aliveFilter(filter bson.M, includeDeleted bool) bson.M {
	f := bson.M{}
	for k, v := range filter {
		f[k] = v
	}
	if !includeDeleted {
		f[fieldDeletedAt] = nil
	}
	return f
}

// serialize renders a raw Mongo document into JSON-friendly Go values
// (ObjectID -> hex string, driver DateTime -> time.Time). The internal
// tenant field is stripped — callers never see `_project_id` (mirrors
// Django's _serialize(), which drops TENANT_FIELD from the API response).
func serialize(doc bson.M) map[string]any {
	if doc == nil {
		return nil
	}
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		if k == fieldProjectID {
			continue
		}
		out[k] = normalizeValue(v)
	}
	return out
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case primitive.ObjectID:
		return t.Hex()
	case primitive.DateTime:
		return t.Time().UTC()
	case bson.M:
		return serialize(t)
	case bson.A:
		arr := make([]any, len(t))
		for i, e := range t {
			arr[i] = normalizeValue(e)
		}
		return arr
	default:
		return v
	}
}

func idToString(v any) string {
	if oid, ok := v.(primitive.ObjectID); ok {
		return oid.Hex()
	}
	return fmt.Sprintf("%v", v)
}
