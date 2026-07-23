package engine

import (
	"context"
	"testing"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testOrchestrator() (*KubernetesOrchestrator, *fake.Clientset) {
	cs := fake.NewClientset()
	cfg := KubernetesConfig{
		Namespace:       "botmother",
		ImageRepository: "registry.iprogrammer.uz/botspace/golang-engine",
		ImageTag:        "1.1.0",
		InternalToken:   "internal-tok",
		TestBotToken:    "test-bot-tok",
		RedisAddr:       "redis:6379",
		RedisPassword:   "redis-pass",
		MongoAddr:       "mongodb://mongo:27017",
		MongoDBName:     "botspace_cms",
		APIURL:          "https://api.botspace.uz",
	}
	return NewKubernetesOrchestratorWithClient(cs, cfg), cs
}

func TestDeploy_BotRuntimeRequiresOwnerAndWebhook(t *testing.T) {
	orch, _ := testOrchestrator()
	base := DeploySpec{ProjectID: uuid.New(), Flow: []byte(`{"nodes":[]}`)}
	if _, err := orch.Deploy(context.Background(), base); err == nil {
		t.Fatal("bot runtime without owner_id must fail")
	}
	base.OwnerID = uuid.New()
	if _, err := orch.Deploy(context.Background(), base); err == nil {
		t.Fatal("bot runtime without webhook_uuid must fail")
	}
}

func TestDeploy_Module_UsesExplicitNameAndImage(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	spec := DeploySpec{
		ReleaseUUID:   uuid.New(),
		Name:          "module-my-slug",
		Image:         "registry.iprogrammer.uz/botspace/module-my-slug:latest",
		ContainerPort: 9000,
		ReadinessPath: "/health",
		LivenessPath:  "/health",
		Env:           map[string]string{"MODULE_SLUG": "my_slug", "PORT": "9000"},
	}
	result, err := orch.Deploy(ctx, spec)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if result.ServiceID != "module-my-slug" {
		t.Fatalf("ServiceID = %q", result.ServiceID)
	}

	dep, err := cs.AppsV1().Deployments("botmother").Get(ctx, "module-my-slug", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment not created: %v", err)
	}
	if dep.Spec.Template.Spec.Containers[0].Image != spec.Image {
		t.Errorf("image = %q, want %q", dep.Spec.Template.Spec.Containers[0].Image, spec.Image)
	}
	if dep.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Path != "/health" {
		t.Errorf("readiness path = %q", dep.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Path)
	}

	// Modules with no WebhookUUID get a Service named after the deployment.
	svc, err := cs.CoreV1().Services("botmother").Get(ctx, "module-my-slug", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("service not created: %v", err)
	}
	if svc.Spec.Ports[0].Port != 9000 {
		t.Errorf("service port = %d", svc.Spec.Ports[0].Port)
	}

	// No flow -> no ConfigMap.
	if _, err := cs.CoreV1().ConfigMaps("botmother").Get(ctx, "module-my-slug-flow", metav1.GetOptions{}); err == nil {
		t.Errorf("expected no flow configmap for a module deploy")
	}
}

func TestDeploy_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()
	spec := DeploySpec{
		Name:  "module-idempotent",
		Image: "registry.example/module:1",
		Env:   map[string]string{"MODULE_SLUG": "idempotent"},
	}

	if _, err := orch.Deploy(ctx, spec); err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	// Second deploy on the same name must update, not error.
	if _, err := orch.Deploy(ctx, spec); err != nil {
		t.Fatalf("second deploy (update path): %v", err)
	}

	list, err := cs.AppsV1().Deployments("botmother").List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected exactly 1 deployment after 2 deploys, got %d", len(list.Items))
	}
}

func TestStop_RemovesModuleDeploymentSecretAndService(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	result, err := orch.Deploy(ctx, DeploySpec{
		Name:  "module-stop-test",
		Image: "registry.example/module:1",
		Env:   map[string]string{"MODULE_SLUG": "stop-test"},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if err := orch.Stop(ctx, result.ServiceID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if _, err := cs.AppsV1().Deployments("botmother").Get(ctx, result.ServiceID, metav1.GetOptions{}); err == nil {
		t.Error("deployment still exists after Stop")
	}
	if _, err := cs.CoreV1().Secrets("botmother").Get(ctx, result.ServiceID+"-env", metav1.GetOptions{}); err == nil {
		t.Error("secret still exists after Stop")
	}
	if _, err := cs.CoreV1().Services("botmother").Get(ctx, result.ServiceID, metav1.GetOptions{}); err == nil {
		t.Error("service still exists after Stop")
	}
}

func TestStop_UnknownServiceID_NoError(t *testing.T) {
	orch, _ := testOrchestrator()
	if err := orch.Stop(context.Background(), "does-not-exist"); err != nil {
		t.Fatalf("Stop on unknown service should be a no-op, got: %v", err)
	}
	if err := orch.Stop(context.Background(), ""); err != nil {
		t.Fatalf("Stop(\"\") should be a no-op, got: %v", err)
	}
}

func TestStatus_MapsReplicaCountsToStatus(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	if got, err := orch.Status(ctx, ""); err != nil || got != StatusStopped {
		t.Fatalf("Status(\"\") = %v, %v", got, err)
	}
	if got, err := orch.Status(ctx, "no-such-deployment"); err != nil || got != StatusStopped {
		t.Fatalf("Status(missing) = %v, %v", got, err)
	}

	mk := func(name string, replicas, ready int32) {
		_, err := cs.AppsV1().Deployments("botmother").Create(ctx, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "botmother"},
			Status:     appsv1.DeploymentStatus{Replicas: replicas, ReadyReplicas: ready},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("seed deployment %s: %v", name, err)
		}
	}
	mk("running-dep", 1, 1)
	mk("init-dep", 1, 0)
	mk("empty-dep", 0, 0)

	cases := map[string]Status{
		"running-dep": StatusRunning,
		"init-dep":    StatusInitializing,
		"empty-dep":   StatusStopped,
	}
	for name, want := range cases {
		got, err := orch.Status(ctx, name)
		if err != nil {
			t.Fatalf("Status(%s): %v", name, err)
		}
		if got != want {
			t.Errorf("Status(%s) = %q, want %q", name, got, want)
		}
	}
}

// TestStatus_CrashLoopingPodReportsFailed mirrors Django's
// cleanup_failed_releases (core/apps/api/tasks/release.py,
// CRASH_RESTART_THRESHOLD=5): a deployment with replicas but a
// crash-looping pod must report failed, not initializing forever.
func TestStatus_CrashLoopingPodReportsFailed(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	labels := map[string]string{"app": "botmother-engine", "engine-name": "crash-dep"}
	_, err := cs.AppsV1().Deployments("botmother").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-dep", Namespace: "botmother"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}},
		Status:     appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 0},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	_, err = cs.CoreV1().Pods("botmother").Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-dep-abc", Namespace: "botmother", Labels: labels},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{RestartCount: 7}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("seed pod: %v", err)
	}

	got, err := orch.Status(ctx, "crash-dep")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != StatusFailed {
		t.Fatalf("Status(crash-dep) = %q, want failed", got)
	}
}

func TestReconcileStatus(t *testing.T) {
	cases := []struct {
		name     string
		db       Status
		live     Status
		expected Status
	}{
		{"initializing+running->running", StatusInitializing, StatusRunning, StatusRunning},
		{"pending+running->running", StatusPending, StatusRunning, StatusRunning},
		{"initializing+failed->failed", StatusInitializing, StatusFailed, StatusFailed},
		{"initializing+stopped(scheduling gap)->initializing", StatusInitializing, StatusStopped, StatusInitializing},
		{"initializing+still initializing->initializing", StatusInitializing, StatusInitializing, StatusInitializing},
		{"terminal running is never touched", StatusRunning, StatusFailed, StatusRunning},
		{"terminal stopped is never touched", StatusStopped, StatusRunning, StatusStopped},
		// "failed" is recoverable: a pod that came up after a transient/slow-start
		// failure gets promoted; a still-not-running one stays failed.
		{"failed+running->running (recovery)", StatusFailed, StatusRunning, StatusRunning},
		{"failed+failed stays failed", StatusFailed, StatusFailed, StatusFailed},
		{"failed+stopped stays failed", StatusFailed, StatusStopped, StatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReconcileStatus(tc.db, tc.live); got != tc.expected {
				t.Errorf("ReconcileStatus(%s, %s) = %s, want %s", tc.db, tc.live, got, tc.expected)
			}
		})
	}
}
