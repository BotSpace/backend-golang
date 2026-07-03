package chat

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// fieldProjectID is the tenant field every document in the shared Mongo
// database carries — engine-golang's internal/repository/collection.go
// scopeDoc() stamps it as an int (Django legacy project pk) on every
// "users"/"chat" write; internal/modules/cms/mongo.go uses the identical
// convention on the backend side. Kept as its own constant (not imported
// from cms) per PORTING_GUIDE.md rule #4.
const fieldProjectID = "_project_id"

// mongoDB wraps the shared driver client for read-only access to the
// "users"/"chat" collections engine-golang writes directly (not through this
// backend's cms module, so there is no schema/field registry to go through —
// these are fixed, engine-defined shapes).
type MongoDB struct {
	client *mongo.Client
	dbName string
}

// NewMongoDB dials MongoDB using APP_MONGO_URL (default
// "mongodb://localhost:27017" — same env var cms.NewMongoClient reads, they
// share one cluster). dbName is the caller-resolved shared CMS database name
// (server.go passes cfg.Engine.MongoDBName, the exact value cms.NewUsecase
// is wired with, defaulting to "botspace_cms" — see usecase.go's fallback).
func NewMongoDB(ctx context.Context, dbName string) (*MongoDB, error) {
	uri := os.Getenv("APP_MONGO_URL")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	if dbName == "" {
		dbName = "botspace_cms"
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(dialCtx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect mongo: %w", err)
	}
	if err := client.Ping(dialCtx, nil); err != nil {
		return nil, fmt.Errorf("ping mongo: %w", err)
	}
	return &MongoDB{client: client, dbName: dbName}, nil
}

func (m *MongoDB) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }

func (m *MongoDB) coll(name string) *mongo.Collection {
	return m.client.Database(m.dbName).Collection(name)
}

// findAll returns every document in coll for tenant, sorted newest-first —
// used for the "users" collection, which Django's get_users_with_messages()
// fetches unbounded (limit=0) before merging with chat stats and paginating
// server-side. maxRows is a practical cap (Django has none, but an unbounded
// Mongo scan on a shared multi-tenant collection is not safe to expose over
// HTTP) — see usecase.go.
func (m *MongoDB) findAll(ctx context.Context, coll string, tenant any, maxRows int64) ([]map[string]any, int64, error) {
	filter := bson.M{fieldProjectID: tenant}
	total, err := m.coll(coll).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("mongo count %s: %w", coll, err)
	}
	cur, err := m.coll(coll).Find(ctx, filter, options.Find().SetLimit(maxRows).SetSort(bson.D{{Key: "_id", Value: -1}}))
	if err != nil {
		return nil, 0, fmt.Errorf("mongo find %s: %w", coll, err)
	}
	defer cur.Close(ctx)
	items, err := decodeAll(ctx, cur)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// findByChatID pages through coll (the "chat" collection) filtered to one
// chat_id, newest-first, mirroring Django's
// find(where={"chat_id": int(chat_id)}, limit=limit, skip=skip, sort=[("_id",-1)]).
func (m *MongoDB) findByChatID(ctx context.Context, coll string, tenant, chatID any, limit, skip int64) ([]map[string]any, int64, error) {
	filter := bson.M{fieldProjectID: tenant, "chat_id": chatID}
	total, err := m.coll(coll).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("mongo count %s: %w", coll, err)
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	opts := options.Find().SetLimit(limit).SetSkip(skip).SetSort(bson.D{{Key: "_id", Value: -1}})
	cur, err := m.coll(coll).Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("mongo find %s: %w", coll, err)
	}
	defer cur.Close(ctx)
	items, err := decodeAll(ctx, cur)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// chatStatsByChatID runs the same aggregation Django's
// get_users_with_messages() does on the "chat" collection: group by chat_id,
// keep the newest message + a total/unread count per group.
func (m *MongoDB) chatStatsByChatID(ctx context.Context, coll string, tenant any) (map[any]chatStats, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{fieldProjectID: tenant}}},
		{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}},
		{{Key: "$group", Value: bson.M{
			"_id":            "$chat_id",
			"last_message":   bson.M{"$first": "$$ROOT"},
			"total_messages": bson.M{"$sum": 1},
			"unread_count": bson.M{"$sum": bson.M{"$cond": bson.M{
				"if":   bson.M{"$eq": bson.A{bson.M{"$ifNull": bson.A{"$is_read", false}}, false}},
				"then": 1,
				"else": 0,
			}}},
		}}},
	}
	cur, err := m.coll(coll).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("mongo aggregate %s: %w", coll, err)
	}
	defer cur.Close(ctx)

	out := make(map[any]chatStats)
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return nil, fmt.Errorf("mongo decode: %w", err)
		}
		key := raw["_id"]
		stats := chatStats{
			TotalMessages: toInt64(raw["total_messages"]),
			UnreadCount:   toInt64(raw["unread_count"]),
		}
		if lm, ok := raw["last_message"].(bson.M); ok {
			stats.LastMessage = serialize(lm)
		}
		out[normalizeValue(key)] = stats
	}
	return out, cur.Err()
}

type chatStats struct {
	LastMessage   map[string]any
	TotalMessages int64
	UnreadCount   int64
}

func decodeAll(ctx context.Context, cur *mongo.Cursor) ([]map[string]any, error) {
	items := make([]map[string]any, 0)
	for cur.Next(ctx) {
		var raw bson.M
		if err := cur.Decode(&raw); err != nil {
			return nil, fmt.Errorf("mongo decode: %w", err)
		}
		items = append(items, serialize(raw))
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("mongo cursor: %w", err)
	}
	return items, nil
}

// serialize renders a raw Mongo document into JSON-friendly Go values and
// drops the internal tenant field — mirrors cms/mongo.go's serialize().
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

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}
