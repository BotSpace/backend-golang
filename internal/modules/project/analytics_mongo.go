package project

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mongoAnalyticsRepo struct{ collection *mongo.Collection }

func NewMongoAnalyticsRepository(db *mongo.Database) AnalyticsRepository {
	return &mongoAnalyticsRepo{collection: db.Collection("bot_conversations")}
}

func (r *mongoAnalyticsRepo) ReadAnalytics(ctx context.Context, ids []string, now time.Time) (map[string]BotAnalytics, error) {
	out := make(map[string]BotAnalytics)
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cursor, err := r.collection.Aggregate(ctx, analyticsPipeline(ids, now), options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return nil, fmt.Errorf("aggregate bot conversations: %w", err)
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var row struct {
			ID             string     `bson:"_id"`
			UsersTotal     int64      `bson:"users_total"`
			ActiveUsers24h int64      `bson:"active_users_24h"`
			ActiveUsers7d  int64      `bson:"active_users_7d"`
			Messages24h    int64      `bson:"messages_24h"`
			LastActivityAt *time.Time `bson:"last_activity_at"`
		}
		if err := cursor.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode bot analytics: %w", err)
		}
		out[row.ID] = BotAnalytics{row.UsersTotal, row.ActiveUsers24h, row.ActiveUsers7d, row.Messages24h, row.LastActivityAt}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("read bot analytics: %w", err)
	}
	return out, nil
}

func analyticsPipeline(ids []string, now time.Time) mongo.Pipeline {
	cond := func(test, yes, no any) bson.M { return bson.M{"$cond": bson.A{test, yes, no}} }
	since24h, since7d := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)
	inboundUser := bson.M{"$and": bson.A{
		bson.M{"$eq": bson.A{"$direction", "inbound"}},
		bson.M{"$isNumber": "$user_id"},
		bson.M{"$gt": bson.A{"$user_id", 0}},
	}}
	validUser := bson.M{"$ne": bson.A{"$_id.user", nil}}
	active := func(since time.Time) bson.M {
		return bson.M{"$and": bson.A{validUser, bson.M{"$gte": bson.A{"$last_activity_at", since}}}}
	}
	// Group by project/user first rather than accumulating unbounded user arrays.
	return mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"project_id": bson.M{"$in": ids}, "direction": bson.M{"$in": bson.A{"inbound", "outbound"}}, "created_at": bson.M{"$type": "date", "$lte": now}}}},
		{{Key: "$group", Value: bson.M{
			"_id":              bson.M{"project": "$project_id", "user": cond(inboundUser, "$user_id", nil)},
			"messages_24h":     bson.M{"$sum": cond(bson.M{"$gte": bson.A{"$created_at", since24h}}, 1, 0)},
			"last_activity_at": bson.M{"$max": "$created_at"},
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":              "$_id.project",
			"users_total":      bson.M{"$sum": cond(validUser, 1, 0)},
			"active_users_24h": bson.M{"$sum": cond(active(since24h), 1, 0)},
			"active_users_7d":  bson.M{"$sum": cond(active(since7d), 1, 0)},
			"messages_24h":     bson.M{"$sum": "$messages_24h"},
			"last_activity_at": bson.M{"$max": "$last_activity_at"},
		}}},
	}
}
