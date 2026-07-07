# backend-golang and engine-golang integration guide

This document explains how `backend-golang` deploys and talks to
`engine-golang`.

Related engine guide:

```text
../engine-golang/docs/BOTSPACE_ENGINE_GUIDE.md
```

## 1. Responsibility split

`backend-golang` owns:

- users, auth, plans, billing, templates, projects, releases
- flow persistence
- deployment orchestration
- admin/dashboard APIs
- Redis stream consumer for engine debug events

`engine-golang` owns:

- Telegram update processing
- flow JSON runtime execution
- per-chat flow state
- Telegram message/action handlers
- runtime CMS collection reads/writes
- runtime payment callback resume
- runtime debug node/context/error emission

## 2. Deployment path

Release/debug deploys go through:

```text
internal/modules/release/usecase.go
  -> internal/shared/engine.Orchestrator
  -> internal/shared/engine/kubernetes.go
```

The real implementation creates Kubernetes resources:

```text
Deployment
Secret      <deployment-name>-env
ConfigMap   <deployment-name>-flow
Service     bot-<webhook-uuid> when webhook UUID exists
```

The ConfigMap mounts:

```text
/app/assets/flow.json
/app/assets/schemas.json
/app/assets/modules.json
```

The engine reads `FLOW_FILE_PATH=/app/assets/flow.json`.

## 3. Env contract injected into engine pods

Backend injects these env vars for bot engine pods:

```text
FLOW_FILE_PATH=/app/assets/flow.json
SCHEMAS_FILE_PATH=/app/assets/schemas.json
MODULE_CONFIG_PATH=/app/assets/modules.json
WEBHOOK_ENABLED=true
WEBHOOK_PORT=<container-port>
API_URL=<backend public/internal URL>
PROJECT_ID=<project ref>
API_TOKEN=<internal token>
INTERNAL_TOKEN=<internal token>
BOT_TOKEN=<debug/release/test token>
DEBUG_MODE=true                  debug deploy only
DEBUG_EVENT_TOKEN=<internal token> debug deploy only
REDIS_ENABLED=true               when engine Redis configured
REDIS_ADDR=<host:port>
REDIS_PASSWORD=<password>
REDIS_PREFIX=<per-bot prefix>
BACKEND_EVENTS_STREAM=true       when Redis configured
MONGO_ADDR=<mongo URL>
MONGO_DB_NAME=<shared CMS DB>
```

The code that builds this env is:

```text
internal/shared/engine/kubernetes.go
```

The engine reads it in:

```text
engine-golang/internal/config/config.go
```

## 4. Flow JSON normalization

Backend normalizes empty/new project flow values before mounting:

```text
[] or empty -> {"nodes":[],"edges":[]}
```

This matters because `engine-golang` expects an object schema, not a JSON array.

Backend also mounts placeholder documents:

```json
schemas.json = {}
modules.json = {"modules":[]}
```

This prevents engine startup failure when CMS schemas/modules are not yet wired.

## 5. Status and logs

Backend maps Kubernetes deployment status to release/debug status:

```text
ready replicas > 0       -> running
replicas > 0             -> initializing unless crash-looping
not found                -> stopped
crash-looping pod        -> failed
```

Status abstraction:

```text
internal/shared/engine/engine.go
```

Kubernetes implementation:

```text
internal/shared/engine/kubernetes.go
```

Logs are read from the pod backing the deployment through the same orchestrator.

## 6. Debug event path

In debug mode, engine emits node/context/error events.

Preferred path:

```text
engine-golang
  -> Redis XADD backend:events
  -> backend-golang Redis consumer
  -> Postgres/Redis persistence
  -> websocket Redis pub/sub
  -> constructor live debug UI
```

Contract:

```text
docs/redis-stream-events.md
```

Backend enables this by injecting:

```text
BACKEND_EVENTS_STREAM=true
```

when engine Redis is configured.

## 7. Payment callback path

Payment flow:

```text
PaymentNode in engine
  -> synchronous backend order creation
  -> engine pauses at PaymentNode
  -> backend/payment provider confirms status
  -> backend calls engine /payment-callback
  -> engine resumes graph from PaymentNode
```

Auth for internal callback:

```text
X-Internal-Token: <INTERNAL_TOKEN>
```

Do not move order creation to Redis stream; the node needs the response
synchronously to render a payment URL/button.

## 8. CMS/Mongo path

Engine collection nodes use Mongo directly when configured:

```text
MONGO_ADDR
MONGO_DB_NAME
PROJECT_ID
```

Engine scopes documents with:

```text
_project_id
```

`PROJECT_ID` can be a legacy integer or UUID string. Backend read paths must
resolve both shapes when reading engine-written data.

## 9. Common integration failures

Engine pod starts but is not ready:

- Invalid or empty `BOT_TOKEN`.
- Mounted `flow.json` is not a schema object.
- Redis is enabled but unreachable.
- Telegram API cannot be reached.

Bot runs but debug UI has no node colors:

- `DEBUG_MODE` is false.
- `BACKEND_EVENTS_STREAM` is false.
- Engine Redis config does not match backend Redis config.
- Backend worker/consumer is not running.
- Websocket relay Redis/channel is not configured.

Collection nodes fail:

- `MONGO_ADDR` or `MONGO_DB_NAME` missing.
- `PROJECT_ID` mismatch with backend project tenant.
- Flow filter uses `_id` as string but collection stores ObjectID; engine has
  normalization for 24-char hex strings, but custom filters can still be wrong.

Payment resume fails:

- `INTERNAL_TOKEN` mismatch.
- Backend calls the wrong engine service.
- Payment callback payload does not match the order/project/chat expected by
  engine state.

## 10. Development checklist for changes

When changing backend deployment behavior:

- Update `internal/shared/engine/kubernetes.go`.
- Keep env names compatible with `engine-golang/internal/config/config.go`.
- Add or update fake-client tests in `internal/shared/engine`.
- Update this document when env contract changes.

When changing engine runtime behavior:

- Add tests in `engine-golang/internal/engine/...`.
- Update `engine-golang/docs/BOTSPACE_ENGINE_GUIDE.md`.
- If a new engine->backend write is added, define the Redis stream contract in
  `docs/redis-stream-events.md` before implementing the consumer.
- If a user-facing node changes, update constructor and agent catalogs too.
