package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (o *KubernetesOrchestrator) poolRuntimeSpec(spec DeploySpec, port, replication int) (storedRuntimeSpec, error) {
	if spec.ProjectID == uuid.Nil {
		return storedRuntimeSpec{}, errors.New("canonical project id is required for pool runtime")
	}
	runtimeProjectID := strings.TrimSpace(spec.RuntimeProjectID)
	if runtimeProjectID == "" {
		runtimeProjectID = spec.ProjectID.String()
	}
	env := make(map[string]string, len(spec.Env)+20)
	for key, value := range spec.Env {
		env[key] = value
	}
	env["PROJECT_ID"] = runtimeProjectID
	env["BACKEND_PROJECT_ID"] = spec.ProjectID.String()
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
	CanonicalPoolMongoConfig(o.cfg.MongoDBName).Apply(env)
	if err := configurePoolRuntimeMode(env, spec.Debug, o.cfg.TestBotToken, o.cfg.InternalToken); err != nil {
		return storedRuntimeSpec{}, fmt.Errorf("BOT_TOKEN is required for runtime %s", spec.WebhookUUID)
	}
	flow, err := canonicalJSONObject(spec.Flow, `{"nodes":[],"edges":[]}`, "flow")
	if err != nil {
		return storedRuntimeSpec{}, err
	}
	schemas, err := canonicalJSONObject(spec.Schemas, `{}`, "schemas")
	if err != nil {
		return storedRuntimeSpec{}, err
	}
	modules, err := canonicalJSONObject(spec.Modules, `{"modules":[]}`, "modules")
	if err != nil {
		return storedRuntimeSpec{}, err
	}
	return storedRuntimeSpec{WebhookID: spec.WebhookUUID.String(), ProjectID: runtimeProjectID, BackendProjectID: spec.ProjectID.String(), Name: resolveDeploymentName(spec), Debug: spec.Debug, RoutePort: port, Replicas: replication, Flow: flow, Schemas: schemas, Modules: modules, Env: env}, nil
}
