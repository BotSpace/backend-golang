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

func (o *KubernetesOrchestrator) deployPoolRuntime(ctx context.Context, spec DeploySpec) (DeployResult, error) {
	if spec.ProjectID == uuid.Nil {
		return DeployResult{}, errors.New("canonical project id is required for pool runtime")
	}
	if spec.WebhookUUID == uuid.Nil {
		return DeployResult{}, errors.New("webhook uuid is required for pool runtime")
	}
	// The engine pool is a Kubernetes-managed infrastructure Deployment. The
	// backend only assigns runtimes to its live workers; it must never create or
	// mutate the pool as a side effect of a project deploy.
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

	runtime, err := o.poolRuntimeSpec(spec, port, replication)
	if err != nil {
		return DeployResult{}, err
	}
	raw, err := json.Marshal(runtime)
	if err != nil {
		return DeployResult{}, err
	}
	webhookID := spec.WebhookUUID.String()
	previousDesired, previousDesiredErr := o.rdb.HGet(ctx, poolDesiredHash, webhookID).Result()
	previousDesiredExists := previousDesiredErr == nil
	if previousDesiredErr != nil && !errors.Is(previousDesiredErr, redis.Nil) {
		return DeployResult{}, fmt.Errorf("load previous desired runtime: %w", previousDesiredErr)
	}
	if err := o.rdb.HSet(ctx, poolDesiredHash, webhookID, raw).Err(); err != nil {
		return DeployResult{}, fmt.Errorf("store desired runtime: %w", err)
	}
	previous := o.poolPlacement(ctx, webhookID)
	committed := false
	defer func() {
		if !committed {
			o.rollbackPoolRuntime(context.Background(), webhookID, previousDesired, previousDesiredExists, previous, targets)
		}
	}()
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
	committed = true
	for _, workerID := range difference(previous, targets) {
		_ = o.removePoolWorkerRuntime(ctx, workerID, webhookID)
	}
	return DeployResult{ServiceID: poolRuntimeServiceID(spec.WebhookUUID), Status: StatusInitializing}, nil
}

func (o *KubernetesOrchestrator) rollbackPoolRuntime(ctx context.Context, webhookID, previousDesired string, previousDesiredExists bool, previous, attempted []string) {
	if previousDesiredExists {
		_ = o.rdb.HSet(ctx, poolDesiredHash, webhookID, previousDesired).Err()
	} else {
		_ = o.rdb.HDel(ctx, poolDesiredHash, webhookID).Err()
	}
	if len(previous) == 0 {
		_ = o.rdb.HDel(ctx, poolPlacementHash, webhookID).Err()
	} else {
		raw, _ := json.Marshal(previous)
		_ = o.rdb.HSet(ctx, poolPlacementHash, webhookID, raw).Err()
	}
	for _, workerID := range difference(attempted, previous) {
		_ = o.removePoolWorkerRuntime(ctx, workerID, webhookID)
	}
}

func (o *KubernetesOrchestrator) stopPoolRuntime(ctx context.Context, ref poolRuntimeRef) error {
	webhookID := ref.WebhookID.String()
	var cleanupErrors []error

	if err := o.rdb.HDel(ctx, poolDesiredHash, webhookID).Err(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove desired runtime: %w", err))
	}

	workers, err := o.loadPoolPlacement(ctx, webhookID)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("load pool placement: %w", err))
	}
	staleWorkers, err := o.poolAssignmentWorkers(ctx)
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("discover pool assignments: %w", err))
	}
	workers = uniqueStrings(append(workers, staleWorkers...))
	for _, workerID := range workers {
		assigned, assignedErr := o.poolWorkerHasRuntime(ctx, workerID, webhookID)
		if assignedErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("check runtime assignment on %s: %w", workerID, assignedErr))
			assigned = true
		}
		if !assigned {
			continue
		}
		if err := o.removePoolWorkerRuntime(ctx, workerID, webhookID); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove runtime from %s: %w", workerID, err))
		}
	}

	if err := o.rdb.HDel(ctx, poolPlacementHash, webhookID).Err(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove pool placement: %w", err))
	}
	if err := o.releasePoolPorts(ctx, webhookID); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("release pool ports: %w", err))
	}
	return errors.Join(cleanupErrors...)
}

func (o *KubernetesOrchestrator) removePoolWorkerRuntime(ctx context.Context, workerID, webhookID string) error {
	pipe := o.rdb.Pipeline()
	pipe.HDel(ctx, poolWorkerRuntimeHash(workerID), webhookID)
	pipe.HDel(ctx, poolWorkerStatusHash(workerID), webhookID)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: poolWorkerCommandStream(workerID), MaxLen: 1000, Approx: true, Values: map[string]any{"action": "remove", "webhook_id": webhookID, "ts": time.Now().UnixMilli()}})
	_, err := pipe.Exec(ctx)
	return err
}

func (o *KubernetesOrchestrator) poolAssignmentWorkers(ctx context.Context) ([]string, error) {
	var workers []string
	for _, suffix := range []string{":runtimes", ":status"} {
		var cursor uint64
		for {
			keys, next, err := o.rdb.Scan(ctx, cursor, poolWorkerRuntimeBase+"*"+suffix, 100).Result()
			if err != nil {
				return workers, err
			}
			for _, key := range keys {
				if workerID, ok := poolAssignmentWorkerID(key, suffix); ok {
					workers = append(workers, workerID)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return uniqueStrings(workers), nil
}

func poolAssignmentWorkerID(key, suffix string) (string, bool) {
	if !strings.HasPrefix(key, poolWorkerRuntimeBase) || !strings.HasSuffix(key, suffix) {
		return "", false
	}
	workerID := strings.TrimSuffix(strings.TrimPrefix(key, poolWorkerRuntimeBase), suffix)
	return workerID, workerID != ""
}

func (o *KubernetesOrchestrator) poolWorkerHasRuntime(ctx context.Context, workerID, webhookID string) (bool, error) {
	pipe := o.rdb.Pipeline()
	runtimeExists := pipe.HExists(ctx, poolWorkerRuntimeHash(workerID), webhookID)
	statusExists := pipe.HExists(ctx, poolWorkerStatusHash(workerID), webhookID)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return runtimeExists.Val() || statusExists.Val(), nil
}

func (o *KubernetesOrchestrator) releasePoolPorts(ctx context.Context, webhookID string) error {
	reservations, err := o.rdb.HGetAll(ctx, poolPortsHash).Result()
	if err != nil {
		return err
	}
	ports := make([]string, 0, 1)
	for port, owner := range reservations {
		if owner == webhookID {
			ports = append(ports, port)
		}
	}
	if len(ports) == 0 {
		return nil
	}
	return o.rdb.HDel(ctx, poolPortsHash, ports...).Err()
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
			} else if err == nil && runtimeStatusFailed(status) {
				return fmt.Errorf("runtime failed on worker %s with status %s", workerID, status)
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
	workers, _ := o.loadPoolPlacement(ctx, webhookID)
	return workers
}

func (o *KubernetesOrchestrator) loadPoolPlacement(ctx context.Context, webhookID string) ([]string, error) {
	raw, err := o.rdb.HGet(ctx, poolPlacementHash, webhookID).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var workers []string
	if err := json.Unmarshal([]byte(raw), &workers); err != nil {
		return nil, err
	}
	return workers, nil
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

func runtimeStatusFailed(status string) bool {
	return status == "failed" || strings.HasPrefix(status, "permanent_failure:")
}

func configurePoolRuntimeMode(env map[string]string, debug bool, testBotToken, internalToken string) error {
	if debug {
		env["BOT_TOKEN"] = testBotToken
		env["DEBUG_MODE"] = "true"
		env["DEBUG_EVENT_TOKEN"] = internalToken
		delete(env, "IS_RELEASE")
		delete(env, "RELEASE_BOT_TOKEN")
	} else {
		env["BOT_TOKEN"] = env["RELEASE_BOT_TOKEN"]
		env["IS_RELEASE"] = "true"
		delete(env, "DEBUG_MODE")
		delete(env, "DEBUG_EVENT_TOKEN")
	}
	if strings.TrimSpace(env["BOT_TOKEN"]) == "" {
		return errors.New("bot token is required")
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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
