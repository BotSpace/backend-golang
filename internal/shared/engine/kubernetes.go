// KubernetesOrchestrator is the real Orchestrator: it talks to the cluster's
// apps/v1 + core/v1 APIs directly, porting the exact resource shape the old
// Django kubernetes_service.py (core/apps/api/services/kubernetes_service.py)
// and module_service.py produced, so it manages the SAME kind of objects the
// already-running `release-*`/`debug-*`/`module-*` Deployments are (naming,
// labels, image, probes, RollingUpdate strategy, imagePullSecrets).
//
// Bot engines (release/debug — spec.Flow set): Deployment + "<name>-env"
// Secret + "<name>-flow" ConfigMap (flow.json mounted at /app/assets) +,
// when spec.WebhookUUID is set, a "bot-<uuid>" Service so Telegram webhook
// routing survives redeploys.
//
// Shared module pods (spec.Flow empty, spec.Name pre-set by the plugin
// module — see plugin.moduleDeploySpec): Deployment + "<name>-env" Secret +
// a "<name>" Service (module RPC callers dial the deployment name itself).
package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesConfig is the subset of config.EngineConfig the orchestrator
// needs, copied in at construction so this package never imports the config
// package (keeps the dependency direction shared -> nothing).
type KubernetesConfig struct {
	Namespace          string
	ImageRepository    string
	ImageTag           string
	RegistrySecretName string

	APIURL        string
	InternalToken string
	TestBotToken  string

	RedisAddr     string
	RedisPassword string

	MongoAddr   string
	MongoDBName string

	PoolSize        int
	RuntimeReplicas int
}

func (c KubernetesConfig) image() string { return c.ImageRepository + ":" + c.ImageTag }

// KubernetesOrchestrator implements Orchestrator against a real cluster.
type KubernetesOrchestrator struct {
	cs  kubernetes.Interface
	cfg KubernetesConfig
	rdb *redis.Client
}

// NewKubernetesOrchestrator builds a cluster client: in-cluster config first
// (the normal path — this backend runs as a pod in ns botmother), falling
// back to the local kubeconfig for development against a remote/staging
// cluster.
func NewKubernetesOrchestrator(cfg KubernetesConfig) (*KubernetesOrchestrator, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s config: %w", err)
		}
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s clientset: %w", err)
	}
	return newKubernetesOrchestrator(cs, cfg), nil
}

// NewKubernetesOrchestratorWithClient wires a pre-built clientset — used by
// tests to inject k8s.io/client-go/kubernetes/fake.
func NewKubernetesOrchestratorWithClient(cs kubernetes.Interface, cfg KubernetesConfig) *KubernetesOrchestrator {
	return newKubernetesOrchestrator(cs, cfg)
}

func newKubernetesOrchestrator(cs kubernetes.Interface, cfg KubernetesConfig) *KubernetesOrchestrator {
	if cfg.Namespace == "" {
		cfg.Namespace = "botmother"
	}
	if cfg.RegistrySecretName == "" {
		cfg.RegistrySecretName = "registry-secret"
	}
	if cfg.PoolSize < 1 {
		cfg.PoolSize = 3
	}
	if cfg.RuntimeReplicas < 1 {
		cfg.RuntimeReplicas = 1
	}
	var rdb *redis.Client
	if cfg.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	}
	return &KubernetesOrchestrator{cs: cs, cfg: cfg, rdb: rdb}
}

// ── Deploy ──────────────────────────────────────────────────────────────

func (o *KubernetesOrchestrator) Deploy(ctx context.Context, spec DeploySpec) (DeployResult, error) {
	if len(spec.Flow) > 0 && spec.OwnerID != uuid.Nil {
		return o.deployUserRuntime(ctx, spec)
	}
	name := resolveDeploymentName(spec)
	ns := o.cfg.Namespace

	labels := map[string]string{"app": "botmother-engine", "engine-name": name}
	if spec.ProjectID != uuid.Nil {
		labels["project_id"] = spec.ProjectID.String()
	}

	port := spec.ContainerPort
	if port == 0 {
		port = 8000
	}
	readinessPath := spec.ReadinessPath
	if readinessPath == "" {
		readinessPath = "/ready"
	}
	livenessPath := spec.LivenessPath
	if livenessPath == "" {
		livenessPath = "/health"
	}
	image := spec.Image
	if image == "" {
		image = o.cfg.image()
	}

	env := make(map[string]string, len(spec.Env)+8)
	for k, v := range spec.Env {
		env[k] = v
	}
	if o.cfg.APIURL != "" {
		env["API_URL"] = o.cfg.APIURL
	}
	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume

	// Bot-engine convention: a non-empty Flow means this is a release/debug
	// deploy (not a shared module pod) — mount the flow + wire the runtime
	// env the golang-engine binary expects.
	if len(spec.Flow) > 0 {
		cmName := name + "-flow"
		// The engine requires all three runtime assets to exist and be valid JSON.
		// The engine's loader.Schema is an OBJECT ({nodes,edges,...}); a brand-new
		// project's flow defaults to an empty array `[]` (Django JSONField
		// default=list parity), which the loader rejects with "cannot unmarshal
		// array into loader.Schema". Normalize any non-object flow to an empty
		// schema so tokensiz/debug bots with no flow yet still boot.
		flowJSON := strings.TrimSpace(string(spec.Flow))
		if flowJSON == "" || flowJSON[0] == '[' {
			flowJSON = `{"nodes":[],"edges":[]}`
		}
		schemasJSON := strings.TrimSpace(string(spec.Schemas))
		if schemasJSON == "" || schemasJSON[0] != '{' {
			schemasJSON = `{}`
		}
		modulesJSON := strings.TrimSpace(string(spec.Modules))
		if modulesJSON == "" || modulesJSON[0] != '{' {
			modulesJSON = `{"modules":[]}`
		}
		cmData := map[string]string{"flow.json": flowJSON, "schemas.json": schemasJSON}
		if err := o.ensureConfigMap(ctx, cmName, cmData); err != nil {
			return DeployResult{}, fmt.Errorf("ensure flow configmap: %w", err)
		}
		runtimeSecretName := name + "-runtime"
		if err := o.ensureDataSecret(ctx, runtimeSecretName, map[string][]byte{"modules.json": []byte(modulesJSON)}); err != nil {
			return DeployResult{}, fmt.Errorf("ensure runtime asset secret: %w", err)
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "flow-volume", MountPath: "/app/assets", ReadOnly: true})
		volumes = append(volumes, corev1.Volume{
			Name: "flow-volume",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{
					{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}}},
					{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: runtimeSecretName}}},
				}},
			},
		})

		env["FLOW_FILE_PATH"] = "/app/assets/flow.json"
		env["SCHEMAS_FILE_PATH"] = "/app/assets/schemas.json"
		env["MODULE_CONFIG_PATH"] = "/app/assets/modules.json"
		env["WEBHOOK_ENABLED"] = "true"
		env["WEBHOOK_PORT"] = strconv.Itoa(port)
		if o.cfg.RedisAddr != "" {
			env["REDIS_ENABLED"] = "true"
			env["REDIS_ADDR"] = o.cfg.RedisAddr
			env["REDIS_PASSWORD"] = o.cfg.RedisPassword
			env["REDIS_PREFIX"] = redisPrefix(spec, name)
			// Route debug node/context/error events over the backend:events Redis
			// stream (consumed by startEventConsumer, which forwards them to the
			// constructor's WS room). Without this the engine falls back to HTTP
			// POST /api/debug/node-event/ — an endpoint the Go backend doesn't
			// serve (Django did) — so every node/context event 404s and the
			// constructor never sees node colors / variables / error alerts.
			env["BACKEND_EVENTS_STREAM"] = "true"
		}
		if o.cfg.MongoAddr != "" {
			env["MONGO_ADDR"] = o.cfg.MongoAddr
			env["MONGO_DB_NAME"] = o.cfg.MongoDBName
		}
		if spec.ProjectID != uuid.Nil {
			env["PROJECT_ID"] = spec.ProjectID.String()
		}
		if o.cfg.InternalToken != "" {
			env["API_TOKEN"] = o.cfg.InternalToken
			env["INTERNAL_TOKEN"] = o.cfg.InternalToken
		}
		// Mirrors kubernetes_service.py:680-685: for a release (not debug)
		// deploy, an explicit BOT_TOKEN env wins; otherwise fall back to the
		// project's RELEASE_BOT_TOKEN (set on the project via its token field
		// or the set-bot-token flow), and only then to the shared test token.
		if _, ok := env["BOT_TOKEN"]; !ok && !spec.Debug {
			if releaseToken, ok := env["RELEASE_BOT_TOKEN"]; ok && releaseToken != "" {
				env["BOT_TOKEN"] = releaseToken
			}
		}
		if _, ok := env["BOT_TOKEN"]; !ok && o.cfg.TestBotToken != "" {
			env["BOT_TOKEN"] = o.cfg.TestBotToken
		}
		if spec.Debug {
			env["DEBUG_MODE"] = "true"
			if o.cfg.InternalToken != "" {
				env["DEBUG_EVENT_TOKEN"] = o.cfg.InternalToken
			}
		}
	}

	secretName := name + "-env"
	if err := o.ensureEnvSecret(ctx, secretName, env); err != nil {
		return DeployResult{}, fmt.Errorf("ensure env secret: %w", err)
	}

	deployment := buildDeployment(buildDeploymentInput{
		Namespace:      ns,
		Name:           name,
		Image:          image,
		Labels:         labels,
		SecretName:     secretName,
		Port:           int32(port),
		ReadinessPath:  readinessPath,
		LivenessPath:   livenessPath,
		VolumeMounts:   volumeMounts,
		Volumes:        volumes,
		RegistrySecret: o.cfg.RegistrySecretName,
	})

	if err := o.applyDeployment(ctx, deployment); err != nil {
		return DeployResult{}, fmt.Errorf("apply deployment %s: %w", name, err)
	}

	// Service: bot engines get "bot-<webhook uuid>" (stable across redeploys —
	// ingress/routing depends on this name, not the deployment name). Shared
	// module pods get "<name>" itself (see plugin.sharedEndpoint's DNS call).
	if spec.WebhookUUID != uuid.Nil {
		if err := o.applyService(ctx, "bot-"+spec.WebhookUUID.String(), name, labels, int32(port), int32(port)); err != nil {
			// Non-fatal: Django also swallowed Service creation errors and let
			// the Deployment stand (a manual Service can be created later).
			return DeployResult{ServiceID: name, Status: StatusInitializing}, nil //nolint:nilerr
		}
	} else if len(spec.Flow) == 0 {
		// Module deploy: Name was pre-set by the caller, so the Service name
		// equals the Deployment name.
		if err := o.applyService(ctx, name, name, labels, int32(port), int32(port)); err != nil {
			return DeployResult{ServiceID: name, Status: StatusInitializing}, nil //nolint:nilerr
		}
	}

	return DeployResult{ServiceID: name, Status: StatusInitializing}, nil
}

// ── Stop ────────────────────────────────────────────────────────────────

func (o *KubernetesOrchestrator) Stop(ctx context.Context, serviceID string) error {
	if serviceID == "" {
		return nil
	}
	if ref, ok := parsePoolRuntimeServiceID(serviceID); ok {
		return o.stopPoolRuntime(ctx, ref)
	}
	if ref, ok := parseUserRuntimeServiceID(serviceID); ok {
		return o.stopUserRuntime(ctx, ref)
	}
	ns := o.cfg.Namespace

	if err := ignoreNotFound(o.cs.AppsV1().Deployments(ns).Delete(ctx, serviceID, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete deployment %s: %w", serviceID, err)
	}
	if err := ignoreNotFound(o.cs.CoreV1().Secrets(ns).Delete(ctx, serviceID+"-env", metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete secret %s-env: %w", serviceID, err)
	}
	if err := ignoreNotFound(o.cs.CoreV1().Secrets(ns).Delete(ctx, serviceID+"-runtime", metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete secret %s-runtime: %w", serviceID, err)
	}
	if err := ignoreNotFound(o.cs.CoreV1().ConfigMaps(ns).Delete(ctx, serviceID+"-flow", metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete configmap %s-flow: %w", serviceID, err)
	}

	// The Service name doesn't derive from serviceID for bot engines
	// (it's "bot-<webhook uuid>"), so find it by the "deployment" label
	// instead of guessing — mirrors Django's remove_deployment fallback.
	svcs, err := o.cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "deployment=" + serviceID,
	})
	if err != nil {
		return fmt.Errorf("list services for %s: %w", serviceID, err)
	}
	for _, svc := range svcs.Items {
		if err := ignoreNotFound(o.cs.CoreV1().Services(ns).Delete(ctx, svc.Name, metav1.DeleteOptions{})); err != nil {
			return fmt.Errorf("delete service %s: %w", svc.Name, err)
		}
	}
	// Module deploys name the Service after the deployment itself — the
	// label selector above already covers it (applyService always stamps
	// the "deployment" label), so no extra lookup is needed here.
	return nil
}

// ── Status ──────────────────────────────────────────────────────────────

func (o *KubernetesOrchestrator) Status(ctx context.Context, serviceID string) (Status, error) {
	if serviceID == "" {
		return StatusStopped, nil
	}
	if ref, ok := parsePoolRuntimeServiceID(serviceID); ok {
		if o.rdb == nil {
			return StatusStopped, nil
		}
		workers := o.poolPlacement(ctx, ref.WebhookID.String())
		if len(workers) == 0 {
			return StatusStopped, nil
		}
		failed := 0
		for _, workerID := range workers {
			status, statusErr := o.rdb.HGet(ctx, poolWorkerStatusHash(workerID), ref.WebhookID.String()).Result()
			if statusErr == nil && status == "ready" {
				return StatusRunning, nil
			}
			if statusErr == nil && status == "failed" {
				failed++
			}
		}
		if failed == len(workers) {
			return StatusFailed, nil
		}
		return StatusInitializing, nil
	}
	var runtimeRef *userRuntimeRef
	if ref, ok := parseUserRuntimeServiceID(serviceID); ok {
		runtimeRef = &ref
		serviceID = ownerDeploymentName(ref.OwnerID)
	}
	dep, err := o.cs.AppsV1().Deployments(o.cfg.Namespace).Get(ctx, serviceID, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return StatusStopped, nil
	}
	if err != nil {
		return "", fmt.Errorf("get deployment %s: %w", serviceID, err)
	}
	if dep.Status.ReadyReplicas > 0 {
		if runtimeRef != nil && o.rdb != nil {
			runtimeStatus, statusErr := o.rdb.HGet(ctx, runtimeStatusKey(runtimeRef.OwnerID), runtimeRef.WebhookID.String()).Result()
			switch {
			case errors.Is(statusErr, redis.Nil), runtimeStatus == "loading":
				return StatusInitializing, nil
			case statusErr != nil:
				return "", fmt.Errorf("get runtime status: %w", statusErr)
			case runtimeStatus == "failed":
				return StatusFailed, nil
			case runtimeStatus != "ready":
				return StatusInitializing, nil
			}
		}
		return StatusRunning, nil
	}
	if dep.Status.Replicas > 0 {
		// Not ready yet — tell a genuinely-still-launching pod apart from one
		// stuck crash-looping (mirrors Django's cleanup_failed_releases
		// CRASH_RESTART_THRESHOLD=5, core/apps/api/tasks/release.py) so
		// ReconcileStatus can report "failed" instead of "initializing"
		// forever.
		if o.hasCrashLoopingPod(ctx, dep) {
			return StatusFailed, nil
		}
		return StatusInitializing, nil
	}
	return StatusStopped, nil
}

// ── Logs ────────────────────────────────────────────────────────────────

// Logs ports Django's deployment_logs_snapshot() (core/apps/api/services/
// kubernetes_service.py): find the pod backing serviceID via the same
// "engine-name=<serviceID>" label Deploy stamps on every pod template, then
// pull its trailing container log as a single non-streaming read (no
// watch/follow — this is a request/response HTTP endpoint, not a WebSocket).
func (o *KubernetesOrchestrator) Logs(ctx context.Context, serviceID string, tailLines, sinceSeconds int) (LogSnapshot, error) {
	if serviceID == "" {
		return LogSnapshot{}, nil
	}
	var runtimeLogPrefix string
	var specificPod string
	if ref, ok := parsePoolRuntimeServiceID(serviceID); ok {
		runtimeLogPrefix = "[" + ref.WebhookID.String() + "] "
		workers := o.poolPlacement(ctx, ref.WebhookID.String())
		if len(workers) > 0 {
			specificPod = workers[0]
		}
		serviceID = poolDeploymentName
	}
	if ref, ok := parseUserRuntimeServiceID(serviceID); ok {
		runtimeLogPrefix = "[" + ref.WebhookID.String() + "] "
		serviceID = ownerDeploymentName(ref.OwnerID)
	}
	tail := int64(tailLines)
	if tail <= 0 {
		tail = 200
	}
	if tail > 2000 {
		tail = 2000
	}

	pods, err := o.cs.CoreV1().Pods(o.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "engine-name=" + serviceID,
	})
	if err != nil {
		return LogSnapshot{}, fmt.Errorf("list pods for %s: %w", serviceID, err)
	}
	if len(pods.Items) == 0 {
		return LogSnapshot{}, nil
	}
	podName := pods.Items[0].Name
	if specificPod != "" {
		for _, pod := range pods.Items {
			if pod.Name == specificPod {
				podName = pod.Name
				break
			}
		}
	}

	opts := &corev1.PodLogOptions{TailLines: &tail, Timestamps: true}
	if sinceSeconds > 0 {
		since := int64(sinceSeconds)
		opts.SinceSeconds = &since
	}
	raw, err := o.cs.CoreV1().Pods(o.cfg.Namespace).GetLogs(podName, opts).Do(ctx).Raw()
	if err != nil {
		return LogSnapshot{PodName: podName}, fmt.Errorf("read pod logs %s: %w", podName, err)
	}

	lines := make([]string, 0)
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln != "" && (runtimeLogPrefix == "" || strings.Contains(ln, runtimeLogPrefix)) {
			lines = append(lines, ln)
		}
	}
	return LogSnapshot{PodName: podName, Lines: lines, Truncated: int64(len(lines)) >= tail}, nil
}

// crashRestartThreshold mirrors Django's CRASH_RESTART_THRESHOLD
// (core/apps/api/tasks/release.py).
const crashRestartThreshold = 5

// hasCrashLoopingPod reports whether any pod backing dep has a container
// that's restarted past the crash threshold or is currently waiting in
// CrashLoopBackOff. Best-effort: a list error just means "don't know",
// treated as not-crash-looping so a transient API hiccup can't flip a
// healthy-but-slow deploy to failed.
func (o *KubernetesOrchestrator) hasCrashLoopingPod(ctx context.Context, dep *appsv1.Deployment) bool {
	if dep.Spec.Selector == nil || len(dep.Spec.Selector.MatchLabels) == 0 {
		return false
	}
	sel := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: dep.Spec.Selector.MatchLabels})
	pods, err := o.cs.CoreV1().Pods(o.cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return false
	}
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.RestartCount >= crashRestartThreshold {
				return true
			}
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				return true
			}
		}
	}
	return false
}

// ── resource builders ──────────────────────────────────────────────────

func resolveDeploymentName(spec DeploySpec) string {
	if spec.Name != "" {
		return sanitizeName(spec.Name)
	}
	var raw string
	if spec.Debug {
		raw = "debug-" + spec.ProjectID.String()
	} else {
		raw = fmt.Sprintf("release-%s-%d", spec.ProjectID.String(), spec.Version)
	}
	return sanitizeName(raw)
}

func sanitizeName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", "-"))
}

// redisPrefix mirrors Django's f"{prefix}_{key}" REDIS_PREFIX, derived from
// the same components as the deployment name so per-bot Redis keys never
// collide.
func redisPrefix(spec DeploySpec, name string) string {
	if spec.Debug {
		return "debug_" + strings.TrimPrefix(name, "debug-")
	}
	return "release_" + strings.TrimPrefix(name, "release-")
}

type buildDeploymentInput struct {
	Namespace      string
	Name           string
	Image          string
	Labels         map[string]string
	SecretName     string
	Port           int32
	ReadinessPath  string
	LivenessPath   string
	VolumeMounts   []corev1.VolumeMount
	Volumes        []corev1.Volume
	RegistrySecret string
	Command        []string
}

// buildDeployment ports create_deployment (kubernetes_service.py): probes,
// RollingUpdate(maxUnavailable=0, maxSurge=1) for zero-downtime redeploys,
// imagePullSecrets, hostname/subdomain.
func buildDeployment(in buildDeploymentInput) *appsv1.Deployment {
	replicas := int32(1)
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(1)

	container := corev1.Container{
		Name:  "engine",
		Image: in.Image,
		EnvFrom: []corev1.EnvFromSource{
			{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: in.SecretName}}},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		},
		VolumeMounts: in.VolumeMounts,
		Command:      in.Command,
		// The "metrics" port exposes the engine's Prometheus /metrics endpoint
		// (bot_* counters) on 9090 — the engine's METRICS_PORT default, which
		// the bot pods don't override. Named so the PodMonitor
		// (k8s/helm/infra) can select it. Only pods (re)deployed after this
		// change carry it; older bots are scraped once they roll.
		Ports: []corev1.ContainerPort{
			{ContainerPort: in.Port, Protocol: corev1.ProtocolTCP},
			{Name: "metrics", ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: in.ReadinessPath, Port: intstr.FromInt32(in.Port)}},
			InitialDelaySeconds: 5, PeriodSeconds: 5, TimeoutSeconds: 3, FailureThreshold: 3, SuccessThreshold: 1,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: in.LivenessPath, Port: intstr.FromInt32(in.Port)}},
			InitialDelaySeconds: 15, PeriodSeconds: 15, TimeoutSeconds: 3, FailureThreshold: 3,
		},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: in.Name, Namespace: in.Namespace, Labels: in.Labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: in.Labels},
			Strategy: appsv1.DeploymentStrategy{
				Type:          appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{MaxUnavailable: &maxUnavailable, MaxSurge: &maxSurge},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: in.Labels,
					Annotations: map[string]string{
						"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
					},
				},
				Spec: corev1.PodSpec{
					Containers:                   []corev1.Container{container},
					RestartPolicy:                corev1.RestartPolicyAlways,
					AutomountServiceAccountToken: boolPtr(false),
					Volumes:                      in.Volumes,
					ImagePullSecrets:             []corev1.LocalObjectReference{{Name: in.RegistrySecret}},
					Hostname:                     in.Name,
					Subdomain:                    "bot-provider",
				},
			},
		},
	}
}

func boolPtr(value bool) *bool { return &value }

// ── k8s API helpers ─────────────────────────────────────────────────────

func (o *KubernetesOrchestrator) applyDeployment(ctx context.Context, dep *appsv1.Deployment) error {
	client := o.cs.AppsV1().Deployments(dep.Namespace)
	_, err := client.Create(ctx, dep, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, getErr := client.Get(ctx, dep.Name, metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	dep.ResourceVersion = existing.ResourceVersion
	_, err = client.Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

func (o *KubernetesOrchestrator) ensureConfigMap(ctx context.Context, name string, data map[string]string) error {
	client := o.cs.CoreV1().ConfigMaps(o.cfg.Namespace)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.cfg.Namespace},
		Data:       data,
	}
	_, err := client.Create(ctx, cm, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, getErr := client.Get(ctx, name, metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	cm.ResourceVersion = existing.ResourceVersion
	_, err = client.Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func (o *KubernetesOrchestrator) ensureEnvSecret(ctx context.Context, name string, data map[string]string) error {
	encoded := make(map[string][]byte, len(data))
	for k, v := range data {
		encoded[k] = []byte(v)
	}
	return o.ensureDataSecret(ctx, name, encoded)
}

func (o *KubernetesOrchestrator) ensureDataSecret(ctx context.Context, name string, data map[string][]byte) error {
	client := o.cs.CoreV1().Secrets(o.cfg.Namespace)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.cfg.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
	_, err := client.Create(ctx, secret, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, getErr := client.Get(ctx, name, metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	secret.ResourceVersion = existing.ResourceVersion
	_, err = client.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func (o *KubernetesOrchestrator) applyService(ctx context.Context, name, deploymentName string, selector map[string]string, port, targetPort int32) error {
	client := o.cs.CoreV1().Services(o.cfg.Namespace)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: o.cfg.Namespace,
			Labels:    map[string]string{"app": "botmother-engine", "deployment": deploymentName},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector,
			Ports:    []corev1.ServicePort{{Name: "http", Protocol: corev1.ProtocolTCP, Port: port, TargetPort: intstr.FromInt32(targetPort)}},
		},
	}
	_, err := client.Create(ctx, svc, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, getErr := client.Get(ctx, name, metav1.GetOptions{})
	if getErr != nil {
		return getErr
	}
	svc.ResourceVersion = existing.ResourceVersion
	svc.Spec.ClusterIP = existing.Spec.ClusterIP
	_, err = client.Update(ctx, svc, metav1.UpdateOptions{})
	return err
}

func ignoreNotFound(err error) error {
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
