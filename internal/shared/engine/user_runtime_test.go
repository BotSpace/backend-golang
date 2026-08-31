package engine

import (
	"testing"

	"github.com/google/uuid"
)

func TestPoolRuntimeServiceIDRoundTrip(t *testing.T) {
	webhookID := uuid.New()
	value := poolRuntimeServiceID(webhookID)
	ref, ok := parsePoolRuntimeServiceID(value)
	if !ok || ref.WebhookID != webhookID {
		t.Fatalf("parse %q = %#v, %v", value, ref, ok)
	}
}

func TestCanonicalJSONObject(t *testing.T) {
	if _, err := canonicalJSONObject([]byte(`[]`), `{}`, "flow"); err == nil {
		t.Fatal("array must be rejected")
	}
	got, err := canonicalJSONObject([]byte(`{"nodes":[]}`), `{}`, "flow")
	if err != nil {
		t.Fatalf("object rejected: %v", err)
	}
	if string(got) != `{"nodes":[]}` {
		t.Fatalf("object normalization = %s", got)
	}
}

func TestRuntimeStatusFailed(t *testing.T) {
	for _, status := range []string{"failed", "permanent_failure:abc123"} {
		if !runtimeStatusFailed(status) {
			t.Fatalf("runtimeStatusFailed(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"", "loading", "ready", "permanent_failure"} {
		if runtimeStatusFailed(status) {
			t.Fatalf("runtimeStatusFailed(%q) = true, want false", status)
		}
	}
}

func TestConfigurePoolRuntimeMode_DebugUsesConfiguredToken(t *testing.T) {
	env := map[string]string{
		"BOT_TOKEN":         "project-bot-token",
		"RELEASE_BOT_TOKEN": "project-release-token",
		"IS_RELEASE":        "true",
	}

	if err := configurePoolRuntimeMode(env, true, "configured-test-token", "internal-token"); err != nil {
		t.Fatalf("configure debug runtime: %v", err)
	}
	if got := env["BOT_TOKEN"]; got != "configured-test-token" {
		t.Fatalf("BOT_TOKEN = %q, want configured test token", got)
	}
	if _, ok := env["RELEASE_BOT_TOKEN"]; ok {
		t.Fatal("debug runtime retained RELEASE_BOT_TOKEN")
	}
	if _, ok := env["IS_RELEASE"]; ok {
		t.Fatal("debug runtime retained project IS_RELEASE")
	}
	if env["DEBUG_MODE"] != "true" || env["DEBUG_EVENT_TOKEN"] != "internal-token" {
		t.Fatalf("debug controls = %#v", env)
	}
}

func TestConfigurePoolRuntimeMode_ReleaseUsesReleaseToken(t *testing.T) {
	env := map[string]string{
		"BOT_TOKEN":         "project-bot-token",
		"RELEASE_BOT_TOKEN": "release-token",
		"IS_RELEASE":        "false",
		"DEBUG_MODE":        "true",
		"DEBUG_EVENT_TOKEN": "project-event-token",
	}

	if err := configurePoolRuntimeMode(env, false, "configured-test-token", "internal-token"); err != nil {
		t.Fatalf("configure release runtime: %v", err)
	}
	if got := env["BOT_TOKEN"]; got != "release-token" {
		t.Fatalf("BOT_TOKEN = %q, want release token", got)
	}
	if env["IS_RELEASE"] != "true" {
		t.Fatalf("IS_RELEASE = %q, want true", env["IS_RELEASE"])
	}
	if _, ok := env["DEBUG_MODE"]; ok {
		t.Fatal("release runtime retained project DEBUG_MODE")
	}
	if _, ok := env["DEBUG_EVENT_TOKEN"]; ok {
		t.Fatal("release runtime retained project DEBUG_EVENT_TOKEN")
	}
}

func TestConfigurePoolRuntimeMode_RequiresRuntimeControlledToken(t *testing.T) {
	if err := configurePoolRuntimeMode(map[string]string{"BOT_TOKEN": "project-token"}, true, "", "internal-token"); err == nil {
		t.Fatal("debug runtime without configured test token must fail")
	}
	if err := configurePoolRuntimeMode(map[string]string{"BOT_TOKEN": "project-token"}, false, "test-token", "internal-token"); err == nil {
		t.Fatal("release runtime without RELEASE_BOT_TOKEN must fail")
	}
}

func TestPoolAssignmentWorkerID(t *testing.T) {
	workerID, ok := poolAssignmentWorkerID("engine:pool:worker:pool-a:runtimes", ":runtimes")
	if !ok || workerID != "pool-a" {
		t.Fatalf("runtime assignment worker = %q, %v", workerID, ok)
	}
	workerID, ok = poolAssignmentWorkerID("engine:pool:worker:pool-b:status", ":status")
	if !ok || workerID != "pool-b" {
		t.Fatalf("status assignment worker = %q, %v", workerID, ok)
	}
	if _, ok := poolAssignmentWorkerID("engine:pool:worker::status", ":status"); ok {
		t.Fatal("empty worker id accepted")
	}
}

func TestRendezvousWorkersStableAndBounded(t *testing.T) {
	workers := []string{"pool-a", "pool-b", "pool-c", "pool-d"}
	first := rendezvousWorkers("bot-1", workers, 2)
	second := rendezvousWorkers("bot-1", append([]string(nil), workers...), 2)
	if len(first) != 2 || first[0] != second[0] || first[1] != second[1] {
		t.Fatalf("unstable placement: %v vs %v", first, second)
	}
	all := rendezvousWorkers("bot-1", workers, 20)
	if len(all) != len(workers) {
		t.Fatalf("replication must clamp to worker count: %v", all)
	}
}
