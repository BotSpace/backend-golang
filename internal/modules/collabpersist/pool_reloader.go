package collabpersist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	poolDesiredHash   = "engine:pool:runtimes"
	poolPlacementHash = "engine:pool:placements"
)

type poolReloader struct {
	repo Repository
	rdb  *redis.Client
	log  *zap.Logger
}

// poolRuntimeSpec intentionally matches engine supervisor RuntimeSpec field
// order. Stable JSON avoids the reconciler treating a flow-only rewrite as a
// second config change and starting the child twice.
type poolRuntimeSpec struct {
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

func NewPoolReloader(repo Repository, rdb *redis.Client, log *zap.Logger) RuntimeReloader {
	return &poolReloader{repo: repo, rdb: rdb, log: log}
}

func (r *poolReloader) ReloadProject(ctx context.Context, projectID string) error {
	if r.repo == nil || r.rdb == nil {
		return nil
	}
	webhookID, flow, err := r.repo.RuntimeSnapshot(ctx, projectID)
	if err != nil {
		return fmt.Errorf("load runtime snapshot: %w", err)
	}
	desiredRaw, err := r.rdb.HGet(ctx, poolDesiredHash, webhookID).Result()
	if errors.Is(err, redis.Nil) {
		return nil // no running debug bot; the next start loads persisted flow
	}
	if err != nil {
		return err
	}
	desiredRaw, err = replaceRuntimeFlow(desiredRaw, flow)
	if err != nil {
		return err
	}
	placementRaw, err := r.rdb.HGet(ctx, poolPlacementHash, webhookID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	var workers []string
	_ = json.Unmarshal([]byte(placementRaw), &workers)

	pipe := r.rdb.Pipeline()
	pipe.HSet(ctx, poolDesiredHash, webhookID, desiredRaw)
	for _, workerID := range workers {
		runtimeHash := "engine:pool:worker:" + workerID + ":runtimes"
		workerRaw, getErr := r.rdb.HGet(ctx, runtimeHash, webhookID).Result()
		if getErr != nil {
			continue // pool reconciler restores a missing/stale worker assignment
		}
		workerRaw, replaceErr := replaceRuntimeFlow(workerRaw, flow)
		if replaceErr != nil {
			return replaceErr
		}
		pipe.HSet(ctx, runtimeHash, webhookID, workerRaw)
		pipe.HSet(ctx, "engine:pool:worker:"+workerID+":status", webhookID, "loading")
		pipe.XAdd(ctx, &redis.XAddArgs{
			Stream: "engine:pool:worker:" + workerID + ":commands",
			MaxLen: 1000, Approx: true,
			Values: map[string]any{"action": "upsert", "webhook_id": webhookID, "ts": time.Now().UnixMilli()},
		})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	r.log.Debug("collab runtime reload queued", zap.String("project_id", projectID), zap.String("webhook_id", webhookID), zap.Int("workers", len(workers)))
	return nil
}

func replaceRuntimeFlow(raw string, flow json.RawMessage) (string, error) {
	if !json.Valid(flow) {
		return "", errors.New("persisted flow is invalid JSON")
	}
	var spec poolRuntimeSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return "", fmt.Errorf("decode runtime spec: %w", err)
	}
	if spec.WebhookID == "" || spec.ProjectID == "" {
		return "", errors.New("runtime spec identity is missing")
	}
	spec.Flow = append(json.RawMessage(nil), flow...)
	next, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode runtime spec: %w", err)
	}
	return string(next), nil
}
