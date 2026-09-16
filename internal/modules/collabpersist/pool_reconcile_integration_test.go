//go:build integration

package collabpersist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/botspace/backend/internal/shared/engine"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func poolTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-d", "-p", "127.0.0.1::6379", "redis:7-alpine")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("start test Redis: %v %s", err, stderr.String())
	}
	id := strings.TrimSpace(string(raw))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", id).CombinedOutput(); err != nil {
			t.Errorf("remove Redis: %v %s", err, out)
		}
	})
	raw, err = exec.CommandContext(ctx, "docker", "port", id, "6379/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: strings.TrimSpace(string(raw))})
	t.Cleanup(func() { _ = rdb.Close() })
	for rdb.Ping(ctx).Err() != nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return rdb
}

type migrationRepo struct {
	Repository
	id, hook string
	flow     json.RawMessage
	fail     bool
}

func (r *migrationRepo) ResolveRuntimeProject(_ context.Context, ref, hook string, debug bool) (string, error) {
	if r.fail || ref != "123" || hook != r.hook || !debug {
		return "", errors.New("SQL mapping unavailable: private credentials")
	}
	return r.id, nil
}
func (r *migrationRepo) RuntimeSnapshot(context.Context, string) (string, json.RawMessage, error) {
	return r.hook, r.flow, nil
}

func TestPoolMigration_RedisTransactionsAndHotReload(t *testing.T) {
	rdb := poolTestRedis(t)
	ctx := context.Background()
	spec := migrationSpec()
	id := uuid.NewString()
	repo := &migrationRepo{id: id, hook: spec.WebhookID, flow: json.RawMessage(`{"nodes":[{"id":"new"}],"edges":[]}`)}
	r := &poolReloader{repo: repo, rdb: rdb, log: zap.NewNop(), mongo: engine.PoolMongoConfig{URI: "mongodb://cms", Database: "cms"}}
	desired := encodeMigrationSpec(t, spec)
	hash := "engine:pool:worker:offline-worker:runtimes"
	if err := rdb.HSet(ctx, poolDesiredHash, spec.WebhookID, desired).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.HSet(ctx, hash, spec.WebhookID, desired).Err(); err != nil {
		t.Fatal(err)
	}
	// No placements/live-worker heartbeat: offline copies still need repair.
	if err := r.ReconcileRuntimes(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{poolDesiredHash, hash} {
		raw, err := rdb.HGet(ctx, key, spec.WebhookID).Result()
		if err != nil {
			t.Fatal(err)
		}
		var got poolRuntimeSpec
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if got.BackendProjectID != id || got.ProjectID != "123" || got.Env["RUNTIME_PRIMARY"] != "false" {
			t.Fatal("repair lost identity/replica")
		}
	}
	stream := "engine:pool:worker:offline-worker:commands"
	if n := rdb.XLen(ctx, stream).Val(); n != 1 {
		t.Fatalf("reload commands=%d", n)
	}
	if err := r.ReconcileRuntimes(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rdb.XLen(ctx, stream).Val(); n != 1 {
		t.Fatal("idempotent repair enqueued reload")
	}
	if err := r.ReloadProject(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{poolDesiredHash, hash} {
		raw := rdb.HGet(ctx, key, spec.WebhookID).Val()
		var got poolRuntimeSpec
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if got.BackendProjectID != id || got.Env["BACKEND_PROJECT_ID"] != id || got.Env["APP_MONGO_URL"] != "mongodb://cms" || string(got.Flow) != string(repo.flow) {
			t.Fatal("hot reload lost identity/Mongo/flow")
		}
	}
	if n := rdb.XLen(ctx, stream).Val(); n != 2 {
		t.Fatalf("flow reload commands=%d", n)
	}
	// Unknown projects fail closed; one bad spec does not prevent other repairs.
	unknown := migrationSpec()
	unknown.ProjectID = "999"
	unknownRaw := encodeMigrationSpec(t, unknown)
	if err := rdb.HSet(ctx, poolDesiredHash, unknown.WebhookID, unknownRaw).Err(); err != nil {
		t.Fatal(err)
	}
	err := r.ReconcileRuntimes(ctx)
	if err == nil || strings.Contains(err.Error(), "private credentials") {
		t.Fatalf("unsafe error: %v", err)
	}
	if got := rdb.HGet(ctx, poolDesiredHash, unknown.WebhookID).Val(); got != unknownRaw {
		t.Fatal("unmapped spec changed")
	}
	// A missing desired record must not be recreated from orphan worker copies.
	if err := rdb.HDel(ctx, poolDesiredHash, spec.WebhookID, unknown.WebhookID).Err(); err != nil {
		t.Fatal(err)
	}
	before := rdb.HGet(ctx, hash, spec.WebhookID).Val()
	if err := r.ReconcileRuntimes(ctx); err != nil {
		t.Fatal(err)
	}
	if rdb.HExists(ctx, poolDesiredHash, spec.WebhookID).Val() || rdb.HGet(ctx, hash, spec.WebhookID).Val() != before {
		t.Fatal("orphan runtime resurrected")
	}
}

func TestPoolMigration_ConcurrentWriteAndStopAreNotOverwritten(t *testing.T) {
	rdb := poolTestRedis(t)
	ctx := context.Background()
	r := &poolReloader{rdb: rdb}
	hook := uuid.NewString()
	hash := "engine:pool:worker:worker:runtimes"
	if err := rdb.HSet(ctx, poolDesiredHash, hook, "old").Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.HSet(ctx, hash, hook, "old-worker").Err(); err != nil {
		t.Fatal(err)
	}
	err := r.updateRuntimeCopies(ctx, hook, "old", []string{hash}, func(raw string) (string, error) {
		if raw == "old" {
			if err := rdb.HSet(ctx, poolDesiredHash, hook, "concurrent-deploy").Err(); err != nil {
				return "", err
			}
		}
		return "repaired", nil
	})
	if !errors.Is(err, redis.TxFailedErr) {
		t.Fatalf("wanted CAS conflict, got %v", err)
	}
	if rdb.HGet(ctx, poolDesiredHash, hook).Val() != "concurrent-deploy" || rdb.HGet(ctx, hash, hook).Val() != "old-worker" {
		t.Fatal("concurrent state overwritten")
	}
	if n := rdb.XLen(ctx, "engine:pool:worker:worker:commands").Val(); n != 0 {
		t.Fatal("aborted repair queued command")
	}
	if err := rdb.HDel(ctx, poolDesiredHash, hook).Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.updateRuntimeCopies(ctx, hook, "concurrent-deploy", []string{hash}, func(raw string) (string, error) { t.Fatal("transform invoked after stop"); return raw, nil }); err != nil {
		t.Fatal(err)
	}
	if rdb.HExists(ctx, poolDesiredHash, hook).Val() {
		t.Fatal("stopped desired spec recreated")
	}
}
