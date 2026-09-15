# Bot monitoring and conversation analytics

## Root cause and resource contract

Bot deploys now return `pool-runtime/<webhook UUID>` and run as child processes
inside `engine-pool`. The old monitoring PromQL extracted a project UUID from
`release-*`/`debug-*` pod names; it cannot identify bots inside shared workers.
Never divide pod CPU/RAM by bot count or assign a worker's resources to every bot.

The backend now prefers these proposed worker-exported metrics (engine/deploy
agents must implement and scrape them; backend changes alone do not emit them):

- `engine_pool_runtime_cpu_seconds_total{project_id, ...}`: child-process CPU
  seconds counter. Backend uses a 5-minute rate, summed across runtime replicas.
- `engine_pool_runtime_memory_rss_bytes{project_id, ...}`: child-process resident
  memory gauge, summed across replicas.
- Recommended for worker health, not currently queried by this backend:
  `engine_pool_runtime_ready{project_id, ...}`: 0/1 per runtime replica.

`project_id` must be canonical `projects.id` UUID text, **not** webhook UUID,
owner UUID or imported numeric runtime tenant ID. Use worker/webhook/replica
labels to distinguish concurrent processes. Remove stopped-process series;
CPU counters must reset on process replacement. The scrape target must attach
`namespace` matching `APP_ENGINE_NAMESPACE`. Query namespace is no longer
hardcoded. Export one authoritative series per process to avoid double counting.
No payloads, bot tokens, or end-user IDs belong in metric labels.

Dedicated-pod queries remain as a fallback (`or`, not addition, to avoid double
counting). There is deliberately no shared-pod fallback. No per-runtime network
metric has been agreed: network queries still cover dedicated legacy pods only.
The monitoring list no longer presents old CPU/RAM/network snapshots as current
pool usage. Historical disk snapshots remain project-scoped.

`prometheus_available` means the queries succeeded, including empty results.
`resource_metrics_available` means attributed CPU **and** RAM samples exist for
that project (history: both series have samples). Clients must show unavailable,
not idle/zero consumption, when this flag is false. Existing numeric resource
fields remain zero then. List `cpu_cores` retains its existing **millicore** unit;
owner snapshot/history `cpu_cores` uses actual cores. Existing network `_bps`
fields use bits per second.

## Mongo persistence contract

Use the same Mongo deployment as CMS (`APP_MONGO_URL`) and database
`APP_ENGINE_MONGO_DB_NAME` (default `botspace_cms`), collection `bot_conversations`.
The analytics adapter reuses the CMS connection and only performs reads.

Documents written by the engine:

- `project_id`: canonical project UUID string (see warning above).
- `update_id`, `direction`: `inbound` or `outbound`.
- `user_id`, `chat_id`: int64; `message_id`, `type`, `payload`.
- `created_at`: BSON datetime in UTC, **not** a string.

The read path does not fetch or return payloads or individual user IDs.
Only requested, non-deleted SQL projects contribute to admin rankings/totals.
Wrong-direction, non-date and future-dated events are excluded. Distinct-user
counts require a positive numeric inbound `user_id`; outbound events never
activate a user. Engine writes must be idempotent under update retries and pool
replication. Do not deduplicate outbound events solely by `update_id` (one update
can produce several messages). This reader counts persisted events as-is.

Engine/deploy should provision at least this compound index:

```javascript
db.bot_conversations.createIndex({project_id: 1, created_at: -1})
```

The reader does not mutate schema/indexes at request time. A grouping by
project/user avoids large `$addToSet` arrays; aggregation permits disk spill and
has a five-second context deadline. All-time users means **retained history**:
TTL/deletions reduce counts; there is no reconstruction of pre-persistence data.
For very large histories, a separately maintained rollup will be necessary.

## API contract

All paths below are under `/api/v1`. Admin routes require admin/superuser.

Analytics fields:

| Field | Definition |
| --- | --- |
| `users_total` | Distinct positive inbound user IDs across retained history |
| `active_users_24h` | Distinct inbound users with events in rolling last 24 hours |
| `active_users_7d` | Distinct inbound users with events in rolling last 7 days |
| `messages_24h` | Recorded inbound + outbound events in rolling last 24 hours |
| `last_activity_at` | Latest inbound/outbound BSON timestamp; RFC3339 or null |

Time windows are inclusive at the boundary and share one UTC `now` per read.
`messages_24h` is an event count, so callbacks or other inbound update types
persisted as conversations count too; it is not a text-message-only count.

### `GET /admin/monitoring`

Existing `{items,total,limit,offset,prometheus_available}` envelope gains
`analytics_available`. Each item gains the five flat analytics fields and
`resource_metrics_available`.

- `ordering=users_desc|users_asc`: `users_total`.
- `ordering=activity_desc|activity_asc`: `messages_24h`, **not** last activity.
- Existing `cpu_*`, `ram_*`, `disk_*`, `net_*` keys remain accepted.
- Empty ordering: SQL creation date descending; invalid ordering: 400.
- Ties: creation date descending, then project UUID ascending.
- Search is applied before global sorting, then offset/limit (default 20,
  max 100). The old 10,000-project ranking cutoff has been removed.
- Mongo error: unsorted/resource-sorted list still works with
  `analytics_available=false`; analytics-sorted lists return 500 rather than
  pretending that every bot has zero activity.
- Global sorting materializes all matching projects in bounded SQL batches;
  total process memory is proportional to matching project count. Concurrent
  project insertion/deletion between SQL batches is not a snapshot transaction.

### `GET /admin/projects/{id}/statistics`

Returns the five analytics fields directly. Existing project with no events:
zeros and `last_activity_at:null`. Missing/deleted project: 404. Mongo failure:
500 (generic public error, no database detail).

### `GET /admin/stats/bots?ordering=activity_desc&limit=20`

Returns `{totals,projects_total,top_bots}`. `totals` contains the five analytics
fields; `top_bots` contains `{id,name,username?,users_total,active_users_24h,
active_users_7d,messages_24h,last_activity_at}`.

`ordering` accepts `activity_desc` (default) or `users_desc`. Limit defaults to
20/max 100. Totals cover all non-deleted projects, not just the returned top
bots. User totals are sums of per-bot distinct counts (**bot-user memberships**,
not globally deduplicated people). Missing analytics infrastructure returns 500.

### `GET /projects/{id}/monitoring`

Existing owner-scoped snapshot gains the five flat analytics fields,
`analytics_available`, and `resource_metrics_available`. Ownership is checked
before reading Mongo. Mongo failure does not prevent the resource snapshot.
Owner/admin history responses also gain `resource_metrics_available`.

## Validation

```sh
make sqlc
make swag
make test
go build ./...
go vet ./...
# Disposable real MongoDB container (Docker required):
go test -race -count=1 -tags integration ./internal/modules/project -run TestMongoAnalytics_ConversationSemantics -v
```
