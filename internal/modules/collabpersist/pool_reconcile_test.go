package collabpersist

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"
	"github.com/botspace/backend/internal/shared/engine"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func migrationSpec() poolRuntimeSpec {
	return poolRuntimeSpec{WebhookID: uuid.NewString(), ProjectID: "123", Debug: true, Name: "debug-123", RoutePort: 12000, Replicas: 2, Flow: json.RawMessage(`{"nodes":[{"id":"old"}],"edges":[]}`), Schemas: json.RawMessage(`{"users":{}}`), Modules: json.RawMessage(`{"modules":[]}`), Env: map[string]string{"RUNTIME_PRIMARY": "false", "RUNTIME_REPLICA_INDEX": "1", "BOT_TOKEN": "test-secret", "MONGO_ADDR": "mongodb://legacy", "MONGO_DB_NAME": "legacy"}}
}

func encodeMigrationSpec(t *testing.T, spec poolRuntimeSpec) string {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUpgradeRuntimeIdentity_NumericTenantAndHotReload(t *testing.T) {
	spec := migrationSpec()
	id := uuid.NewString()
	raw := encodeMigrationSpec(t, spec)
	// Preserve future fields as well as the newly added canonical identity.
	raw = strings.TrimSuffix(raw, "}") + `,"future_config":{"enabled":true}}`
	cfg := engine.PoolMongoConfig{URI: "mongodb://cms", Database: "cms"}
	next, err := upgradeRuntimeIdentity(raw, spec, id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	flow := json.RawMessage(`{"nodes":[{"id":"new"}],"edges":[]}`)
	next, err = replaceRuntimeFlow(next, flow)
	if err != nil {
		t.Fatal(err)
	}
	var got poolRuntimeSpec
	if err := json.Unmarshal([]byte(next), &got); err != nil {
		t.Fatal(err)
	}
	if got.BackendProjectID != id || got.ProjectID != "123" || got.Env["PROJECT_ID"] != "123" || got.Env["BACKEND_PROJECT_ID"] != id {
		t.Fatal("canonical or tenant identity lost")
	}
	if got.RoutePort != spec.RoutePort || got.Replicas != 2 || got.Env["RUNTIME_PRIMARY"] != "false" || got.Env["RUNTIME_REPLICA_INDEX"] != "1" || got.Env["BOT_TOKEN"] != spec.Env["BOT_TOKEN"] || string(got.Schemas) != string(spec.Schemas) || string(got.Modules) != string(spec.Modules) {
		t.Fatal("routing/credentials/replica metadata changed")
	}
	if string(got.Flow) != string(flow) || !strings.Contains(next, `"future_config":{"enabled":true}`) {
		t.Fatal("flow update/unknown field preservation failed")
	}
	if got.Env["APP_MONGO_URL"] != cfg.URI || got.Env["MONGO_ADDR"] != cfg.URI || got.Env["APP_ENGINE_MONGO_DB_NAME"] != cfg.Database || got.Env["MONGO_DB_NAME"] != cfg.Database {
		t.Fatal("Mongo destination not normalized")
	}
	again, err := upgradeRuntimeIdentity(next, spec, id, cfg)
	if err != nil || again != next {
		t.Fatalf("repair not byte-idempotent: %v", err)
	}
	again, err = replaceRuntimeFlow(next, flow)
	if err != nil || again != next {
		t.Fatalf("hot reload not byte-idempotent: %v", err)
	}
}

func TestUpgradeRuntimeIdentity_RejectsConflicts(t *testing.T) {
	expected := migrationSpec()
	id := uuid.NewString()
	cfg := engine.PoolMongoConfig{URI: "mongodb://cms", Database: "cms"}
	for _, field := range []string{"backend", "env", "hook", "tenant", "mode"} {
		t.Run(field, func(t *testing.T) {
			spec := migrationSpec()
			spec.WebhookID = expected.WebhookID
			switch field {
			case "backend":
				spec.BackendProjectID = uuid.NewString()
			case "env":
				spec.Env["BACKEND_PROJECT_ID"] = uuid.NewString()
			case "hook":
				spec.WebhookID = uuid.NewString()
			case "tenant":
				spec.ProjectID = "999"
			case "mode":
				spec.Debug = false
			}
			if _, err := upgradeRuntimeIdentity(encodeMigrationSpec(t, spec), expected, id, cfg); err == nil {
				t.Fatal("conflict accepted")
			}
		})
	}
	if _, err := upgradeRuntimeIdentity(encodeMigrationSpec(t, expected), expected, "123", cfg); err == nil {
		t.Fatal("noncanonical mapping accepted")
	}
	if _, err := upgradeRuntimeIdentity(encodeMigrationSpec(t, expected), expected, id, engine.PoolMongoConfig{}); err == nil {
		t.Fatal("unknown Mongo destination accepted")
	}
}

func TestVerifiedRuntimeProject_SQLWebhookAndMode(t *testing.T) {
	p := sqlcdb.Project{ID: uuid.New(), DebugWebhookUuid: uuid.New(), ReleaseWebhookUuid: uuid.New()}
	for _, debug := range []bool{false, true} {
		hook := p.ReleaseWebhookUuid
		if debug {
			hook = p.DebugWebhookUuid
		}
		got, err := verifiedRuntimeProject(p, hook.String(), debug)
		if err != nil || got != p.ID.String() {
			t.Fatalf("mapping failed: %v", err)
		}
		if _, err := verifiedRuntimeProject(p, hook.String(), !debug); err == nil {
			t.Fatal("wrong mode accepted")
		}
	}
	for _, hook := range []string{p.ID.String(), uuid.NewString(), "123", uuid.Nil.String()} {
		if _, err := verifiedRuntimeProject(p, hook, true); err == nil {
			t.Fatal("project/foreign/nil id treated as webhook")
		}
	}
}

func TestLoadPoolMongoConfig_UsesAPISettingsWithoutAPIValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("configs", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("configs", "config.yaml"), []byte("engine:\n  mongo_db_name: yaml_cms\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_CRYPTO_KEY", "") // Worker must not require API secrets.
	t.Setenv("APP_MONGO_URL", "mongodb://canonical")
	t.Setenv("APP_ENGINE_MONGO_DB_NAME", "")
	cfg, err := loadPoolMongoConfig()
	if err != nil || cfg.Database != "yaml_cms" || cfg.URI != "mongodb://canonical" {
		t.Fatalf("settings=%+v err=%v", cfg, err)
	}
	t.Setenv("APP_ENGINE_MONGO_DB_NAME", "env_cms")
	cfg, err = loadPoolMongoConfig()
	if err != nil || cfg.Database != "env_cms" {
		t.Fatal("env did not win")
	}
}

type startupReconciler struct{ called chan struct{} }

func (r startupReconciler) ReconcileRuntimes(context.Context) error {
	r.called <- struct{}{}
	return errors.New("retry later")
}
func TestReconcileRuntimes_RunsAtStartupAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := startupReconciler{called: make(chan struct{}, 1)}
	u := &Usecase{Log: zap.NewNop()}
	done := make(chan struct{})
	go func() { defer close(done); u.reconcileRuntimes(ctx, r) }()
	select {
	case <-r.called:
	case <-time.After(time.Second):
		t.Fatal("startup repair did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("repair loop did not stop")
	}
}
