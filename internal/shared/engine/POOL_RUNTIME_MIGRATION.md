# Canonical pool runtime identity rollout

## Producer and child settings

`deployPoolRuntime` now writes both:

- `project_id` / `env.PROJECT_ID`: unchanged CMS tenant, including imported numeric IDs.
- `backend_project_id` / `env.BACKEND_PROJECT_ID`: `DeploySpec.ProjectID` (canonical SQL `projects.id`). A nil canonical UUID is rejected.

Project environment overrides cannot replace these identities. Both preferred
and legacy Mongo settings are explicitly set on the child:

- `APP_MONGO_URL` and `MONGO_ADDR`: backend process `APP_MONGO_URL`, default
  `mongodb://localhost:27017`, exactly like the backend CMS connection.
- `APP_ENGINE_MONGO_DB_NAME` and `MONGO_DB_NAME`: canonical configured database;
  env override, then configured `engine.mongo_db_name`, then `botspace_cms`.

The legacy `KubernetesConfig.MongoAddr` is deliberately not the pool child's
source of truth. Shared-module deployments are unchanged. The pool supervisor
itself must receive the same Mongo settings from deployment configuration;
these changes only control child specs, not supervisor pods.

## Existing persisted specs

The existing collab-persist worker now runs identity reconciliation at startup
and once per minute, including when there are no collaborative edits. A pass
has a 45-second timeout. No new worker/cron wiring is required.

For each desired entry in `engine:pool:runtimes`:

1. Resolve numeric `project_id` through SQL `projects.legacy_id`, or look up a
   UUID strictly as SQL `projects.id`. The resolver uses existing sqlc queries.
2. Verify the spec's webhook equals that project's debug/release webhook,
   according to its `debug` flag. Deleted projects are not accepted.
3. Reject any conflicting existing `backend_project_id` or env canonical ID.
4. Add canonical identity and normalize Mongo env settings in desired and
   **existing** worker assignment copies, including offline worker hashes found
   by SCAN. Preserve CMS tenant, flow, credentials, routing, and replica flags.
5. Atomically compare/update with Redis WATCH/MULTI and enqueue worker `upsert`
   commands only for changed worker copies. Conflicts retry on the next pass.

This never derives a UUID from a numeric ID, public project UUID, owner ID,
webhook ID, runtime name, or a hash. No UUID syntax fallback substitutes for
SQL ownership verification. Missing/deleted mappings and conflicting copies
are left untouched and logged for operator action. One bad mapping does not
prevent valid mappings elsewhere from being processed.

Hot reload preserves canonical identity and unknown top-level fields. It also
performs verified repair when needed, changes only the intended flow/config,
and uses WATCH/MULTI rather than overwriting a concurrent deploy/stop. It
retries up to three times on transaction conflicts. Repeating an unchanged
repair or flow reload does not queue another child restart.

## Required rollout sequence

1. Deploy this backend API **and backend worker** before the stricter engine.
2. Ensure worker PostgreSQL points to the authoritative backend database and
   worker Redis URL/database points to the same pool Redis used by the API and
   supervisor. Repair uses the worker's existing Redis client; it does not
   discover an alternate Redis instance or database.
3. Supply identical `APP_MONGO_URL` and `APP_ENGINE_MONGO_DB_NAME` to API, worker,
   and supervisor. If using YAML for the database name, mount the API's
   `configs/config.yaml` (or `config.yaml`) in the worker too. The worker reads
   only this setting, without requiring API JWT/crypto configuration. Do not
   rely on legacy worker `MONGO_URL` to choose the analytics destination.
4. Wait for `pool runtime identity reconciliation checked` with `failed=0` and
   the expected `desired` count. Check desired and worker copies contain the
   canonical field before upgrading engine workers. Do not print entire specs:
   they contain bot/backend/Redis/Mongo secrets.
5. Resolve failed mappings using authoritative SQL/backfill data or perform a
   normal backend redeploy of the affected project. Then roll out the engine.

## Limitations and operational effects

- This is a Redis spec migration, **not** SQL backfill or Mongo data migration.
  Missing `legacy_id` mappings require operator repair. Existing data in the
  old Mongo destination is not copied or merged. Changing Mongo destinations
  can require a separate CMS/history migration.
- Identity/env changes may restart an active child; they are not flow-only hot
  reloads. Use the usual rolling deployment window.
- Running the new engine before this backend worker finishes can temporarily
  reject numeric specs. No repair is possible while PostgreSQL/Redis is down,
  the worker is not running, or required mappings remain unresolved.
- Orphan worker assignments without a desired entry are not recreated or
  upgraded. Normal engine lifecycle cleanup owns them. An identity conflict
  in any existing copy leaves that runtime's repair uncommitted.
- Older supervisors do not fence their reconciliation writes and may briefly
  restore an assignment from an older desired snapshot during migration. The
  matching new engine uses WATCH/MULTI with exact desired-spec checks and
  guarded orphan cleanup. Upgrade backend first and verify copies before the
  engine rollout; this is not a global rollout barrier.
- Repair revalidates active specs every pass; large pools, continual concurrent
  writes, or SQL errors may require multiple passes. Monitor incomplete-pass
  warnings and counts; a 45-second deadline is not a completion guarantee.
- No manifests, supervisor code, config module, generated SQL, or unrelated
  backend modules are changed by this patch.

## Tests

```sh
go test -race -count=1 ./internal/shared/engine ./internal/modules/collabpersist
# Disposable real Redis and PostgreSQL containers (Docker required):
go test -race -count=1 -tags integration ./internal/modules/collabpersist -run 'TestPoolMigration_|TestResolveRuntimeProject_' -v
make test
go build ./...
go vet ./...
```
