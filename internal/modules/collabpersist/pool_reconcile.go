package collabpersist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/botspace/backend/internal/shared/engine"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ReconcileRuntimes upgrades existing desired and worker specs without changing
// tenant IDs, placement, flow, ports, or replica flags. SQL is the authority even
// when an old runtime's project_id happens to have UUID syntax.
func (r *poolReloader) ReconcileRuntimes(ctx context.Context) error {
	if r.configErr != nil {
		return errors.New("pool migration cannot load backend Mongo configuration")
	}
	resolver, ok := r.repo.(RuntimeIdentityRepository)
	if !ok || r.rdb == nil {
		return errors.New("pool migration requires SQL identity resolver and Redis")
	}
	desired, err := r.rdb.HGetAll(ctx, poolDesiredHash).Result()
	if err != nil {
		return fmt.Errorf("load pool migration candidates: %w", err)
	}
	hashes, err := r.runtimeHashes(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for hook, raw := range desired {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		if err := r.reconcileRuntime(ctx, resolver, hook, raw, hashes); err != nil {
			// Specs contain secrets; neither raw JSON nor database errors belong in logs.
			failures = append(failures, fmt.Errorf("runtime %s: %w", hook, err))
		}
	}
	if r.log != nil {
		r.log.Info("pool runtime identity reconciliation checked", zap.Int("desired", len(desired)), zap.Int("failed", len(failures)))
	}
	return errors.Join(failures...)
}

func (r *poolReloader) reconcileRuntime(ctx context.Context, resolver RuntimeIdentityRepository, hook, raw string, hashes []string) error {
	var spec poolRuntimeSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return errors.New("invalid persisted runtime JSON")
	}
	if spec.WebhookID != hook {
		return errors.New("runtime key/webhook mismatch")
	}
	canonical, err := resolver.ResolveRuntimeProject(ctx, spec.ProjectID, hook, spec.Debug)
	if err != nil {
		return errors.New("SQL runtime identity could not be verified")
	}
	return r.updateRuntimeCopies(ctx, hook, raw, hashes, func(value string) (string, error) {
		return upgradeRuntimeIdentity(value, spec, canonical, r.mongo)
	})
}

func upgradeRuntimeIdentity(raw string, expected poolRuntimeSpec, canonical string, mongo engine.PoolMongoConfig) (string, error) {
	id, err := uuid.Parse(canonical)
	if err != nil || id == uuid.Nil {
		return "", errors.New("SQL returned invalid canonical identity")
	}
	if mongo.URI == "" || mongo.Database == "" {
		return "", errors.New("backend Mongo destination is missing")
	}
	var spec poolRuntimeSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return "", errors.New("invalid persisted runtime JSON")
	}
	if spec.WebhookID != expected.WebhookID || spec.ProjectID != expected.ProjectID || spec.Debug != expected.Debug {
		return "", errors.New("desired/worker runtime identity mismatch")
	}
	for _, claim := range []string{spec.BackendProjectID, spec.Env["BACKEND_PROJECT_ID"]} {
		if claim != "" && claim != id.String() {
			return "", errors.New("canonical identity conflicts with SQL mapping")
		}
	}
	if spec.BackendProjectID == id.String() && spec.Env["BACKEND_PROJECT_ID"] == id.String() && spec.Env["PROJECT_ID"] == spec.ProjectID &&
		spec.Env["APP_MONGO_URL"] == mongo.URI && spec.Env["MONGO_ADDR"] == mongo.URI &&
		spec.Env["APP_ENGINE_MONGO_DB_NAME"] == mongo.Database && spec.Env["MONGO_DB_NAME"] == mongo.Database {
		return raw, nil
	}
	spec.BackendProjectID = id.String()
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env["BACKEND_PROJECT_ID"] = id.String()
	spec.Env["PROJECT_ID"] = spec.ProjectID
	mongo.Apply(spec.Env)
	next, err := marshalRuntimeSpec(raw, spec)
	if err != nil {
		return "", errors.New("cannot encode repaired runtime")
	}
	return string(next), nil
}

// Scan assignment hashes as well as live placement: stopped workers' persisted
// copies must not reintroduce the old spec when they restart. Orphan assignments
// with no desired runtime are deliberately not resurrected.
func (r *poolReloader) runtimeHashes(ctx context.Context) ([]string, error) {
	var hashes []string
	seen := map[string]bool{}
	var cursor uint64
	for {
		keys, next, err := r.rdb.Scan(ctx, cursor, "engine:pool:worker:*:runtimes", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan pool assignments: %w", err)
		}
		for _, key := range keys {
			if !seen[key] {
				seen[key] = true
				hashes = append(hashes, key)
			}
		}
		cursor = next
		if cursor == 0 {
			return hashes, nil
		}
	}
}

// WATCH prevents repair/hot reload from overwriting a deploy, stop, worker
// assignment, or newer flow committed after the snapshot. No missing spec is
// created here; engine reconciliation remains responsible for placement.
func (r *poolReloader) updateRuntimeCopies(ctx context.Context, hook, expectedRaw string, hashes []string, transform func(string) (string, error)) error {
	keys := append([]string{poolDesiredHash}, hashes...)
	return r.rdb.Watch(ctx, func(tx *redis.Tx) error {
		raw, err := tx.HGet(ctx, poolDesiredHash, hook).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		if raw != expectedRaw {
			return redis.TxFailedErr
		}
		next, err := transform(raw)
		if err != nil {
			return err
		}
		updates := map[string]string{}
		if next != raw {
			updates[poolDesiredHash] = next
		}
		for _, hash := range hashes {
			raw, err := tx.HGet(ctx, hash, hook).Result()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return err
			}
			next, err := transform(raw)
			if err != nil {
				return err
			}
			if next != raw {
				updates[hash] = next
			}
		}
		if len(updates) == 0 {
			return nil
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			for hash, value := range updates {
				pipe.HSet(ctx, hash, hook, value)
				if hash == poolDesiredHash {
					continue
				}
				base := strings.TrimSuffix(hash, ":runtimes")
				pipe.HSet(ctx, base+":status", hook, "loading")
				pipe.XAdd(ctx, &redis.XAddArgs{Stream: base + ":commands", MaxLen: 1000, Approx: true, Values: map[string]any{"action": "upsert", "webhook_id": hook, "ts": time.Now().UnixMilli()}})
			}
			return nil
		})
		return err
	}, keys...)
}
