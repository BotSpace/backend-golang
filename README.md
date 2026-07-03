# goapp — Go Clean Architecture Template

Production-tayyor Go template. Kichik loyihalarda overkill emas, kattalarida esa to'g'ridan-to'g'ri kengaytirish mumkin. Stack: **gin + pgx + golang-migrate + viper + zap + air + docker**. sqlc tayyor turibdi — istasangiz yoqasiz.

## Asosiy g'oyalar

- **Feature-modular** struktura: har feature `internal/modules/<name>/` ichida — `domain.go`, `repository.go`, `usecase.go`, `handler.go`, `dto.go`, `routes.go`. Bitta featureda ishlash bitta papkani ochish bilan tugaydi.
- **Clean architecture qatlamlari** (har modulda):
  - `domain.go` — pure entity + `Repository` interfeysi (port).
  - `repository.go` — pgx implementatsiyasi (adapter). Hand-written queries, lekin sqlc'ga oson o'tasiz.
  - `usecase.go` — biznes logika. Faqat `Repository` interfeysiga bog'liq.
  - `handler.go` + `dto.go` + `routes.go` — HTTP delivery.
- **Shared infra** `internal/shared/`: `config`, `logger`, `database`, `httpx` (response/error), `middleware`, `validator`.
- **AppError** orqali xato pattern: domain xatosi → HTTP javob mapping `httpx.Error()`'da.
- **Graceful shutdown**, **migrationlar avtomatik startda** (yoki CLI orqali qo'lda).
- **Zero generation required** ishga tushishi uchun: `sqlc` opsional, manual pgx queries default.

## Tezkor start

```bash
# 1. Module nomini o'zgartiring (default github.com/botspace/backend)
# go.mod va barcha importlarni replace qiling, masalan:
#   find . -type f -name '*.go' -exec sed -i '' 's|github.com/botspace/backend|github.com/youruser/yourproject|g' {} +
#   go mod edit -module github.com/youruser/yourproject

# 2. Dev toollar
make install-tools     # air, migrate, sqlc, golangci-lint

# 3. Database
make db-up             # postgresni docker compose orqali ko'taring

# 4. Run
make dev               # air bilan hot reload
# yoki:
make run

# 5. Health check
curl http://localhost:8080/healthz
```

`make dev` ishga tushishi bilan migrationlar avtomatik qo'llaniladi (`internal/shared/database/migrate.go`). Production'da bu xatti-harakatni o'zgartirib, CLI orqali boshqarishingiz mumkin.

### Swagger UI

Server ishlayotganda (development rejimi): http://localhost:8080/swagger/index.html

Handler annotatsiyalari o'zgartirilgach docs'ni qayta generatsiya qiling:
```bash
make swag
```

Generated fayllar: `docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` (qo'lda tegmaslik kerak).

## API namunasi

```bash
# Register
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","name":"A","password":"supersecret"}'

# Login
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"a@b.com","password":"supersecret"}'

# Me (access token bilan)
curl http://localhost:8080/api/v1/users/me \
  -H 'Authorization: Bearer <ACCESS_TOKEN>'

# Refresh (rotation — eski token bekor qilinadi)
curl -X POST http://localhost:8080/api/v1/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<REFRESH_TOKEN>"}'

# Logout (refresh tokenni revoke qiladi)
curl -X POST http://localhost:8080/api/v1/auth/logout \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"<REFRESH_TOKEN>"}'

# Admin: userlar ro'yxati (filter + pagination)
curl 'http://localhost:8080/api/v1/users?search=ali&limit=20&offset=0' \
  -H 'Authorization: Bearer <ADMIN_ACCESS_TOKEN>'

# Admin: rol o'zgartirish
curl -X PATCH http://localhost:8080/api/v1/users/<USER_ID>/role \
  -H 'Authorization: Bearer <ADMIN_ACCESS_TOKEN>' \
  -H 'Content-Type: application/json' \
  -d '{"role":"admin"}'
```

> **Rollar (RBAC):** har user `role` ustuniga ega (default `user`). Access token ichida `role` claim bo'ladi; `middleware.RequireRole("admin")` admin-only endpointlarni himoya qiladi. Birinchi adminni DB orqali bering: `UPDATE users SET role='admin' WHERE email='...';`

## WebSocket

`internal/modules/ws/` — bitta **hub** (yagona goroutine, locksiz state) barcha ulanishlarga xabar tarqatadi. Handshake **auth** bilan himoyalangan: access token `?token=` query yoki `Authorization` header orqali tekshiriladi (brauzerlar WS'da header yubora olmaydi), upgrade'dan **oldin** — noto'g'ri token oddiy `401` qaytaradi.

Production xususiyatlari: ping/pong heartbeat + read/write deadline, `maxMessageSize` limit, sekin clientni hub'ni bloklamasdan drop qilish, `CheckOrigin` (HTTP CORS ro'yxatidan), `ctx` orqali graceful shutdown.

```js
// Brauzer
const token = "<ACCESS_TOKEN>";
const ws = new WebSocket(`ws://localhost:8080/api/v1/ws?token=${token}`);
ws.onmessage = (e) => console.log(JSON.parse(e.data)); // {type:"message", from:"<uid>", body:"..."}
ws.onopen = () => ws.send("salom");                    // hamma ulangan clientlarga broadcast
```

```bash
# CLI (wscat)
wscat -c "ws://localhost:8080/api/v1/ws?token=<ACCESS_TOKEN>"
```

### Rooms (guruhlar)

Hub uch xil ko'lamda yuboradi: **hammaga**, **bitta userga**, va **room**ga (ko'p user qo'shiladigan guruh). Client kichik JSON protokol bilan boshqaradi:

```js
ws.send(JSON.stringify({ type: "join",  room: "general" }));            // guruhga qo'shilish
ws.send(JSON.stringify({ type: "leave", room: "general" }));            // chiqish
ws.send(JSON.stringify({ type: "message", room: "general", body: "hi" })); // guruhga xabar
ws.send(JSON.stringify({ type: "message", body: "hi" }));               // hammaga (room yo'q)
ws.send("salom");                                                       // JSON bo'lmasa -> hammaga broadcast
```

Kelgan xabar a'zolarga `{type:"message", from:"<uid>", room:"general", body:"hi"}` ko'rinishida yetadi. Uzilganda client barcha roomlardan avtomatik chiqariladi.

**Server tomondan** (kod ichidan) — app logikasi bilan boshqarish:
```go
hub.JoinUser(userID, "order-42")          // userning barcha ulanishlarini room'ga qo'sh
hub.LeaveUser(userID, "order-42")
hub.BroadcastToRoom("order-42", payload)   // room a'zolariga yubor
hub.SendToUser(userID, payload)            // bitta userga
hub.Broadcast(payload)                      // hammaga
```

> **Xavfsizlik:** hozir client-side `join` ochiq — istalgan user istalgan room'ga kira oladi. Yopiq guruhlar uchun `hub.go` dagi `readPump` ichidagi `"join"` joyiga **ruxsat tekshiruvini** qo'shing (bu user shu room a'zosimi?), yoki client join'ni o'chirib faqat server-side `hub.JoinUser` bilan boshqaring. Joy `// ponytail:` komment bilan belgilangan.

O'zingizning routingingiz uchun `hub.go` dagi `handleInbound` ni o'zgartiring.

## Yangi modul qo'shish

1. `internal/modules/<name>/` papka yarat.
2. `domain.go` da entity + `Repository` interfeysi.
3. `repository.go` da pgx implementatsiyasi (`NewPostgresRepository`).
4. `usecase.go` da `Usecase` interfeysi va konkret tip.
5. `handler.go`, `dto.go`, `routes.go`.
6. `internal/server/server.go` ichida wiring qo'sh:
   ```go
   widgetRepo := widget.NewPostgresRepository(s.pool)
   widgetUC := widget.NewUsecase(widgetRepo)
   widget.RegisterRoutes(v1, widget.NewHandler(widgetUC), tokens)
   ```
7. Migration qo'sh: `make migrate-new NAME=create_widgets`.

Bu hammasi. Module o'zining ichida self-contained — boshqa modullarga faqat ularning eksport qilingan interfeyslari orqali bog'lanadi (masalan, `auth` modul `user.Usecase` interfeysidan foydalanadi).

## sqlc'ga o'tish (opsional)

Manual pgx queries yetarli — lekin SQL ko'paygach `sqlc` foydali bo'ladi:

```bash
make sqlc      # queries/*.sql -> internal/shared/database/sqlc/*.go
```

Keyin `repository.go` ichidagi pgx kodini sqlc generated wrapperga almashtiring. Domain interfeysi (`Repository`) o'zgarmaydi — usecase tegmasdan qoladi.

## Migrationlar

- Yaratish: `make migrate-new NAME=add_orders`
- Yuqori: `make migrate-up`
- Pastga: `make migrate-down`
- Versiyani majburlash (xato holatda): `make migrate-force V=2`

Migrate library startda avtomatik chaqiriladi (`MigrateUp`), shu sababli local dev'da alohida buyruq kerak emas. Production deploylarda `APP_DB_AUTO_MIGRATE=false` qo'ying va migrationlarni alohida job/CLI orqali (`make migrate-up`) boshqaring — multi-replica deploylarda shu xavfsizroq.

## Konfiguratsiya

Viper `configs/config.yaml` + `APP_*` env vars. Startup'da `.env` fayli ham avtomatik yuklanadi (`.env.example`dan nusxa oling). Ustunlik tartibi: **real env > `.env` > `config.yaml` > defaultlar**. Production'da odatda env-only:

```
APP_DB_HOST=db.prod.local
APP_ENV=production
APP_LOG_LEVEL=info
APP_JWT_PRIVATE_KEY_PATH=/run/secrets/jwt_private.pem
APP_JWT_PUBLIC_KEY_PATH=/run/secrets/jwt_public.pem
```

### JWT kalitlari (RS256)

Tokenlar **RS256** bilan imzolanadi: **private key** imzolaydi, **public key** tekshiradi (asimmetrik — verifierlar hech qachon imzo materialini ushlamaydi). Kalitlar `keys/` papkasida:

```bash
make gen-keys   # keys/jwt_private.pem (0600) + keys/jwt_public.pem
```

- **Development**: kalitlar bo'lmasa, startup'da avtomatik generatsiya qilinadi (`keys/`ga yoziladi). Qulay — `make run` darrov ishlaydi.
- **Production**: avtomatik generatsiya YO'Q. Kalitlar bo'lmasa server ishga tushmaydi (fail-fast).
- **Kalit bir marta yaratiladi** (har muhit uchun: prod uchun bir marta) va secret manager / mounted secret'da saqlanib, **barcha deploylarda qayta ishlatiladi**. Har deployda yangilamang — bu barcha mavjud tokenlarni bekor qiladi (hammani logout qiladi).
- Prodda **dev kalitlarni ishlatmang** — prod uchun alohida o'z kalitingizni yarating.
- **Rotation** (xavfsizlik hodisasi yoki kalit sizib chiqsa) — ataylab qilinadigan ish: yangi kalit bering, eski tokenlar bekor bo'ladi (yumshoq o'tish uchun qisqa access TTL'ga tayaning yoki bir muddat ikkala public keyni qabul qiling).
- Kalitlar **hech qachon git'ga commit qilinmaydi** (`.gitignore`da `keys/*.pem`).

## Testlash

- **Unit**: `internal/modules/user/usecase_test.go`, `internal/modules/auth/handler_test.go` — fake repo/store bilan (Docker kerak emas).
- **Integration**: `internal/modules/user/repository_integration_test.go` — [testcontainers-go](https://github.com/testcontainers/testcontainers-go) bilan real Postgres ko'taradi. `//go:build integration` tegi ostida, Docker talab qiladi.

```bash
make test              # unit testlar (race detector)
make test-integration  # integration testlar (Docker kerak)
make cover             # coverage.html
```

> Colima/OrbStack kabi nostandart Docker socket ishlatsangiz `DOCKER_HOST` ni o'rnating, masalan:
> `export DOCKER_HOST=unix://$HOME/.colima/default/docker.sock`

## Struktura

```
.
├── cmd/api/main.go               # entry point, graceful shutdown
├── configs/config.yaml           # default config
├── internal/
│   ├── modules/
│   │   ├── user/                 # CRUD + me + admin (RBAC) endpoints
│   │   ├── auth/                 # JWT register/login/refresh/logout
│   │   └── ws/                   # authenticated WebSocket hub
│   ├── shared/
│   │   ├── config/               # viper loader
│   │   ├── database/             # pgxpool + migrate
│   │   ├── httpx/                # AppError + response helpers
│   │   ├── logger/               # zap wrapper
│   │   ├── middleware/           # logger, recovery, auth
│   │   └── validator/            # validator instance
│   └── server/server.go          # DI + routing
├── migrations/                   # *.up.sql / *.down.sql
├── queries/                      # sqlc input (optional)
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── sqlc.yaml
└── .air.toml
```

## Xavfsizlik & operatsion eslatmalar

- **JWT kalitlari (RS256)** — private key imzolaydi, public key tekshiradi. Production'da kalitlar majburiy (auto-gen yo'q). Kalit **bir marta** yaratilib barcha deploylarda qayta ishlatiladi (secret manager'da). Batafsil: yuqoridagi "JWT kalitlari" bo'limi.
- **Refresh token revocation + rotation** — refresh tokenlar `jti` bo'yicha `refresh_tokens` jadvalida saqlanadi. `Refresh` har chaqirilganda eski token revoke qilinadi (rotation — replay himoyasi), `/auth/logout` esa tokenni darhol bekor qiladi. Access tokenlar stateless (15m), shuning uchun rol o'zgarishi keyingi refreshda kuchga kiradi. Eskirgan tokenlarni tozalash — bu backend'ning ishi emas: `worker-golang`'ning `auth_cleanup_expired_refresh_tokens` cron job'i (soatiga bir marta) bajaradi.
- **Rate limiting** — `APP_HTTP_RATE_LIMIT_RPS` orqali per-IP token bucket (0 = o'chiq). Bu **instance-local** — bir nechta replica orqasida har biri alohida cheklaydi; global limit kerak bo'lsa Redis'ga ko'chiring.
- **CORS** — `APP_HTTP_CORS_ORIGINS` (default `*`). Production'da aniq originlar yozing.
- **Body limit** — `APP_HTTP_MAX_BODY_BYTES` (default 1 MiB) so'rov tanasini cheklaydi; `MaxHeaderBytes` ham qo'yilgan.
- **Health/Readiness** — `/healthz` (liveness, DB'ga tegmaydi) va `/readyz` (DB ping). k8s probe'lari uchun `/readyz` ishlating.

## Production checklist

- [ ] RS256 kalitlari prod uchun **bir marta** generatsiya qilingan (`make gen-keys`), dev kalit emas; barcha deploylarda qayta ishlatiladi.
- [ ] Private key secret manager / mounted secret'da (git'da emas).
- [ ] `APP_ENV=production` (gin release mode).
- [ ] `APP_DB_AUTO_MIGRATE=false` + migration alohida job/init container'da.
- [ ] `APP_HTTP_CORS_ORIGINS` aniq originlarga cheklangan.
- [ ] `APP_HTTP_RATE_LIMIT_RPS` o'rnatilgan (yoki tashqi WAF/proxy'da).
- [ ] HTTPS reverse proxy (nginx/caddy/traefik) orqasida.
- [ ] DB credentiallar secret manager'da.
- [ ] k8s liveness=`/healthz`, readiness=`/readyz`.
- [ ] OpenTelemetry/Prometheus tracing — kerak bo'lganda `internal/shared/middleware/`ga qo'shing.
