package engine

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestPoolRuntimeSpec_NumericTenantCanonicalIdentityAndMongo(t *testing.T) {
	t.Setenv("APP_MONGO_URL", "mongodb://canonical-cms:27017")
	t.Setenv("APP_ENGINE_MONGO_DB_NAME", "canonical_cms")
	orch := &KubernetesOrchestrator{cfg: KubernetesConfig{MongoAddr: "mongodb://old-engine:27017", MongoDBName: "old_engine", TestBotToken: "test-bot", InternalToken: "internal"}}
	id := uuid.New()
	for _, debug := range []bool{false, true} {
		input := DeploySpec{ProjectID: id, RuntimeProjectID: "123", OwnerID: uuid.New(), WebhookUUID: uuid.New(), Debug: debug, Flow: []byte(`{"nodes":[],"edges":[]}`), Env: map[string]string{
			"PROJECT_ID": "wrong-tenant", "BACKEND_PROJECT_ID": uuid.NewString(),
			"APP_MONGO_URL": "mongodb://project-override", "MONGO_ADDR": "mongodb://project-legacy",
			"APP_ENGINE_MONGO_DB_NAME": "project_override", "MONGO_DB_NAME": "legacy_override",
			"RELEASE_BOT_TOKEN": "release-bot",
		}}
		spec, err := orch.poolRuntimeSpec(input, 12345, 2)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["backend_project_id"] != id.String() || wire["project_id"] != "123" {
			t.Fatalf("identity not preserved: backend=%v tenant=%v", wire["backend_project_id"], wire["project_id"])
		}
		if spec.Env["BACKEND_PROJECT_ID"] != id.String() || spec.Env["PROJECT_ID"] != "123" {
			t.Fatal("wrong child identity")
		}
		if spec.Env["APP_MONGO_URL"] != "mongodb://canonical-cms:27017" || spec.Env["MONGO_ADDR"] != spec.Env["APP_MONGO_URL"] || spec.Env["APP_ENGINE_MONGO_DB_NAME"] != "canonical_cms" || spec.Env["MONGO_DB_NAME"] != "canonical_cms" {
			t.Fatal("canonical Mongo configuration did not override legacy/project env")
		}
		if input.Env["PROJECT_ID"] != "wrong-tenant" {
			t.Fatal("input env mutated")
		}
		if spec.RoutePort != 12345 || spec.Replicas != 2 {
			t.Fatal("routing metadata changed")
		}
	}
}

func TestPoolRuntimeSpec_UUIDAndMissingIdentity(t *testing.T) {
	t.Setenv("APP_MONGO_URL", "")
	t.Setenv("APP_ENGINE_MONGO_DB_NAME", "")
	orch := &KubernetesOrchestrator{cfg: KubernetesConfig{MongoAddr: "mongodb://legacy", MongoDBName: "yaml_cms", TestBotToken: "test"}}
	input := DeploySpec{ProjectID: uuid.New(), WebhookUUID: uuid.New(), Debug: true}
	spec, err := orch.poolRuntimeSpec(input, 12345, 1)
	if err != nil {
		t.Fatal(err)
	}
	if spec.ProjectID != input.ProjectID.String() || spec.BackendProjectID != input.ProjectID.String() {
		t.Fatal("UUID runtime identity mismatch")
	}
	if spec.Env["APP_MONGO_URL"] != "mongodb://localhost:27017" || spec.Env["APP_ENGINE_MONGO_DB_NAME"] != "yaml_cms" {
		t.Fatal("CMS defaults/config override not used")
	}
	input.ProjectID = uuid.Nil
	if _, err := orch.poolRuntimeSpec(input, 12345, 1); err == nil {
		t.Fatal("nil canonical identity accepted")
	}
	if got := CanonicalPoolMongoConfig(""); got.Database != "botspace_cms" {
		t.Fatal("wrong default database")
	}
}
