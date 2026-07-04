# engine-golang → backend-golang: Redis Stream event contract

Per the architecture directive: engine → backend **writes** (high-volume,
fire-and-forget persistence) go over a Redis Stream, not a synchronous
internal HTTP API. Reads (credentials, config, chat history, template
catalog, release/debug logs) stay HTTP — see the main task's item 2.

This mirrors `worker-golang`'s existing Django-web → worker consumer
(`worker-golang/internal/shared/redisq/stream.go`) byte-for-byte, so
engine-golang only needs one `XADD` helper shared by every Go consumer it
talks to.

## Transport

- **Stream key**: `backend:events`
- **Consumer group**: `backend-golang`
- **Redis**: same instance already injected into release/debug engine pods
  as `REDIS_ADDR`/`REDIS_PASSWORD` (this backend reads it from
  `engine.redis_host`/`engine.redis_port`/`engine.redis_password`, i.e.
  `APP_ENGINE_REDIS_HOST` etc. — no new config).

Producer (engine-golang, **not yet implemented** — this is the contract to
build against):

```
XADD backend:events * payload '{"task":"<event-type>","args":{...}}'
```

- `payload` is a single Redis Stream field containing a JSON string.
- `task` selects the handler (see registry below).
- `args` is task-specific JSON — schemas below.
- No dedup/idempotency key at the transport level: every handler must be
  safe to run twice (the consumer never acks before the handler returns
  successfully, so a backend restart mid-handler redelivers the entry).

Consumer (this repo): `internal/shared/redisq` (`Consumer.Register(task, handler)`,
started as a background goroutine in `internal/server/server.go`
`startEventConsumer`). `XGroupCreateMkStream` runs on startup, so the engine
can start XADDing before the backend has ever run once.

## Project reference convention

Every event below carries `project_id` as the **engine's** raw reference —
either the Django legacy integer pk (as a string/number) or Django's
`project.uuid` field — **not** this backend's internal `projects.id`
(a uuid5 hash the engine has no way to compute). The backend resolves it via
`debug.Usecase.ResolveProjectRef` (mirrors Django's
`cms/views/_common.py resolve_project()`):

1. Try parsing as an integer → look up `projects.legacy_id`.
2. Else try parsing as a UUID → look up `projects.uuid` (NOT `projects.id`).
3. Else the event is rejected (bad payload, acked and dropped — retrying a
   malformed ref never helps).

This is exactly the id space the engine already has today (its `PROJECT_ID`
env var — see the item-1 finding: production release pods currently get the
Django legacy int, e.g. `PROJECT_ID=1408`).

## Event registry

Every `debug.*` handler below, after its Postgres/Redis persistence
succeeds, ALSO forwards the event to the constructor's live WS feed by
publishing to the standalone `websocket` service's Redis Pub/Sub channel
(package `internal/shared/wsevents` — see its doc comment for the channel
name + envelope shape, which mirrors Django's `ws_publish` exactly). The room
is `project_<ref>` using the event's own `project_id` verbatim (no legacy-id
resolution needed — `IssueCollabToken`'s room naming already accepts either
an int or a string ref, and this is the raw engine reference either way).
`release_status`/`debug_status` are published the same way but originate
from `internal/modules/release`'s usecase (deploy/stop/reconcile), not this
consumer — see `internal/modules/release/ws_events.go`.

### `debug.error_event` — IMPLEMENTED (consumer + persistence + read API live), forwarded as WS `error`

Replaces the old synchronous `POST {API_URL}/api/debug/error-event/` /
`error-event` HTTP call the engine's `internal/debug/emitter.go` `EmitError`/
`EmitAlert` make today (see engine-golang `errorEvent` struct, line 132).

```json
{
  "task": "debug.error_event",
  "args": {
    "project_id": "1408",
    "chat_id": 123456789,
    "node_id": "node_7",
    "node_type": "message",
    "level": "error",
    "message": "template render failed",
    "detail": "KeyError: 'user.name'",
    "ts": 1735689600
  }
}
```

- `ts` is a Unix seconds timestamp (optional; falls back to insert time).
- Persisted to Postgres `debug_error_event` (migration `000034`), scoped by
  the resolved internal `project_id`.
- Read side: `GET /api/v1/debug/errors?project_id=<internal-uuid>&limit=` (JWT,
  owner-scoped) → `{"errors":[{node_id,node_type,message,detail,chat_id,ts}]}`.

### `debug.node_event` — IMPLEMENTED (consumer + persistence live, no read API yet), forwarded as WS `node_execution`

Replaces `POST .../api/debug/node-event/` (engine's `nodeEvent`, emitter.go
line 26). Django only ever WebSocket-published this (no persistence); this
backend now persists it to Postgres `debug_node_event` (migration `000036`)
for a future "node execution history" read API, but does not expose one yet
— write-only for now (`debug.Usecase.RecordNodeEvent`).

```json
{
  "task": "debug.node_event",
  "args": {
    "project_id": "1408",
    "chat_id": 123456789,
    "node_id": "node_7",
    "node_type": "message",
    "status": "done",
    "error": null,
    "ts": 1735689600,
    "run_id": 42,
    "seq": 7
  }
}
```

### `debug.context_event` — IMPLEMENTED (Redis-cache consumer live), forwarded as WS `context`

Replaces `POST .../api/debug/context-event/` (engine's `contextEvent`,
emitter.go line 222). Django cached only the *latest* snapshot per
`(project, chat_id)` in Redis with a 10-minute TTL, not a Postgres table —
this backend's consumer does the same: `debug.Usecase.RecordContext` SETs
`debug:ctx:{internal-project-id}:{chat_id}` (`debug.RedisContextCache`,
`internal/modules/debug/context_cache.go`) with a 10-minute TTL, keyed by
this backend's *resolved* internal project id (not the engine's raw ref).
No read API yet (nothing consumes the cached key today — add one if/when a
live variable-inspector view needs it). If the backend can't reach the
engine's Redis at startup (`Engine.RedisAddr()` empty), `RecordContext`
silently no-ops rather than failing the event.

```json
{
  "task": "debug.context_event",
  "args": {
    "project_id": "1408",
    "chat_id": 123456789,
    "variables": { "...": "slimmed flow context" },
    "ts": 1735689600
  }
}
```

### `bot_user.upsert` — CONTRACT DEFINED, not yet consumed

Today the engine writes this **directly to Mongo** (`internal/repository/user.go`
`UpsertUser`, collection `users` in the project's Mongo scope) with an HTTP
fallback only used when `MONGO_ADDR` isn't configured (dev/simulator). Since
the engine already reaches Mongo directly and item 1 makes the CMS `users`
collection tenant-scoped by `_project_id` in the shared `botspace_cms` DB,
there is **no backend consumer needed for this today** — only add one if the
direct-Mongo path is retired in favor of routing every engine write through
the backend. Contract, if that happens:

```json
{
  "task": "bot_user.upsert",
  "args": {
    "project_id": "1408",
    "chat_id": 123456789,
    "first_name": "Ali",
    "last_name": "Valiyev",
    "username": "alivaliyev"
  }
}
```

### `message.save` / `token_usage.record` — NOT FOUND in engine-golang today

Grepped engine-golang for both (inbound-message persistence, LLM
token/credit usage reporting) and found neither exists as a write path yet
(see the research summary in this task's report). No contract to define
until the engine side implements the feature — define the `args` shape when
that work starts, following the same `project_id`-as-raw-ref convention
above.

### Writes that do NOT belong on this stream

Re-grepped engine-golang's actual outbound HTTP writes (beyond the three
`debug.*` calls above) to make sure nothing else was silently missed:

- **Payment order creation** (`internal/engine/handlers/payment.go:208`
  `createOrder`, and the LLM `payment_link` tool,
  `internal/llm/payment_link_tool.go:215`) — `POST
  {API_URL}/api/payment/order/create/`. **Not a stream candidate**: the flow
  node needs the created order's id/payment URL back synchronously to
  render a payment link/button in the same turn. XADD is fire-and-forget
  with no reply channel, so this must stay a synchronous HTTP call (or
  become a request/reply RPC some other way, not this stream).
- **Generic "Save to Collection" flow-node writes**
  (`internal/repository/collection.go` Insert/Update/Delete) — same
  reasoning: `POST /api/collection/{name}/insert|update|delete` is only hit
  as an HTTP *fallback* when the engine has no direct Mongo connection
  (dev/simulator, `MONGO_ADDR` unset — see `bot_user.upsert` above for the
  identical pattern), and the flow can reference the inserted document
  afterward, so it isn't pure fire-and-forget either. Leave as-is.
- **Module file upload** (`internal/engine/handlers/download.go:167`
  `uploadFileToModuleAPI`) — `POST {API_URL}/api/module-file/` (multipart,
  binary body). Needs the uploaded file's id/URL back synchronously, and
  Redis Streams isn't a great fit for multipart/binary payloads anyway.
  Leave as HTTP.

## Adding a new event type

1. Pick a `task` name (`domain.verb_noun`, e.g. `debug.error_event`).
2. Define its `args` JSON shape here.
3. `consumer.Register("<task>", handler)` in
   `internal/server/server.go` `startEventConsumer`.
4. Resolve `args.project_id` via `debugmod.Usecase.ResolveProjectRef` (or a
   sibling module's equivalent) before persisting — never trust it as this
   backend's internal id directly.
5. Keep the handler idempotent — no ack happens until it returns nil, so a
   crash mid-handler always redelivers.
