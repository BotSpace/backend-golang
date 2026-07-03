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

func TestDeploy_Release_CreatesDeploymentSecretConfigMapAndService(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	projectID := uuid.New()
	webhookUUID := uuid.New()

	result, err := orch.Deploy(ctx, DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: uuid.New(),
		Version:     3,
		Debug:       false,
		Flow:        []byte(`{"nodes":[]}`),
		Env:         map[string]string{"IS_RELEASE": "true"},
		WebhookUUID: webhookUUID,
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	wantName := "release-" + projectID.String() + "-3"
	if result.ServiceID != wantName {
		t.Fatalf("ServiceID = %q, want %q", result.ServiceID, wantName)
	}
	if result.Status != StatusInitializing {
		t.Fatalf("Status = %q, want initializing", result.Status)
	}

	dep, err := cs.AppsV1().Deployments("botmother").Get(ctx, wantName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment not created: %v", err)
	}
	if got := dep.Spec.Template.Spec.Containers[0].Image; got != "registry.iprogrammer.uz/botspace/golang-engine:1.1.0" {
		t.Errorf("image = %q", got)
	}
	if dep.Labels["project_id"] != projectID.String() {
		t.Errorf("missing project_id label: %+v", dep.Labels)
	}
	if dep.Spec.Template.Spec.ImagePullSecrets[0].Name != "registry-secret" {
		t.Errorf("imagePullSecrets = %+v", dep.Spec.Template.Spec.ImagePullSecrets)
	}
	if dep.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Errorf("strategy type = %v", dep.Spec.Strategy.Type)
	}
	if dep.Spec.Strategy.RollingUpdate.MaxUnavailable.IntValue() != 0 || dep.Spec.Strategy.RollingUpdate.MaxSurge.IntValue() != 1 {
		t.Errorf("rolling update = %+v", dep.Spec.Strategy.RollingUpdate)
	}

	secret, err := cs.CoreV1().Secrets("botmother").Get(ctx, wantName+"-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	for _, want := range []string{"IS_RELEASE", "FLOW_FILE_PATH", "WEBHOOK_ENABLED", "PROJECT_ID", "API_TOKEN", "INTERNAL_TOKEN", "REDIS_ADDR", "MONGO_ADDR", "API_URL"} {
		if _, ok := secret.Data[want]; !ok {
			t.Errorf("secret missing key %q (got %v)", want, keysOf(secret.Data))
		}
	}
	if string(secret.Data["PROJECT_ID"]) != projectID.String() {
		t.Errorf("PROJECT_ID = %q", secret.Data["PROJECT_ID"])
	}

	cm, err := cs.CoreV1().ConfigMaps("botmother").Get(ctx, wantName+"-flow", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("configmap not created: %v", err)
	}
	if cm.Data["flow.json"] != `{"nodes":[]}` {
		t.Errorf("flow.json = %q", cm.Data["flow.json"])
	}

	svc, err := cs.CoreV1().Services("botmother").Get(ctx, "bot-"+webhookUUID.String(), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("service not created: %v", err)
	}
	if svc.Spec.Selector["engine-name"] != wantName {
		t.Errorf("service selector = %+v", svc.Spec.Selector)
	}
}

func TestDeploy_Debug_UsesDebugNamingAndDebugEnv(t *testing.T) {
	ctx := context.Background()
	orch, _ := testOrchestrator()

	projectID := uuid.New()
	result, err := orch.Deploy(ctx, DeploySpec{
		ProjectID: projectID,
		Debug:     true,
		Flow:      []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	want := "debug-" + projectID.String()
	if result.ServiceID != want {
		t.Fatalf("ServiceID = %q, want %q", result.ServiceID, want)
	}
}

func TestDeploy_Release_MapsReleaseBotTokenToBotToken(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	projectID := uuid.New()
	result, err := orch.Deploy(ctx, DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: uuid.New(),
		Version:     1,
		Debug:       false,
		Flow:        []byte(`{}`),
		Env:         map[string]string{"RELEASE_BOT_TOKEN": "123:release-tok"},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	secret, err := cs.CoreV1().Secrets("botmother").Get(ctx, result.ServiceID+"-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	if got := string(secret.Data["BOT_TOKEN"]); got != "123:release-tok" {
		t.Errorf("BOT_TOKEN = %q, want RELEASE_BOT_TOKEN value", got)
	}
}

func TestDeploy_Release_ExplicitBotTokenWinsOverReleaseBotToken(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	projectID := uuid.New()
	result, err := orch.Deploy(ctx, DeploySpec{
		ProjectID:   projectID,
		ReleaseUUID: uuid.New(),
		Version:     1,
		Debug:       false,
		Flow:        []byte(`{}`),
		Env:         map[string]string{"BOT_TOKEN": "explicit-tok", "RELEASE_BOT_TOKEN": "123:release-tok"},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	secret, err := cs.CoreV1().Secrets("botmother").Get(ctx, result.ServiceID+"-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	if got := string(secret.Data["BOT_TOKEN"]); got != "explicit-tok" {
		t.Errorf("BOT_TOKEN = %q, want explicit value preserved", got)
	}
}

func TestDeploy_Debug_DoesNotFallBackToReleaseBotToken(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()

	projectID := uuid.New()
	result, err := orch.Deploy(ctx, DeploySpec{
		ProjectID: projectID,
		Debug:     true,
		Flow:      []byte(`{}`),
		Env:       map[string]string{"RELEASE_BOT_TOKEN": "123:release-tok"},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	secret, err := cs.CoreV1().Secrets("botmother").Get(ctx, result.ServiceID+"-env", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	// Debug mode must ignore RELEASE_BOT_TOKEN and fall straight through to
	// the shared TestBotToken fallback (mirrors kubernetes_service.py's
	// `prefix == "release"` guard).
	if got := string(secret.Data["BOT_TOKEN"]); got != "test-bot-tok" {
		t.Errorf("BOT_TOKEN = %q, want TestBotToken fallback", got)
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
	projectID := uuid.New()
	spec := DeploySpec{ProjectID: projectID, Version: 1, Flow: []byte(`{}`)}

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

func TestStop_RemovesDeploymentSecretConfigMapAndService(t *testing.T) {
	ctx := context.Background()
	orch, cs := testOrchestrator()
	projectID := uuid.New()
	webhookUUID := uuid.New()

	result, err := orch.Deploy(ctx, DeploySpec{
		ProjectID: projectID, Version: 1, Flow: []byte(`{}`), WebhookUUID: webhookUUID,
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
	if _, err := cs.CoreV1().ConfigMaps("botmother").Get(ctx, result.ServiceID+"-flow", metav1.GetOptions{}); err == nil {
		t.Error("configmap still exists after Stop")
	}
	if _, err := cs.CoreV1().Services("botmother").Get(ctx, "bot-"+webhookUUID.String(), metav1.GetOptions{}); err == nil {
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
		{"initializing+stopped(vanished)->failed", StatusInitializing, StatusStopped, StatusFailed},
		{"initializing+still initializing->initializing", StatusInitializing, StatusInitializing, StatusInitializing},
		{"terminal running is never touched", StatusRunning, StatusFailed, StatusRunning},
		{"terminal stopped is never touched", StatusStopped, StatusRunning, StatusStopped},
		{"terminal failed is never touched", StatusFailed, StatusRunning, StatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReconcileStatus(tc.db, tc.live); got != tc.expected {
				t.Errorf("ReconcileStatus(%s, %s) = %s, want %s", tc.db, tc.live, got, tc.expected)
			}
		})
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
