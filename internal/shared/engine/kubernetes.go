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
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
}

func (c KubernetesConfig) image() string { return c.ImageRepository + ":" + c.ImageTag }

// KubernetesOrchestrator implements Orchestrator against a real cluster.
type KubernetesOrchestrator struct {
	cs  kubernetes.Interface
	cfg KubernetesConfig
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
	return &KubernetesOrchestrator{cs: cs, cfg: cfg}
}

// ── Deploy ──────────────────────────────────────────────────────────────

func (o *KubernetesOrchestrator) Deploy(ctx context.Context, spec DeploySpec) (DeployResult, error) {
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
		// TODO(engine): the old Django deploy also wrote schemas.json (CMS
		// collection field-kind map) and modules.json (enabled project
		// modules) into this same ConfigMap so the engine could coerce CMS
		// values and reach shared module RPC endpoints. DeploySpec does not
		// carry that data yet — wire it through release.Usecase once the Go
		// CMS/plugin usecases expose it, then set SCHEMAS_FILE_PATH /
		// MODULE_CONFIG_PATH below alongside FLOW_FILE_PATH.
		if err := o.ensureConfigMap(ctx, cmName, map[string]string{"flow.json": string(spec.Flow)}); err != nil {
			return DeployResult{}, fmt.Errorf("ensure flow configmap: %w", err)
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "flow-volume", MountPath: "/app/assets", ReadOnly: true})
		volumes = append(volumes, corev1.Volume{
			Name: "flow-volume",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: cmName}},
			},
		})

		env["FLOW_FILE_PATH"] = "/app/assets/flow.json"
		env["WEBHOOK_ENABLED"] = "true"
		env["WEBHOOK_PORT"] = strconv.Itoa(port)
		if o.cfg.RedisAddr != "" {
			env["REDIS_ENABLED"] = "true"
			env["REDIS_ADDR"] = o.cfg.RedisAddr
			env["REDIS_PASSWORD"] = o.cfg.RedisPassword
			env["REDIS_PREFIX"] = redisPrefix(spec, name)
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
		if err := o.applyService(ctx, "bot-"+spec.WebhookUUID.String(), name, labels, int32(port)); err != nil {
			// Non-fatal: Django also swallowed Service creation errors and let
			// the Deployment stand (a manual Service can be created later).
			return DeployResult{ServiceID: name, Status: StatusInitializing}, nil //nolint:nilerr
		}
	} else if len(spec.Flow) == 0 {
		// Module deploy: Name was pre-set by the caller, so the Service name
		// equals the Deployment name.
		if err := o.applyService(ctx, name, name, labels, int32(port)); err != nil {
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
	ns := o.cfg.Namespace

	if err := ignoreNotFound(o.cs.AppsV1().Deployments(ns).Delete(ctx, serviceID, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete deployment %s: %w", serviceID, err)
	}
	if err := ignoreNotFound(o.cs.CoreV1().Secrets(ns).Delete(ctx, serviceID+"-env", metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("delete secret %s-env: %w", serviceID, err)
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
	dep, err := o.cs.AppsV1().Deployments(o.cfg.Namespace).Get(ctx, serviceID, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return StatusStopped, nil
	}
	if err != nil {
		return "", fmt.Errorf("get deployment %s: %w", serviceID, err)
	}
	if dep.Status.ReadyReplicas > 0 {
		return StatusRunning, nil
	}
	if dep.Status.Replicas > 0 {
		return StatusInitializing, nil
	}
	return StatusStopped, nil
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
}

// buildDeployment ports create_deployment (kubernetes_service.py): same
// resource limits, probes, RollingUpdate(maxUnavailable=0, maxSurge=1) for
// zero-downtime redeploys, imagePullSecrets, hostname/subdomain.
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
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		VolumeMounts: in.VolumeMounts,
		Ports:        []corev1.ContainerPort{{ContainerPort: in.Port, Protocol: corev1.ProtocolTCP}},
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
					Containers:       []corev1.Container{container},
					RestartPolicy:    corev1.RestartPolicyAlways,
					Volumes:          in.Volumes,
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: in.RegistrySecret}},
					Hostname:         in.Name,
					Subdomain:        "bot-provider",
				},
			},
		},
	}
}

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
	client := o.cs.CoreV1().Secrets(o.cfg.Namespace)
	encoded := make(map[string][]byte, len(data))
	for k, v := range data {
		encoded[k] = []byte(v)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.cfg.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       encoded,
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

func (o *KubernetesOrchestrator) applyService(ctx context.Context, name, deploymentName string, selector map[string]string, port int32) error {
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
			Ports:    []corev1.ServicePort{{Name: "http", Protocol: corev1.ProtocolTCP, Port: port, TargetPort: intstr.FromInt32(port)}},
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
