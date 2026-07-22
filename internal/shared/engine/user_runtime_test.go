package engine

import (
	"context"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEnsureOwnerRuntimeCreatesOneStableSupervisorDeployment(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()
	ownerID := uuid.New()

	first, err := orch.EnsureOwnerRuntime(ctx, ownerID)
	if err != nil {
		t.Fatalf("EnsureOwnerRuntime: %v", err)
	}
	second, err := orch.EnsureOwnerRuntime(ctx, ownerID)
	if err != nil {
		t.Fatalf("EnsureOwnerRuntime second call: %v", err)
	}
	if first != second || first != ownerDeploymentName(ownerID) {
		t.Fatalf("deployment names = %q, %q", first, second)
	}

	dep, err := cs.AppsV1().Deployments("botmother").Get(ctx, first, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("owner deployment missing: %v", err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if len(container.Command) != 1 || container.Command[0] != "/app/supervisor" {
		t.Fatalf("command = %#v", container.Command)
	}
	if dep.Labels["owner_id"] != ownerID.String() {
		t.Fatalf("owner label = %q", dep.Labels["owner_id"])
	}
	if _, ok := dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]; ok {
		t.Fatal("idempotent ensure must not force a rollout")
	}

	secret, err := cs.CoreV1().Secrets("botmother").Get(ctx, first+"-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("owner secret missing: %v", err)
	}
	if string(secret.Data["USER_ID"]) != ownerID.String() {
		t.Fatalf("USER_ID = %q", secret.Data["USER_ID"])
	}
}

func TestUserRuntimeServiceIDRoundTrip(t *testing.T) {
	ownerID, webhookID := uuid.New(), uuid.New()
	value := userRuntimeServiceID(ownerID, webhookID)
	ref, ok := parseUserRuntimeServiceID(value)
	if !ok || ref.OwnerID != ownerID || ref.WebhookID != webhookID {
		t.Fatalf("parse %q = %#v, %v", value, ref, ok)
	}
	if _, ok := parseUserRuntimeServiceID("release-legacy-1"); ok {
		t.Fatal("legacy service id must not parse as user runtime")
	}
}

func TestNormalizeJSONObject(t *testing.T) {
	if got := string(normalizeJSONObject([]byte(`[]`), `{"fallback":true}`)); got != `{"fallback":true}` {
		t.Fatalf("array normalization = %s", got)
	}
	if got := string(normalizeJSONObject([]byte(`{"nodes":[]}`), `{}`)); got != `{"nodes":[]}` {
		t.Fatalf("object normalization = %s", got)
	}
}
