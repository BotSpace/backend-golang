package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	poolDeploymentName     = "engine-pool"
	poolRuntimePrefix      = "pool-runtime/"
	poolDesiredHash        = "engine:pool:runtimes"
	poolPlacementHash      = "engine:pool:placements"
	poolWorkersZSet        = "engine:pool:workers"
	poolPortsHash          = "engine:pool:ports"
	poolWorkerRuntimeBase  = "engine:pool:worker:"
	poolHeartbeatFreshness = 15 * time.Second
)

type poolRuntimeRef struct{ WebhookID uuid.UUID }

func poolRuntimeServiceID(webhookID uuid.UUID) string { return poolRuntimePrefix + webhookID.String() }

func parsePoolRuntimeServiceID(value string) (poolRuntimeRef, bool) {
	if !strings.HasPrefix(value, poolRuntimePrefix) {
		return poolRuntimeRef{}, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(value, poolRuntimePrefix))
	return poolRuntimeRef{WebhookID: id}, err == nil
}

func poolWorkerRuntimeHash(workerID string) string {
	return poolWorkerRuntimeBase + workerID + ":runtimes"
}
func poolWorkerStatusHash(workerID string) string {
	return poolWorkerRuntimeBase + workerID + ":status"
}
func poolWorkerCommandStream(workerID string) string {
	return poolWorkerRuntimeBase + workerID + ":commands"
}

// EnsureOwnerRuntime now means "ensure shared runtime capacity". ownerID is
// intentionally ignored: creating a user or bot never creates a dedicated pod.
func (o *KubernetesOrchestrator) EnsureOwnerRuntime(ctx context.Context, _ uuid.UUID) (string, error) {
	return o.EnsureEnginePool(ctx)
}

func (o *KubernetesOrchestrator) EnsureEnginePool(ctx context.Context) (string, error) {
	if o.rdb == nil {
		return "", errors.New("redis is required for engine pool")
	}
	env := map[string]string{
		"REDIS_ENABLED": "true", "REDIS_ADDR": o.cfg.RedisAddr,
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
	secretName := poolDeploymentName + "-env"
	if err := o.ensureEnvSecret(ctx, secretName, env); err != nil {
		return "", fmt.Errorf("ensure pool env: %w", err)
	}
	labels := map[string]string{"app": "botmother-engine-pool", "engine-name": poolDeploymentName}
	dep := buildDeployment(buildDeploymentInput{Namespace: o.cfg.Namespace, Name: poolDeploymentName, Image: o.cfg.image(), Labels: labels, SecretName: secretName, Port: 8000, ReadinessPath: "/ready", LivenessPath: "/health", RegistrySecret: o.cfg.RegistrySecretName, Command: []string{"/app/supervisor"}})
	replicas := int32(o.cfg.PoolSize)
	dep.Spec.Replicas = &replicas
	dep.Spec.Template.Spec.Hostname = ""
	dep.Spec.Template.Spec.Subdomain = ""
	dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env,
		corev1.EnvVar{Name: "POOL_WORKER_ID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		corev1.EnvVar{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}},
	)
	dep.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi")},
	}
	delete(dep.Spec.Template.Annotations, "kubectl.kubernetes.io/restartedAt")
	if err := o.applyDeployment(ctx, dep); err != nil {
		return "", fmt.Errorf("ensure engine pool deployment: %w", err)
	}
	if err := o.applyService(ctx, poolDeploymentName, poolDeploymentName, labels, 8000, 8000); err != nil {
		return "", fmt.Errorf("ensure engine pool service: %w", err)
	}
	return poolDeploymentName, nil
}

func (o *KubernetesOrchestrator) deployUserRuntime(ctx context.Context, spec DeploySpec) (DeployResult, error) {
	return o.deployPoolRuntime(ctx, spec)
}

func (o *KubernetesOrchestrator) deployPoolRuntime(ctx context.Context, spec DeploySpec) (DeployResult, error) {
	if spec.WebhookUUID == uuid.Nil {
		return DeployResult{}, errors.New("webhook uuid is required for pool runtime")
	}
	if _, err := o.EnsureEnginePool(ctx); err != nil {
		return DeployResult{}, err
	}
	workers, err := o.waitPoolWorkers(ctx, 60*time.Second)
	if err != nil {
		return DeployResult{}, err
	}
	replication := spec.RuntimeReplicas
	if replication < 1 {
		replication = o.cfg.RuntimeReplicas
	}
	if replication > len(workers) {
		replication = len(workers)
	}
	targets := rendezvousWorkers(spec.WebhookUUID.String(), workers, replication)
	port, err := o.allocatePoolPort(ctx, spec.WebhookUUID)
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
	webhookID := spec.WebhookUUID.String()
	if err := o.rdb.HSet(ctx, poolDesiredHash, webhookID, raw).Err(); err != nil {
		return DeployResult{}, fmt.Errorf("store desired runtime: %w", err)
	}
	previous := o.poolPlacement(ctx, webhookID)
	for replicaIndex, workerID := range targets {
		workerRuntime := runtime
		workerRuntime.Env = make(map[string]string, len(runtime.Env)+2)
		for key, value := range runtime.Env {
			workerRuntime.Env[key] = value
		}
		workerRuntime.Env["RUNTIME_REPLICA_INDEX"] = strconv.Itoa(replicaIndex)
		workerRuntime.Env["RUNTIME_PRIMARY"] = strconv.FormatBool(replicaIndex == 0)
		workerRaw, marshalErr := json.Marshal(workerRuntime)
		if marshalErr != nil {
			return DeployResult{}, marshalErr
		}
		pipe := o.rdb.Pipeline()
		pipe.HSet(ctx, poolWorkerRuntimeHash(workerID), webhookID, workerRaw)
		pipe.HSet(ctx, poolWorkerStatusHash(workerID), webhookID, "loading")
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: poolWorkerCommandStream(workerID), MaxLen: 1000, Approx: true, Values: map[string]any{"action": "upsert", "webhook_id": webhookID, "ts": time.Now().UnixMilli()}})
		if _, err := pipe.Exec(ctx); err != nil {
			return DeployResult{}, fmt.Errorf("assign runtime to %s: %w", workerID, err)
		}
	}
	if err := o.waitPoolRuntimeReady(ctx, webhookID, targets, 60*time.Second); err != nil {
		return DeployResult{}, err
	}
	placementRaw, _ := json.Marshal(targets)
	if err := o.rdb.HSet(ctx, poolPlacementHash, webhookID, placementRaw).Err(); err != nil {
		return DeployResult{}, fmt.Errorf("publish placement: %w", err)
	}
	for _, workerID := range difference(previous, targets) {
		o.removePoolWorkerRuntime(ctx, workerID, webhookID)
	}
	return DeployResult{ServiceID: poolRuntimeServiceID(spec.WebhookUUID), Status: StatusInitializing}, nil
}

func (o *KubernetesOrchestrator) stopPoolRuntime(ctx context.Context, ref poolRuntimeRef) error {
	webhookID := ref.WebhookID.String()
	workers := o.poolPlacement(ctx, webhookID)
	for _, workerID := range workers {
		o.removePoolWorkerRuntime(ctx, workerID, webhookID)
	}
	pipe := o.rdb.Pipeline()
	pipe.HDel(ctx, poolDesiredHash, webhookID)
	pipe.HDel(ctx, poolPlacementHash, webhookID)
	_, err := pipe.Exec(ctx)
	return err
}

func (o *KubernetesOrchestrator) removePoolWorkerRuntime(ctx context.Context, workerID, webhookID string) {
	pipe := o.rdb.Pipeline()
	pipe.HDel(ctx, poolWorkerRuntimeHash(workerID), webhookID)
	pipe.HDel(ctx, poolWorkerStatusHash(workerID), webhookID)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: poolWorkerCommandStream(workerID), MaxLen: 1000, Approx: true, Values: map[string]any{"action": "remove", "webhook_id": webhookID, "ts": time.Now().UnixMilli()}})
	_, _ = pipe.Exec(ctx)
}

func (o *KubernetesOrchestrator) waitPoolWorkers(ctx context.Context, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		workers, err := o.livePoolWorkers(ctx)
		if err == nil && len(workers) > 0 {
			return workers, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, errors.New("engine pool has no live workers")
}

func (o *KubernetesOrchestrator) livePoolWorkers(ctx context.Context) ([]string, error) {
	cutoff := strconv.FormatInt(time.Now().Add(-poolHeartbeatFreshness).UnixMilli(), 10)
	return o.rdb.ZRangeByScore(ctx, poolWorkersZSet, &redis.ZRangeBy{Min: cutoff, Max: "+inf"}).Result()
}

func (o *KubernetesOrchestrator) waitPoolRuntimeReady(ctx context.Context, webhookID string, workers []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready := 0
		for _, workerID := range workers {
			status, err := o.rdb.HGet(ctx, poolWorkerStatusHash(workerID), webhookID).Result()
			if err == nil && status == "ready" {
				ready++
			} else if err == nil && status == "failed" {
				return fmt.Errorf("runtime failed on worker %s", workerID)
			}
		}
		if ready == len(workers) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("pool runtime readiness timeout")
}

func (o *KubernetesOrchestrator) poolPlacement(ctx context.Context, webhookID string) []string {
	raw, err := o.rdb.HGet(ctx, poolPlacementHash, webhookID).Result()
	if err != nil {
		return nil
	}
	var workers []string
	_ = json.Unmarshal([]byte(raw), &workers)
	return workers
}

func (o *KubernetesOrchestrator) allocatePoolPort(ctx context.Context, webhookID uuid.UUID) (int, error) {
	span := maxRuntimeRoutePort - minRuntimeRoutePort + 1
	start := int(crc32.ChecksumIEEE(webhookID[:])) % span
	for offset := 0; offset < span; offset++ {
		port := minRuntimeRoutePort + (start+offset)%span
		ok, err := o.rdb.HSetNX(ctx, poolPortsHash, strconv.Itoa(port), webhookID.String()).Result()
		if err != nil {
			return 0, err
		}
		if ok {
			return port, nil
		}
		owner, err := o.rdb.HGet(ctx, poolPortsHash, strconv.Itoa(port)).Result()
		if err == nil && owner == webhookID.String() {
			return port, nil
		}
	}
	return 0, errors.New("no pool runtime ports available")
}

func rendezvousWorkers(key string, workers []string, count int) []string {
	type scored struct {
		id    string
		score uint64
	}
	scores := make([]scored, 0, len(workers))
	for _, worker := range workers {
		sum := sha256.Sum256([]byte(key + "|" + worker))
		scores = append(scores, scored{id: worker, score: binary.BigEndian.Uint64(sum[:8])})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if count > len(scores) {
		count = len(scores)
	}
	out := make([]string, count)
	for i := 0; i < count; i++ {
		out[i] = scores[i].id
	}
	return out
}

func difference(left, right []string) []string {
	keep := make(map[string]bool, len(right))
	for _, item := range right {
		keep[item] = true
	}
	var out []string
	for _, item := range left {
		if !keep[item] {
			out = append(out, item)
		}
	}
	return out
}
