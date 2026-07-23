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
