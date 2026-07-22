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
	Replicas  int               `json:"replicas,omitempty"`
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
func (o *KubernetesOrchestrator) ensureLegacyOwnerRuntime(ctx context.Context, ownerID uuid.UUID) (string, error) {
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

func (o *KubernetesOrchestrator) deployOwnerRuntime(ctx context.Context, spec DeploySpec) (DeployResult, error) {
	if spec.WebhookUUID == uuid.Nil {
		return DeployResult{}, errors.New("webhook uuid is required for user runtime")
	}
	if o.rdb == nil {
		return DeployResult{}, errors.New("redis is required for user runtime")
	}
	name, err := o.ensureLegacyOwnerRuntime(ctx, spec.OwnerID)
	if err != nil {
		return DeployResult{}, err
	}
	port, err := o.allocateRuntimePort(ctx, spec.OwnerID, spec.WebhookUUID)
	if err != nil {
		return DeployResult{}, err
	}

	runtimeProjectID := spec.ProjectID.String()
	if strings.TrimSpace(spec.RuntimeProjectID) != "" {
		runtimeProjectID = strings.TrimSpace(spec.RuntimeProjectID)
	}
	env := make(map[string]string, len(spec.Env)+16)
	for key, value := range spec.Env {
		env[key] = value
	}
	env["PROJECT_ID"] = runtimeProjectID
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

	runtime := storedRuntimeSpec{WebhookID: spec.WebhookUUID.String(), ProjectID: runtimeProjectID, Name: resolveDeploymentName(spec), Debug: spec.Debug, RoutePort: port, Flow: normalizeJSONObject(spec.Flow, `{"nodes":[],"edges":[]}`), Schemas: normalizeJSONObject(spec.Schemas, `{}`), Modules: normalizeJSONObject(spec.Modules, `{"modules":[]}`), Env: env}
	raw, err := json.Marshal(runtime)
	if err != nil {
		return DeployResult{}, err
	}
	if err := o.rdb.HSet(ctx, runtimeHashKey(spec.OwnerID), spec.WebhookUUID.String(), raw).Err(); err != nil {
		return DeployResult{}, fmt.Errorf("store runtime: %w", err)
	}
	_ = o.rdb.HSet(ctx, runtimeStatusKey(spec.OwnerID), spec.WebhookUUID.String(), "loading").Err()
	if err := o.publishRuntimeCommand(ctx, spec.OwnerID, "upsert", spec.WebhookUUID); err != nil {
		return DeployResult{}, err
	}
	// Do not switch the stable bot Service while the new child is still
	// booting. This preserves the legacy/runtime currently serving traffic and
	// makes both migrations and ordinary hot reloads an atomic cutover.
	if err := o.waitRuntimeReady(ctx, spec.OwnerID, spec.WebhookUUID, 60*time.Second); err != nil {
		_ = o.rdb.HDel(ctx, runtimeHashKey(spec.OwnerID), spec.WebhookUUID.String()).Err()
		_ = o.rdb.HDel(ctx, runtimeStatusKey(spec.OwnerID), spec.WebhookUUID.String()).Err()
		_ = o.publishRuntimeCommand(ctx, spec.OwnerID, "remove", spec.WebhookUUID)
		return DeployResult{}, err
	}

	labels := map[string]string{"app": "botmother-user-engine", "engine-name": name, "owner_id": spec.OwnerID.String()}
	if err := o.applyService(ctx, "bot-"+spec.WebhookUUID.String(), name, labels, 8000, int32(port)); err != nil {
		_ = o.rdb.HDel(ctx, runtimeHashKey(spec.OwnerID), spec.WebhookUUID.String()).Err()
		return DeployResult{}, fmt.Errorf("route runtime: %w", err)
	}
	return DeployResult{ServiceID: userRuntimeServiceID(spec.OwnerID, spec.WebhookUUID), Status: StatusInitializing}, nil
}

func (o *KubernetesOrchestrator) waitRuntimeReady(ctx context.Context, ownerID, webhookID uuid.UUID, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := o.rdb.HGet(ctx, runtimeStatusKey(ownerID), webhookID.String()).Result()
		if err == nil && status == "ready" {
			return nil
		}
		if err == nil && status == "failed" {
			return errors.New("runtime failed readiness")
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("read runtime readiness: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("runtime readiness timeout")
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

// MigrateLegacyRuntimes converts active UUID-based release/debug Deployments
// in place. The old pod keeps serving until the owner supervisor reports its
// child ready; then the stable bot Service is switched, the DB service id is
// replaced, and only finally are the legacy pod assets removed.
func (o *KubernetesOrchestrator) MigrateLegacyRuntimes(
	ctx context.Context,
	ownerForService func(context.Context, uuid.UUID, string, bool) (uuid.UUID, bool, error),
	replaceServiceID func(context.Context, uuid.UUID, string, string, bool) (bool, error),
) (int, error) {
	if o.rdb == nil {
		return 0, errors.New("redis is required for migration")
	}
	locked, err := o.rdb.SetNX(ctx, "engine:migration:shared-pool:v1", time.Now().UnixMilli(), 30*time.Minute).Result()
	if err != nil {
		return 0, err
	}
	if !locked {
		return 0, nil
	}
	defer o.rdb.Del(context.Background(), "engine:migration:shared-pool:v1")

	deps, err := o.cs.AppsV1().Deployments(o.cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app=botmother-engine"})
	if err != nil {
		return 0, fmt.Errorf("list legacy runtimes: %w", err)
	}
	migrated := 0
	var migrationErrors []error
	for i := range deps.Items {
		dep := &deps.Items[i]
		debug := strings.HasPrefix(dep.Name, "debug-")
		if !debug && !strings.HasPrefix(dep.Name, "release-") {
			continue
		}
		projectID, parseErr := uuid.Parse(dep.Labels["project_id"])
		if parseErr != nil {
			continue
		} // Django-era numeric project; owned by the legacy backend.
		ownerID, active, ownerErr := ownerForService(ctx, projectID, dep.Name, debug)
		if ownerErr != nil || !active {
			continue
		}
		webhookID, hookErr := o.legacyWebhookID(ctx, dep.Name)
		if hookErr != nil {
			migrationErrors = append(migrationErrors, hookErr)
			continue
		}
		flow, schemas, modules, env, assetErr := o.legacyRuntimeAssets(ctx, dep.Name)
		if assetErr != nil {
			migrationErrors = append(migrationErrors, assetErr)
			continue
		}

		result, deployErr := o.deployUserRuntime(ctx, DeploySpec{OwnerID: ownerID, ProjectID: projectID, Debug: debug, Name: dep.Name, Flow: flow, Schemas: schemas, Modules: modules, Env: env, WebhookUUID: webhookID})
		if deployErr != nil {
			migrationErrors = append(migrationErrors, fmt.Errorf("migrate %s: %w", dep.Name, deployErr))
			continue
		}
		updated, updateErr := replaceServiceID(ctx, projectID, dep.Name, result.ServiceID, debug)
		if updateErr != nil || !updated {
			// DB no longer considers this runtime active. Restore its old route;
			// the ordinary orphan sweep may remove it later.
			_ = o.stopPoolRuntime(ctx, poolRuntimeRef{WebhookID: webhookID})
			if updateErr != nil {
				migrationErrors = append(migrationErrors, fmt.Errorf("update %s service id: %w", dep.Name, updateErr))
			}
			continue
		}
		if deleteErr := o.deleteLegacyRuntimeAssets(ctx, dep.Name); deleteErr != nil {
			migrationErrors = append(migrationErrors, deleteErr)
			continue
		}
		migrated++
	}

	// The previous architecture already consolidated some bots into one pod
	// per owner. Move those Redis-backed runtimes into the shared pool too,
	// then delete an owner pod only after every one of its children cut over.
	var cursor uint64
	for {
		keys, next, scanErr := o.rdb.Scan(ctx, cursor, "engine:user:*:runtimes", 100).Result()
		if scanErr != nil {
			migrationErrors = append(migrationErrors, scanErr)
			break
		}
		for _, key := range keys {
			parts := strings.Split(key, ":")
			if len(parts) != 4 {
				continue
			}
			ownerID, ownerErr := uuid.Parse(parts[2])
			if ownerErr != nil {
				continue
			}
			items, itemsErr := o.rdb.HGetAll(ctx, key).Result()
			if itemsErr != nil {
				migrationErrors = append(migrationErrors, itemsErr)
				continue
			}
			ownerComplete := true
			for webhookRaw, raw := range items {
				webhookID, hookErr := uuid.Parse(webhookRaw)
				var stored storedRuntimeSpec
				if hookErr != nil || json.Unmarshal([]byte(raw), &stored) != nil {
					ownerComplete = false
					continue
				}
				projectID, projectErr := uuid.Parse(stored.ProjectID)
				if projectErr != nil {
					ownerComplete = false
					migrationErrors = append(migrationErrors, fmt.Errorf("owner runtime %s has non-uuid canonical project %q", webhookID, stored.ProjectID))
					continue
				}
				result, deployErr := o.deployPoolRuntime(ctx, DeploySpec{OwnerID: ownerID, ProjectID: projectID, RuntimeProjectID: stored.ProjectID, Debug: stored.Debug, Name: stored.Name, Flow: stored.Flow, Schemas: stored.Schemas, Modules: stored.Modules, Env: stored.Env, WebhookUUID: webhookID})
				if deployErr != nil {
					ownerComplete = false
					migrationErrors = append(migrationErrors, fmt.Errorf("migrate owner runtime %s: %w", webhookID, deployErr))
					continue
				}
				oldID := userRuntimeServiceID(ownerID, webhookID)
				updated, updateErr := replaceServiceID(ctx, projectID, oldID, result.ServiceID, stored.Debug)
				if updateErr != nil || !updated {
					ownerComplete = false
					_ = o.stopPoolRuntime(ctx, poolRuntimeRef{WebhookID: webhookID})
					if updateErr != nil {
						migrationErrors = append(migrationErrors, updateErr)
					}
					continue
				}
				_ = o.rdb.HDel(ctx, runtimeHashKey(ownerID), webhookRaw).Err()
				_ = o.rdb.HDel(ctx, runtimeStatusKey(ownerID), webhookRaw).Err()
				_ = o.publishRuntimeCommand(ctx, ownerID, "remove", webhookID)
				migrated++
			}
			if ownerComplete {
				remaining, _ := o.rdb.HLen(ctx, key).Result()
				if remaining == 0 {
					name := ownerDeploymentName(ownerID)
					_ = ignoreNotFound(o.cs.AppsV1().Deployments(o.cfg.Namespace).Delete(ctx, name, metav1.DeleteOptions{}))
					_ = ignoreNotFound(o.cs.CoreV1().Secrets(o.cfg.Namespace).Delete(ctx, name+"-env", metav1.DeleteOptions{}))
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return migrated, errors.Join(migrationErrors...)
}

func (o *KubernetesOrchestrator) legacyWebhookID(ctx context.Context, deploymentName string) (uuid.UUID, error) {
	services, err := o.cs.CoreV1().Services(o.cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "deployment=" + deploymentName})
	if err != nil {
		return uuid.Nil, err
	}
	for _, service := range services.Items {
		if strings.HasPrefix(service.Name, "bot-") {
			if id, parseErr := uuid.Parse(strings.TrimPrefix(service.Name, "bot-")); parseErr == nil {
				return id, nil
			}
		}
	}
	return uuid.Nil, fmt.Errorf("legacy runtime %s has no bot service", deploymentName)
}

func (o *KubernetesOrchestrator) legacyRuntimeAssets(ctx context.Context, name string) ([]byte, []byte, []byte, map[string]string, error) {
	cm, err := o.cs.CoreV1().ConfigMaps(o.cfg.Namespace).Get(ctx, name+"-flow", metav1.GetOptions{})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get %s flow: %w", name, err)
	}
	envSecret, err := o.cs.CoreV1().Secrets(o.cfg.Namespace).Get(ctx, name+"-env", metav1.GetOptions{})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("get %s env: %w", name, err)
	}
	env := make(map[string]string, len(envSecret.Data))
	for key, value := range envSecret.Data {
		env[key] = string(value)
	}
	modules := []byte(`{"modules":[]}`)
	if runtimeSecret, runtimeErr := o.cs.CoreV1().Secrets(o.cfg.Namespace).Get(ctx, name+"-runtime", metav1.GetOptions{}); runtimeErr == nil {
		if value := runtimeSecret.Data["modules.json"]; len(value) > 0 {
			modules = value
		}
	}
	return []byte(cm.Data["flow.json"]), []byte(cm.Data["schemas.json"]), modules, env, nil
}

func (o *KubernetesOrchestrator) deleteLegacyRuntimeAssets(ctx context.Context, name string) error {
	ns := o.cfg.Namespace
	if err := ignoreNotFound(o.cs.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
		return err
	}
	if err := ignoreNotFound(o.cs.CoreV1().Secrets(ns).Delete(ctx, name+"-env", metav1.DeleteOptions{})); err != nil {
		return err
	}
	if err := ignoreNotFound(o.cs.CoreV1().Secrets(ns).Delete(ctx, name+"-runtime", metav1.DeleteOptions{})); err != nil {
		return err
	}
	return ignoreNotFound(o.cs.CoreV1().ConfigMaps(ns).Delete(ctx, name+"-flow", metav1.DeleteOptions{}))
}
