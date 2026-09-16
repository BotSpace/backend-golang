package collabpersist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/botspace/backend/internal/shared/engine"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const (
	poolDesiredHash   = "engine:pool:runtimes"
	poolPlacementHash = "engine:pool:placements"
)

type poolReloader struct {
	repo      Repository
	rdb       *redis.Client
	log       *zap.Logger
	mongo     engine.PoolMongoConfig
	configErr error
}

// poolRuntimeSpec intentionally matches engine supervisor RuntimeSpec field
// order. Stable JSON avoids the reconciler treating a flow-only rewrite as a
// second config change and starting the child twice.
type poolRuntimeSpec struct {
	WebhookID        string            `json:"webhook_id"`
	ProjectID        string            `json:"project_id"`
	BackendProjectID string            `json:"backend_project_id,omitempty"`
	Name             string            `json:"name"`
	Debug            bool              `json:"debug"`
	RoutePort        int               `json:"route_port"`
	Replicas         int               `json:"replicas,omitempty"`
	Flow             json.RawMessage   `json:"flow"`
	Schemas          json.RawMessage   `json:"schemas"`
	Modules          json.RawMessage   `json:"modules"`
	Env              map[string]string `json:"env"`
}

func NewPoolReloader(repo Repository, rdb *redis.Client, log *zap.Logger) RuntimeReloader {
	mongo, err := loadPoolMongoConfig()
	return &poolReloader{repo: repo, rdb: rdb, log: log, mongo: mongo, configErr: err}
}

// The worker does not load the API Config. Read only its Mongo database setting
// here; invoking config.Load would impose unrelated API/JWT validation on workers.
func loadPoolMongoConfig() (engine.PoolMongoConfig, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("./configs")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		var missing viper.ConfigFileNotFoundError
		if !errors.As(err, &missing) {
			return engine.PoolMongoConfig{}, errors.New("cannot read backend Mongo configuration")
		}
	}
	return engine.CanonicalPoolMongoConfig(v.GetString("engine.mongo_db_name")), nil
}

func (r *poolReloader) ReloadProject(ctx context.Context, projectID string) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := r.reloadProject(ctx, projectID)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return redis.TxFailedErr
}

func (r *poolReloader) reloadProject(ctx context.Context, projectID string) error {
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
	var expected poolRuntimeSpec
	if err := json.Unmarshal([]byte(desiredRaw), &expected); err != nil {
		return errors.New("invalid desired runtime JSON")
	}
	if expected.WebhookID != webhookID || !expected.Debug {
		return errors.New("debug runtime identity mismatch")
	}
	canonical := ""
	if resolver, ok := r.repo.(RuntimeIdentityRepository); ok {
		if r.configErr != nil {
			return errors.New("backend Mongo configuration unavailable")
		}
		canonical, err = resolver.ResolveRuntimeProject(ctx, expected.ProjectID, webhookID, true)
		if err != nil || canonical != projectID {
			return errors.New("SQL debug runtime identity could not be verified")
		}
	}
	hashes, err := r.runtimeHashes(ctx)
	if err != nil {
		return err
	}
	if err := r.updateRuntimeCopies(ctx, webhookID, desiredRaw, hashes, func(raw string) (string, error) {
		var spec poolRuntimeSpec
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return "", errors.New("invalid runtime JSON")
		}
		if spec.WebhookID != webhookID || spec.ProjectID != expected.ProjectID || !spec.Debug {
			return "", errors.New("worker debug identity mismatch")
		}
		if canonical != "" {
			var err error
			raw, err = upgradeRuntimeIdentity(raw, expected, canonical, r.mongo)
			if err != nil {
				return "", err
			}
		}
		return replaceRuntimeFlow(raw, flow)
	}); err != nil {
		return err
	}
	r.log.Debug("collab runtime reload queued", zap.String("project_id", projectID), zap.String("webhook_id", webhookID))
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
	next, err := marshalRuntimeSpec(raw, spec)
	if err != nil {
		return "", fmt.Errorf("encode runtime spec: %w", err)
	}
	return string(next), nil
}

// Keep supervisor field order for byte-stable reconciliation, but do not erase
// fields written by a newer engine/backend during a rolling deployment.
func marshalRuntimeSpec(raw string, spec poolRuntimeSpec) ([]byte, error) {
	var extra map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return nil, err
	}
	for _, key := range []string{"webhook_id", "project_id", "backend_project_id", "name", "debug", "route_port", "replicas", "flow", "schemas", "modules", "env"} {
		delete(extra, key)
	}
	base, err := json.Marshal(spec)
	if err != nil || len(extra) == 0 {
		return base, err
	}
	tail, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	base[len(base)-1] = ','
	return append(base, tail[1:]...), nil
}
