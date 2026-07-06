package k8sx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const testNamespace = "botmother"

// seedEngineResources creates a Deployment + "<name>-env" Secret +
// "<name>-flow" ConfigMap + a Service labeled "deployment=<name>" — the
// exact resource shape KubernetesOrchestrator.Deploy (the new backend) and
// the old Django kubernetes_service.py both create for a release/debug
// engine, so RemoveDeployment has something real to delete.
func seedEngineResources(t *testing.T, cs *fake.Clientset, name string) {
	t.Helper()
	ctx := context.Background()

	_, err := cs.AppsV1().Deployments(testNamespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = cs.CoreV1().Secrets(testNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-env", Namespace: testNamespace},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = cs.CoreV1().ConfigMaps(testNamespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-flow", Namespace: testNamespace},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = cs.CoreV1().Services(testNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bot-" + name,
			Namespace: testNamespace,
			Labels:    map[string]string{"deployment": name},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

func assertEngineResourcesGone(t *testing.T, cs *fake.Clientset, name string) {
	t.Helper()
	ctx := context.Background()

	_, err := cs.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
	assert.Error(t, err, "deployment %s should be deleted", name)

	_, err = cs.CoreV1().Secrets(testNamespace).Get(ctx, name+"-env", metav1.GetOptions{})
	assert.Error(t, err, "secret %s-env should be deleted", name)

	_, err = cs.CoreV1().ConfigMaps(testNamespace).Get(ctx, name+"-flow", metav1.GetOptions{})
	assert.Error(t, err, "configmap %s-flow should be deleted", name)

	svcs, err := cs.CoreV1().Services(testNamespace).List(ctx, metav1.ListOptions{LabelSelector: "deployment=" + name})
	require.NoError(t, err)
	assert.Empty(t, svcs.Items, "service for %s should be deleted", name)
}

func assertEngineResourcesPresent(t *testing.T, cs *fake.Clientset, name string) {
	t.Helper()
	ctx := context.Background()

	_, err := cs.AppsV1().Deployments(testNamespace).Get(ctx, name, metav1.GetOptions{})
	assert.NoError(t, err, "deployment %s must NOT be deleted", name)

	_, err = cs.CoreV1().Secrets(testNamespace).Get(ctx, name+"-env", metav1.GetOptions{})
	assert.NoError(t, err, "secret %s-env must NOT be deleted", name)

	_, err = cs.CoreV1().ConfigMaps(testNamespace).Get(ctx, name+"-flow", metav1.GetOptions{})
	assert.NoError(t, err, "configmap %s-flow must NOT be deleted", name)
}

// crashLoopingPod builds a pod stuck in CrashLoopBackOff with restartCount
// past the threshold, created createdAgo in the past.
func crashLoopingPod(podName, engineName string, restartCount int32, createdAgo time.Duration) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName,
			Namespace:         testNamespace,
			Labels:            map[string]string{"app": "botmother-engine", "engine-name": engineName},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-createdAgo)),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "engine",
				RestartCount: restartCount,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				},
			}},
		},
	}
}

// healthyPod builds a 1/1 Ready, Running pod (no restarts).
func healthyPod(podName, engineName string, createdAgo time.Duration) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName,
			Namespace:         testNamespace,
			Labels:            map[string]string{"app": "botmother-engine", "engine-name": engineName},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-createdAgo)),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "engine",
				Ready:        true,
				RestartCount: 0,
				State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func newTestClient(t *testing.T) (*Client, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset()
	log := zap.NewNop()
	return NewWithClient(cs, testNamespace, log), cs
}

func TestCleanupCrashLoopingEngines_CrashLoopingReleaseIsDeleted(t *testing.T) {
	client, cs := newTestClient(t)
	ctx := context.Background()
	const name = "release-11111111-1111-1111-1111-111111111111-1"

	seedEngineResources(t, cs, name)
	pod := crashLoopingPod("pod-1", name, 6, 2*time.Hour)
	_, err := cs.CoreV1().Pods(testNamespace).Create(ctx, &pod, metav1.CreateOptions{})
	require.NoError(t, err)

	removed, err := client.CleanupCrashLoopingEngines(ctx, time.Now())
	require.NoError(t, err)

	require.Len(t, removed, 1)
	assert.Equal(t, name, removed[0].Name)
	assert.Equal(t, EngineKindRelease, removed[0].Kind)
	assertEngineResourcesGone(t, cs, name)
}

func TestCleanupCrashLoopingEngines_CrashLoopingDebugIsDeleted(t *testing.T) {
	// Regression test for the bug that let debug-* bots crash-loop forever:
	// the old ReleaseEngineName only matched "release-" and silently skipped
	// every debug-* pod, however many times it restarted.
	client, cs := newTestClient(t)
	ctx := context.Background()
	const name = "debug-22222222-2222-2222-2222-222222222222"

	seedEngineResources(t, cs, name)
	pod := crashLoopingPod("pod-2", name, 40, 6*time.Hour)
	_, err := cs.CoreV1().Pods(testNamespace).Create(ctx, &pod, metav1.CreateOptions{})
	require.NoError(t, err)

	removed, err := client.CleanupCrashLoopingEngines(ctx, time.Now())
	require.NoError(t, err)

	require.Len(t, removed, 1)
	assert.Equal(t, name, removed[0].Name)
	assert.Equal(t, EngineKindDebug, removed[0].Kind)
	assertEngineResourcesGone(t, cs, name)
}

func TestCleanupCrashLoopingEngines_HealthyReleaseIsKept(t *testing.T) {
	client, cs := newTestClient(t)
	ctx := context.Background()
	const name = "release-33333333-3333-3333-3333-333333333333-2"

	seedEngineResources(t, cs, name)
	pod := healthyPod("pod-3", name, 2*time.Hour)
	_, err := cs.CoreV1().Pods(testNamespace).Create(ctx, &pod, metav1.CreateOptions{})
	require.NoError(t, err)

	removed, err := client.CleanupCrashLoopingEngines(ctx, time.Now())
	require.NoError(t, err)

	assert.Empty(t, removed, "a healthy 1/1 Ready bot must never be reported as failed")
	assertEngineResourcesPresent(t, cs, name)
}

func TestCleanupCrashLoopingEngines_YoungCrashLooperIsKeptDuringGrace(t *testing.T) {
	client, cs := newTestClient(t)
	ctx := context.Background()
	const name = "release-44444444-4444-4444-4444-444444444444-1"

	seedEngineResources(t, cs, name)
	// Same restart count/waiting-reason as the deleted case, but the pod is
	// only 2 minutes old — well inside FailureGracePeriod (10m). A bot that
	// just started (still pulling a large image, first boot retry) must be
	// given time before being treated as permanently failed.
	pod := crashLoopingPod("pod-4", name, 6, 2*time.Minute)
	_, err := cs.CoreV1().Pods(testNamespace).Create(ctx, &pod, metav1.CreateOptions{})
	require.NoError(t, err)

	removed, err := client.CleanupCrashLoopingEngines(ctx, time.Now())
	require.NoError(t, err)

	assert.Empty(t, removed, "a young crash-looping pod must be kept during the grace period")
	assertEngineResourcesPresent(t, cs, name)
}

func TestCleanupCrashLoopingEngines_NonBotDeploymentIsNeverTouched(t *testing.T) {
	client, cs := newTestClient(t)
	ctx := context.Background()

	// A shared module pod: same app=botmother-engine label (Deploy() stamps
	// it on every engine deployment, module or bot), same crash-loop signal,
	// but its engine-name has neither the "release-" nor "debug-" prefix.
	// It must NEVER be matched/deleted by this cleanup.
	const moduleName = "module-openrouter"
	seedEngineResources(t, cs, moduleName)
	modulePod := crashLoopingPod("pod-module", moduleName, 50, 6*time.Hour)
	_, err := cs.CoreV1().Pods(testNamespace).Create(ctx, &modulePod, metav1.CreateOptions{})
	require.NoError(t, err)

	// A completely unrelated deployment (e.g. backend-golang) with no
	// app=botmother-engine pod at all — EnginePods' label selector must not
	// pick it up.
	_, err = cs.AppsV1().Deployments(testNamespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "backend-golang", Namespace: testNamespace},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	removed, err := client.CleanupCrashLoopingEngines(ctx, time.Now())
	require.NoError(t, err)

	assert.Empty(t, removed)
	assertEngineResourcesPresent(t, cs, moduleName)
	_, err = cs.AppsV1().Deployments(testNamespace).Get(ctx, "backend-golang", metav1.GetOptions{})
	assert.NoError(t, err, "non-bot deployment must never be touched")
}

func TestBotEngineName(t *testing.T) {
	cases := []struct {
		label    string
		wantName string
		wantKind EngineKind
	}{
		{"release-abc-1", "release-abc-1", EngineKindRelease},
		{"debug-abc", "debug-abc", EngineKindDebug},
		{"module-openrouter", "", EngineKindNone},
		{"", "", EngineKindNone},
	}
	for _, tc := range cases {
		pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"engine-name": tc.label}}}
		name, kind := BotEngineName(pod)
		assert.Equal(t, tc.wantName, name, "label=%q", tc.label)
		assert.Equal(t, tc.wantKind, kind, "label=%q", tc.label)
	}
}
