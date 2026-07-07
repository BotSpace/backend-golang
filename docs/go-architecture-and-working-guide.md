# Go backend architecture and working guide

This guide explains how to work on `backend-golang` using the clean architecture
style inherited from the `uzstack/jst-go` template.

Source template checked: https://github.com/uzstack/jst-go
Checked revision: `81b086c57efe3360ffe7daf79a4d430bf53ddd64`

## 1. Mental model

`backend-golang` is a feature-modular Go API. Every business area lives in its
own package under:

```text
internal/modules/<module>/
```

The usual module shape is:

```text
domain.go       core entities, repository ports, business constants
usecase.go      business rules and orchestration
repository.go   Postgres/sqlc/pgx adapter
dto.go          request and response structs
handler.go      HTTP handlers: bind input, call usecase, return response
routes.go       route registration and middleware placement
```

The important rule is dependency direction:

```text
handler -> usecase -> repository interface -> repository implementation
```

Business logic must stay in `usecase.go`. HTTP concerns stay in handlers and
routes. SQL and storage details stay in repositories. Shared infrastructure
lives in `internal/shared`.

## 2. Project map

Top-level backend directories:

```text
cmd/api/          API entrypoint and graceful shutdown
cmd/worker/       background jobs and queue consumers
configs/          default YAML config
internal/server/  dependency wiring and route registration
internal/modules/ business modules
internal/shared/  config, database, middleware, logging, crypto, engine, etc.
migrations/       database migrations
queries/          sqlc query definitions
docs/             Swagger output and hand-written operations docs
```

Important shared packages:

```text
internal/shared/config      Viper config loader and APP_* env mapping
internal/shared/database    pgx pool, migrations, common DB errors
internal/shared/httpx       response helpers and AppError mapping
internal/shared/middleware  auth, CORS, request ID, timeout, rate limit
internal/shared/crypto      secrets-at-rest encryption helper
internal/shared/engine      deploy/debug orchestration abstraction
internal/shared/redisq      Redis stream queue helper
internal/shared/wsevents    release/debug event publisher
```

`internal/server/server.go` is the composition root. It creates repositories,
usecases, handlers, and registers routes. When adding a module, wiring belongs
there, not inside `cmd/api`.

## 3. Request lifecycle

A normal HTTP request flows like this:

```text
cmd/api/main.go
  -> server.New(...)
  -> Server.Router()
  -> gin middleware chain
  -> module route
  -> handler method
  -> usecase method
  -> repository method
  -> sqlc generated query / pgx
  -> response through httpx
```

Handler responsibilities:

- Parse path/query/body.
- Read authenticated user from middleware context when needed.
- Call one usecase method.
- Convert the result to a DTO.
- Return success or `httpx.Error`.

Usecase responsibilities:

- Validate business rules.
- Coordinate multiple repository calls.
- Translate repository/domain errors to `httpx.AppError`.
- Keep HTTP, JSON, and SQL details out of the core flow.

Repository responsibilities:

- Call `sqlcdb.Queries` or pgx.
- Translate DB errors to domain/common errors.
- Map SQL models to module domain structs.
- Never return HTTP-specific errors directly unless the surrounding module has
  already established that convention.

## 4. Example: user module

The user module shows the intended pattern clearly.

`domain.go` defines the entity and persistence port:

```go
type User struct {
    ID           uuid.UUID
    Phone        string
    PasswordHash string
    Role         string
}

type Repository interface {
    Create(ctx context.Context, u *User) error
    GetByID(ctx context.Context, id uuid.UUID) (*User, error)
    GetByPhone(ctx context.Context, phone string) (*User, error)
}
```

`usecase.go` depends on `Repository`, not on Postgres:

```go
type usecase struct {
    repo Repository
}

func NewUsecase(repo Repository) Usecase {
    return &usecase{repo: repo}
}
```

`repository.go` adapts sqlc/pgx to the domain interface:

```go
type pgRepo struct {
    queries *sqlcdb.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) Repository {
    return &pgRepo{queries: sqlcdb.New(pool)}
}
```

This is why unit tests can fake the repository without running Postgres.

## 5. Adding a new module

Use this checklist for a new feature, for example `coupon`.

1. Create the module directory:

```bash
mkdir -p internal/modules/coupon
```

2. Add `domain.go`.

Put pure types and the repository interface here:

```go
package coupon

import (
    "context"
    "time"

    "github.com/google/uuid"
)

type Coupon struct {
    ID        uuid.UUID
    Code      string
    Amount    int64
    CreatedAt time.Time
}

type Repository interface {
    Create(ctx context.Context, c *Coupon) error
    GetByCode(ctx context.Context, code string) (*Coupon, error)
}
```

3. Add `usecase.go`.

Put business rules here:

```go
package coupon

import (
    "context"

    "github.com/botspace/backend/internal/shared/httpx"
    "github.com/google/uuid"
)

type Usecase interface {
    Create(ctx context.Context, code string, amount int64) (*Coupon, error)
}

type usecase struct {
    repo Repository
}

func NewUsecase(repo Repository) Usecase {
    return &usecase{repo: repo}
}

func (u *usecase) Create(ctx context.Context, code string, amount int64) (*Coupon, error) {
    if code == "" {
        return nil, httpx.BadRequest("coupon.code_required", "code is required")
    }
    if amount <= 0 {
        return nil, httpx.BadRequest("coupon.invalid_amount", "amount must be positive")
    }

    c := &Coupon{ID: uuid.New(), Code: code, Amount: amount}
    if err := u.repo.Create(ctx, c); err != nil {
        return nil, httpx.Internal(err)
    }
    return c, nil
}
```

4. Add migration files.

```bash
make migrate-new NAME=create_coupons
```

Then fill the generated `*.up.sql` and `*.down.sql`.

5. Add sqlc queries in `queries/coupon.sql`.

Example:

```sql
-- name: CreateCoupon :one
INSERT INTO coupons (id, code, amount)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetCouponByCode :one
SELECT * FROM coupons WHERE code = $1;
```

6. Regenerate sqlc.

```bash
make sqlc
```

7. Add `repository.go`.

Wrap `sqlcdb.Queries` and map DB rows to domain structs.

8. Add `dto.go`, `handler.go`, and `routes.go`.

Keep request/response structs in `dto.go`. Keep Gin-specific code in
`handler.go` and `routes.go`.

9. Wire the module in `internal/server/server.go`.

```go
couponRepo := coupon.NewPostgresRepository(s.pool)
couponUC := coupon.NewUsecase(couponRepo)
coupon.RegisterRoutes(v1, coupon.NewHandler(couponUC), tokens)
```

10. Add focused tests.

Start with usecase unit tests using a fake repository. Add repository tests only
when SQL behavior is non-trivial.

## 6. Error handling convention

Prefer these layers:

```text
repository: pgx/sqlc error -> domain/common error
usecase: domain/common error -> httpx.AppError
handler: error -> httpx.Error(c, err)
```

Common examples:

```go
if errors.Is(err, database.ErrNotFound) {
    return nil, httpx.NotFound("user.not_found", "user not found")
}

if errors.Is(err, ErrPhoneTaken) {
    return nil, httpx.Conflict("user.phone_taken", "phone number already registered")
}

return nil, httpx.Internal(err)
```

Use stable error codes like `module.reason`. Frontends can rely on those codes.

## 7. SQL and migrations

This project uses both migrations and sqlc:

```text
migrations/*.up.sql       schema changes
migrations/*.down.sql     rollback changes
queries/*.sql             typed query source
internal/shared/database/sqlc/*.go generated output
```

When changing DB shape:

1. Create a migration.
2. Update or add sqlc queries.
3. Run `make sqlc`.
4. Adjust repository mappings.
5. Run tests.

Do not manually edit generated files in `internal/shared/database/sqlc`.

## 8. Configuration

Config comes from:

```text
real env -> .env -> configs/config.yaml -> defaults
```

Most backend variables use the `APP_` prefix. Examples:

```text
APP_ENV=development
APP_DB_HOST=localhost
APP_DB_AUTO_MIGRATE=true
APP_JWT_PRIVATE_KEY_PATH=keys/jwt_private.pem
APP_JWT_PUBLIC_KEY_PATH=keys/jwt_public.pem
APP_CRYPTO_KEY=change-me-in-production
APP_INTERNAL_TOKEN=change-me-internal
```

Production rules:

- Set `APP_ENV=production`.
- Set real JWT keys; do not use development keys.
- Set `APP_DB_AUTO_MIGRATE=false` and run migrations as a deploy step.
- Restrict CORS with `APP_HTTP_CORS_ORIGINS`.
- Replace `APP_CRYPTO_KEY` and `APP_INTERNAL_TOKEN`.

## 9. Local workflow

Install Go and the tools expected by the Makefile. The module currently targets
the Go version declared in `go.mod`.

Common commands:

```bash
make db-up          # start Postgres from docker-compose
make gen-keys       # create local RS256 keys if needed
make run            # run API
make run-worker     # run worker
make test           # unit tests with race detector
make sqlc           # regenerate typed queries
make swag           # regenerate Swagger docs
```

If `go` is not installed, install Go first before running backend commands.

## 10. How to learn Go through this codebase

Read the code in this order:

1. `cmd/api/main.go`

   Learn process startup, config loading, DB connection, migrations, shutdown.

2. `internal/server/server.go`

   Learn dependency injection by hand. This is where repositories and usecases
   become a running HTTP API.

3. `internal/modules/user`

   Learn the base clean architecture pattern: domain, usecase, repository,
   handler, DTO, routes.

4. `internal/modules/auth`

   Learn cross-module usecase dependency, JWT, refresh tokens, OTP flow.

5. `queries/users.sql` and generated `internal/shared/database/sqlc/users.sql.go`

   Learn how SQL becomes typed Go methods.

6. `internal/shared/httpx`

   Learn the project-wide error and response format.

7. `internal/shared/middleware`

   Learn Gin middleware, auth context, request timeout, CORS, rate limiting.

8. A larger module such as `project`, `release`, or `template`

   Learn orchestration across modules, engine deploy logic, and integration
   boundaries.

Practice tasks:

- Add a small read-only endpoint to an existing module.
- Add a usecase test with a fake repository.
- Add a new sqlc query and map it in `repository.go`.
- Add a new module with one table and one CRUD endpoint.
- Add a migration and rollback it locally.

## 11. Code review checklist

Before opening a PR:

- Business rule is in usecase, not handler.
- Handler does not call sqlc/pgx directly.
- Repository does not depend on Gin.
- New DB change has both up and down migrations.
- New SQL query was regenerated through sqlc.
- Errors use stable `module.reason` codes.
- Auth and role middleware are placed in `routes.go`.
- Secrets are not logged or returned in DTOs.
- Unit tests cover non-trivial business rules.
- `make test` passes locally.

## 12. Differences from the base `jst-go` template

`jst-go` is a small clean architecture starter. This backend keeps the same
shape, but has grown into a platform backend:

- More modules: projects, releases, templates, CMS, payments, credentials, KYC,
  tickets, webhooks, admin, debug, messages.
- sqlc is actively used for many repositories.
- There is a worker process in `cmd/worker`.
- The server can degrade gracefully when optional dependencies like Mongo or
  engine Redis are unavailable.
- There are integration points for Kubernetes deploys, object storage, Redis
  events, Mongo CMS documents, payment gateways, and agent/dashboard clients.

When in doubt, follow the local code first. Use `jst-go` as the architectural
baseline, but match this repository's existing module conventions.
