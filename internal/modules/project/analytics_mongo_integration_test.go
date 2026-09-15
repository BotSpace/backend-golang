//go:build integration

package project

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// A disposable real MongoDB verifies aggregation semantics without mocking the driver.
func TestMongoAnalytics_ConversationSemantics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "-p", "127.0.0.1::27017", "mongo:7")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("start test Mongo: %v: %s", err, stderr.String())
	}
	container := strings.TrimSpace(string(raw))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(cleanup, "docker", "rm", "-f", container).CombinedOutput(); err != nil {
			t.Errorf("cleanup Mongo: %v %s", err, out)
		}
	})
	raw, err = exec.CommandContext(ctx, "docker", "port", container, "27017/tcp").CombinedOutput()
	if err != nil {
		t.Fatalf("Mongo port: %v %s", err, raw)
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://"+strings.TrimSpace(string(raw))).SetServerSelectionTimeout(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
	db := client.Database("analytics_test")
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	event := func(project, direction string, user int64, at time.Time) bson.M {
		return bson.M{"project_id": project, "direction": direction, "user_id": user, "created_at": at}
	}
	docs := []any{
		event("a", "inbound", 1, now.Add(-8*24*time.Hour)),
		event("a", "inbound", 1, now.Add(-time.Hour)),
		event("a", "inbound", 1, now.Add(-2*time.Hour)),
		event("a", "inbound", 2, now.Add(-7*24*time.Hour)),
		event("a", "inbound", 3, now.Add(-24*time.Hour)),
		event("a", "inbound", 4, now.Add(-8*24*time.Hour)),
		event("a", "outbound", 4, now), // Outbound must not activate user 4.
		event("a", "outbound", 99, now),
		event("a", "inbound", 0, now),
		event("a", "inbound", -1, now),
		event("a", "inbound", 100, now.Add(time.Hour)), // Future timestamps excluded.
		event("a", "invalid", 100, now),
		event("b", "inbound", 1, now), // Same user, separate bot membership.
		event("not-requested", "inbound", 55, now),
		bson.M{"project_id": "a", "direction": "inbound", "user_id": 101, "created_at": "2026-09-15"},
	}
	if _, err := db.Collection("bot_conversations").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	repo := NewMongoAnalyticsRepository(db)
	result, err := repo.ReadAnalytics(ctx, []string{"a", "b", "empty"}, now)
	if err != nil {
		t.Fatal(err)
	}
	a := result["a"]
	if a.UsersTotal != 4 || a.ActiveUsers24h != 2 || a.ActiveUsers7d != 3 || a.Messages24h != 7 || a.LastActivityAt == nil || !a.LastActivityAt.Equal(now) {
		t.Fatalf("unexpected analytics: %+v", a)
	}
	if result["b"].UsersTotal != 1 || result["empty"].UsersTotal != 0 || len(result) != 2 {
		t.Fatalf("scope/empty: %+v", result)
	}
	if got, err := repo.ReadAnalytics(ctx, nil, now); err != nil || len(got) != 0 {
		t.Fatal(fmt.Sprint(got, err))
	}
}
