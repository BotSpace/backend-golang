// Package mongox — legacy CMS Mongo (har loyiha o'z db'si: project_<id>).
package mongox

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Client struct {
	c *mongo.Client
}

func New(ctx context.Context, url string) (*Client, error) {
	c, err := mongo.Connect(ctx, options.Client().ApplyURI(url).SetMaxPoolSize(20))
	if err != nil {
		return nil, err
	}
	if err := c.Ping(ctx, nil); err != nil {
		return nil, err
	}
	return &Client{c: c}, nil
}

// ProjectUsers — project_<id> db'sidagi "users" kolleksiyasidan barcha hujjatlar.
func (m *Client) ProjectUsers(ctx context.Context, projectID int64) ([]bson.M, error) {
	coll := m.c.Database(fmt.Sprintf("project_%d", projectID)).Collection("users")
	cur, err := coll.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ProjectDataSize — project_<id> db'sidagi barcha kolleksiyalar dataSize (bytes).
// Backend _cms_bytes taxminiga mos: dbStats.dataSize ishlatamiz (aniqroq va arzon).
func (m *Client) ProjectDataSize(ctx context.Context, projectID int64) (int64, error) {
	db := m.c.Database(fmt.Sprintf("project_%d", projectID))
	var stats struct {
		DataSize int64 `bson:"dataSize"`
	}
	if err := db.RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Decode(&stats); err != nil {
		return 0, err
	}
	return stats.DataSize, nil
}

// TenantDataSize returns an approximate per-project CMS size in the shared
// backend-golang CMS database by summing BSON sizes of documents scoped by
// _project_id across all user collections.
func (m *Client) TenantDataSize(ctx context.Context, dbName string, tenant any) (int64, error) {
	db := m.c.Database(dbName)
	names, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return 0, err
	}

	var total int64
	for _, name := range names {
		if len(name) >= 7 && name[:7] == "system." {
			continue
		}
		coll := db.Collection(name)
		cur, err := coll.Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: bson.D{{Key: "_project_id", Value: tenant}}}},
			{{Key: "$project", Value: bson.D{{Key: "size", Value: bson.D{{Key: "$bsonSize", Value: "$$ROOT"}}}}}},
			{{Key: "$group", Value: bson.D{{Key: "_id", Value: nil}, {Key: "total", Value: bson.D{{Key: "$sum", Value: "$size"}}}}}},
		})
		if err != nil {
			return 0, err
		}

		var rows []struct {
			Total int64 `bson:"total"`
		}
		if err := cur.All(ctx, &rows); err != nil {
			_ = cur.Close(ctx)
			return 0, err
		}
		_ = cur.Close(ctx)
		if len(rows) > 0 {
			total += rows[0].Total
		}
	}
	return total, nil
}
