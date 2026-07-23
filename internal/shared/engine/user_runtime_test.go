package engine

import (
	"context"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnsureEnginePoolCreatesOneSharedDeployment(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	first, err := orch.EnsureEnginePool(ctx)
	if err != nil {
		t.Fatalf("EnsureEnginePool: %v", err)
	}
	second, err := orch.EnsureEnginePool(ctx)
	if err != nil {
		t.Fatalf("EnsureEnginePool second call: %v", err)
	}
	if first != second || first != poolDeploymentName {
		t.Fatalf("deployment names = %q, %q", first, second)
	}

	dep, err := cs.AppsV1().Deployments("botmother").Get(ctx, first, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pool deployment missing: %v", err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if len(container.Command) != 1 || container.Command[0] != "/app/supervisor" {
		t.Fatalf("command = %#v", container.Command)
	}
	if dep.Labels["app"] != "botmother-engine-pool" {
		t.Fatalf("pool labels = %#v", dep.Labels)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Fatalf("pool replicas = %#v", dep.Spec.Replicas)
	}
	if _, ok := dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]; ok {
		t.Fatal("idempotent ensure must not force a rollout")
	}

	secret, err := cs.CoreV1().Secrets("botmother").Get(ctx, first+"-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pool secret missing: %v", err)
	}
	if _, exists := secret.Data["USER_ID"]; exists {
		t.Fatal("shared pool secret must not contain USER_ID")
	}
}

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
