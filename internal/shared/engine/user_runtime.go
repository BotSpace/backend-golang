package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	userRuntimeServicePrefix = "user-runtime/"
	minRuntimeRoutePort      = 10000
	maxRuntimeRoutePort      = 59999
)

type userRuntimeRef struct {
	OwnerID   uuid.UUID
	WebhookID uuid.UUID
}

type storedRuntimeSpec struct {
	WebhookID string            `json:"webhook_id"`
	ProjectID string            `json:"project_id"`
	Name      string            `json:"name"`
	Debug     bool              `json:"debug"`
	RoutePort int               `json:"route_port"`
	Flow      json.RawMessage   `json:"flow"`
	Schemas   json.RawMessage   `json:"schemas"`
	Modules   json.RawMessage   `json:"modules"`
	Env       map[string]string `json:"env"`
}

func ownerDeploymentName(ownerID uuid.UUID) string { return "user-engine-" + ownerID.String() }
func runtimeHashKey(ownerID uuid.UUID) string      { return "engine:user:" + ownerID.String() + ":runtimes" }
func runtimePortKey(ownerID uuid.UUID) string      { return "engine:user:" + ownerID.String() + ":ports" }
func runtimeStatusKey(ownerID uuid.UUID) string    { return "engine:user:" + ownerID.String() + ":status" }
func runtimeCommandStream(ownerID uuid.UUID) string {
	return "engine:user:" + ownerID.String() + ":commands"
}

func userRuntimeServiceID(ownerID, webhookID uuid.UUID) string {
	return userRuntimeServicePrefix + ownerID.String() + "/" + webhookID.String()
}

func parseUserRuntimeServiceID(value string) (userRuntimeRef, bool) {
	if !strings.HasPrefix(value, userRuntimeServicePrefix) {
		return userRuntimeRef{}, false
	}
	parts := strings.Split(strings.TrimPrefix(value, userRuntimeServicePrefix), "/")
	if len(parts) != 2 {
		return userRuntimeRef{}, false
	}
	ownerID, err := uuid.Parse(parts[0])
	if err != nil {
		return userRuntimeRef{}, false
	}
	webhookID, err := uuid.Parse(parts[1])
	if err != nil {
		return userRuntimeRef{}, false
	}
	return userRuntimeRef{OwnerID: ownerID, WebhookID: webhookID}, true
}

// EnsureOwnerRuntime creates the empty owner supervisor once. Repeated calls
// do not rewrite the Deployment, so creating/editing bots never restarts it.
func (o *KubernetesOrchestrator) EnsureOwnerRuntime(ctx context.Context, ownerID uuid.UUID) (string, error) {
	if ownerID == uuid.Nil {
		return "", errors.New("owner id is required")
	}
	name := ownerDeploymentName(ownerID)
	ns := o.cfg.Namespace
	existing, getErr := o.cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return "", fmt.Errorf("get owner deployment: %w", getErr)
	}
	if getErr == nil && len(existing.Spec.Template.Spec.Containers) > 0 && existing.Spec.Template.Spec.Containers[0].Image == o.cfg.image() {
		return name, nil
	}

	env := map[string]string{
		"USER_ID": ownerID.String(), "REDIS_ENABLED": "true", "REDIS_ADDR": o.cfg.RedisAddr,
		"REDIS_PASSWORD": o.cfg.RedisPassword, "LOG_LEVEL": "info", "BOT_RUNTIME_PATH": "/app/bot",
	}
	if o.cfg.APIURL != "" {
		env["API_URL"] = o.cfg.APIURL
	}
	if o.cfg.InternalToken != "" {
		env["API_TOKEN"] = o.cfg.InternalToken
		env["INTERNAL_TOKEN"] = o.cfg.InternalToken
	}
	if o.cfg.MongoAddr != "" {
		env["MONGO_ADDR"] = o.cfg.MongoAddr
		env["MONGO_DB_NAME"] = o.cfg.MongoDBName
	}
	secretName := name + "-env"
	if err := o.ensureEnvSecret(ctx, secretName, env); err != nil {
		return "", fmt.Errorf("ensure owner env: %w", err)
	}
	labels := map[string]string{"app": "botmother-user-engine", "engine-name": name, "owner_id": ownerID.String()}
	dep := buildDeployment(buildDeploymentInput{Namespace: ns, Name: name, Image: o.cfg.image(), Labels: labels, SecretName: secretName, Port: 8000, ReadinessPath: "/ready", LivenessPath: "/health", RegistrySecret: o.cfg.RegistrySecretName, Command: []string{"/app/supervisor"}})
	dep.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	// This annotation is meaningful for legacy bot redeploys but would cause an
	// owner pod restart merely because Ensure raced. Keep owner creation stable.
	delete(dep.Spec.Template.Annotations, "kubectl.kubernetes.io/restartedAt")
	if err := o.applyDeployment(ctx, dep); err != nil {
		return "", fmt.Errorf("create owner deployment: %w", err)
	}
	return name, nil
}

func (o *KubernetesOrchestrator) deployUserRuntime(ctx context.Context, spec DeploySpec) (DeployResult, error) {
	if spec.WebhookUUID == uuid.Nil {
		return DeployResult{}, errors.New("webhook uuid is required for user runtime")
	}
	if o.rdb == nil {
		return DeployResult{}, errors.New("redis is required for user runtime")
	}
	name, err := o.EnsureOwnerRuntime(ctx, spec.OwnerID)
	if err != nil {
		return DeployResult{}, err
	}
	port, err := o.allocateRuntimePort(ctx, spec.OwnerID, spec.WebhookUUID)
	if err != nil {
		return DeployResult{}, err
	}

	env := make(map[string]string, len(spec.Env)+16)
	for key, value := range spec.Env {
		env[key] = value
	}
	env["PROJECT_ID"] = spec.ProjectID.String()
	env["REDIS_ENABLED"] = "true"
	env["REDIS_ADDR"] = o.cfg.RedisAddr
	env["REDIS_PASSWORD"] = o.cfg.RedisPassword
	env["REDIS_PREFIX"] = redisPrefix(spec, resolveDeploymentName(spec))
	env["BACKEND_EVENTS_STREAM"] = "true"
	if o.cfg.APIURL != "" {
		env["API_URL"] = o.cfg.APIURL
	}
	if o.cfg.InternalToken != "" {
		env["API_TOKEN"] = o.cfg.InternalToken
		env["INTERNAL_TOKEN"] = o.cfg.InternalToken
	}
	if o.cfg.MongoAddr != "" {
		env["MONGO_ADDR"] = o.cfg.MongoAddr
		env["MONGO_DB_NAME"] = o.cfg.MongoDBName
	}
	if !spec.Debug && env["BOT_TOKEN"] == "" {
		env["BOT_TOKEN"] = env["RELEASE_BOT_TOKEN"]
	}
	if env["BOT_TOKEN"] == "" {
		env["BOT_TOKEN"] = o.cfg.TestBotToken
	}
	if spec.Debug {
		env["DEBUG_MODE"] = "true"
		env["DEBUG_EVENT_TOKEN"] = o.cfg.InternalToken
	} else {
		env["IS_RELEASE"] = "true"
	}

	runtime := storedRuntimeSpec{WebhookID: spec.WebhookUUID.String(), ProjectID: spec.ProjectID.String(), Name: resolveDeploymentName(spec), Debug: spec.Debug, RoutePort: port, Flow: normalizeJSONObject(spec.Flow, `{"nodes":[],"edges":[]}`), Schemas: normalizeJSONObject(spec.Schemas, `{}`), Modules: normalizeJSONObject(spec.Modules, `{"modules":[]}`), Env: env}
	raw, err := json.Marshal(runtime)
	if err != nil {
		return DeployResult{}, err
	}
	if err := o.rdb.HSet(ctx, runtimeHashKey(spec.OwnerID), spec.WebhookUUID.String(), raw).Err(); err != nil {
		return DeployResult{}, fmt.Errorf("store runtime: %w", err)
	}
	_ = o.rdb.HSet(ctx, runtimeStatusKey(spec.OwnerID), spec.WebhookUUID.String(), "loading").Err()

	labels := map[string]string{"app": "botmother-user-engine", "engine-name": name, "owner_id": spec.OwnerID.String()}
	if err := o.applyService(ctx, "bot-"+spec.WebhookUUID.String(), name, labels, 8000, int32(port)); err != nil {
		_ = o.rdb.HDel(ctx, runtimeHashKey(spec.OwnerID), spec.WebhookUUID.String()).Err()
		return DeployResult{}, fmt.Errorf("route runtime: %w", err)
	}
	if err := o.publishRuntimeCommand(ctx, spec.OwnerID, "upsert", spec.WebhookUUID); err != nil {
		return DeployResult{}, err
	}
	return DeployResult{ServiceID: userRuntimeServiceID(spec.OwnerID, spec.WebhookUUID), Status: StatusInitializing}, nil
}

func (o *KubernetesOrchestrator) stopUserRuntime(ctx context.Context, ref userRuntimeRef) error {
	if o.rdb != nil {
		if err := o.rdb.HDel(ctx, runtimeHashKey(ref.OwnerID), ref.WebhookID.String()).Err(); err != nil {
			return fmt.Errorf("remove runtime: %w", err)
		}
		_ = o.publishRuntimeCommand(ctx, ref.OwnerID, "remove", ref.WebhookID)
		_ = o.rdb.HDel(ctx, runtimeStatusKey(ref.OwnerID), ref.WebhookID.String()).Err()
	}
	return ignoreNotFound(o.cs.CoreV1().Services(o.cfg.Namespace).Delete(ctx, "bot-"+ref.WebhookID.String(), metav1.DeleteOptions{}))
}

func (o *KubernetesOrchestrator) publishRuntimeCommand(ctx context.Context, ownerID uuid.UUID, action string, webhookID uuid.UUID) error {
	if o.rdb == nil {
		return errors.New("redis unavailable")
	}
	return o.rdb.XAdd(ctx, &redis.XAddArgs{Stream: runtimeCommandStream(ownerID), MaxLen: 1000, Approx: true, Values: map[string]any{"action": action, "webhook_id": webhookID.String(), "ts": time.Now().UnixMilli()}}).Err()
}

func (o *KubernetesOrchestrator) allocateRuntimePort(ctx context.Context, ownerID, webhookID uuid.UUID) (int, error) {
	if raw, err := o.rdb.HGet(ctx, runtimeHashKey(ownerID), webhookID.String()).Result(); err == nil {
		var existing storedRuntimeSpec
		if json.Unmarshal([]byte(raw), &existing) == nil && existing.RoutePort >= minRuntimeRoutePort {
			return existing.RoutePort, nil
		}
	}
	span := maxRuntimeRoutePort - minRuntimeRoutePort + 1
	start := int(crc32.ChecksumIEEE(webhookID[:])) % span
	for offset := 0; offset < span; offset++ {
		port := minRuntimeRoutePort + (start+offset)%span
		ok, err := o.rdb.HSetNX(ctx, runtimePortKey(ownerID), strconv.Itoa(port), webhookID.String()).Result()
		if err != nil {
			return 0, fmt.Errorf("reserve runtime port: %w", err)
		}
		if ok {
			return port, nil
		}
		owner, err := o.rdb.HGet(ctx, runtimePortKey(ownerID), strconv.Itoa(port)).Result()
		if err == nil && owner == webhookID.String() {
			return port, nil
		}
	}
	return 0, errors.New("no runtime route ports available")
}

func normalizeJSONObject(raw []byte, fallback string) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' {
		return json.RawMessage(fallback)
	}
	return append(json.RawMessage(nil), raw...)
}
