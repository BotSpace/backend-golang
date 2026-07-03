# AGENTS.md — AI agent guide for jst-go

Bu fayl AI agentlar (Claude Code, Cursor, Copilot, Codex va boshqalar) uchun. Loyihada **mos** va **maintainable** kod yozish qoidalari shu yerda. Inson dasturchilarga ham foydali.

## 1. Loyiha haqida qisqacha

**jst-go** — production-tayyor Go template, **feature-modular clean architecture** asosida. Stack: `gin + pgx + sqlc + golang-migrate + viper + zap`. Maqsadi: kichik loyihada overkill bo'lmaslik, lekin katta loyihada o'sa olish.

Module nomi: `github.com/botspace/backend` (yangi loyihada o'zingiznikiga almashtiriladi).

## 2. Direktoriya strukturasi — qoida

```
cmd/api/                    # entry point — faqat main.go
internal/
  modules/<name>/           # har feature shu yerda, self-contained
    domain.go               # entity + Repository interface
    repository.go           # pgx + sqlc implementatsiya (adapter)
    usecase.go              # business logic — faqat domain interface'ga bog'liq
    dto.go                  # request/response struct'lari, validate teglari
    handler.go              # gin handlerlar
    routes.go               # RegisterRoutes funksiyasi
  shared/                   # cross-cutting infra — modulga tegishli emas
    config/                 # viper config loader
    database/               # pgxpool + migrate + sqlc generated kod
    httpx/                  # AppError + standart response shape
    logger/                 # zap wrapper
    middleware/             # logger, recovery, auth (TokenVerifier interface)
    validator/              # singleton validator
  server/                   # DI wiring + router
migrations/                 # *.up.sql / *.down.sql (golang-migrate)
queries/                    # sqlc input (.sql fayllar :one/:many/:exec)
configs/config.yaml         # default config
```

### Qaerga nima yoziladi

| Vazifa | Joy |
|---|---|
| Yangi entity (User, Order, Post) | `internal/modules/<name>/` — yangi papka |
| Yangi business logic operatsiya | mavjud `<name>/usecase.go` |
| Yangi HTTP endpoint | `<name>/handler.go` + `<name>/routes.go` |
| Yangi SQL query | `queries/<table>.sql` → `make sqlc` |
| Yangi jadval | `make migrate-new NAME=...` → `migrations/` |
| Cross-cutting helper (har modul ishlatadigan) | `internal/shared/<area>/` |
| pkg-da yangi public API | **kerak emas** — `pkg/` papkasi yo'q, hammasi `internal/` |

## 3. Arxitektura qoidalari — buzilmasin

### 3.1 Bog'lanish yo'nalishi (dependency direction)

```
handler ──> usecase ──> domain (Repository interface)
              │              ▲
              ▼              │
         dto + httpx     repository (pgx/sqlc) implementatsiya qiladi
```

- **Usecase faqat interface'ga bog'lanadi** (`Repository`, `TokenVerifier`). Concrete tipga (`*pgRepo`) bog'lanmaydi.
- **Domain (`domain.go`) hech narsaga bog'lanmaydi** — `database/sql`, `gin`, `zap`, `pgx` import qilmaydi.
- **Repository concrete tipi** (`pgRepo`) — package'dan tashqariga **eksport qilinmaydi**. Faqat `NewPostgresRepository(pool) Repository` eksport qilinadi.

### 3.2 Modullararo bog'lanish

- Modullar **bir-birining concrete tipiga** murojaat qilmaydi. Faqat **eksport qilingan interfeyslar** orqali.
- Misol: `auth` moduli `user.Usecase` interfeysiga bog'lanadi (concrete `*usecase` ga emas).
- Circular import paydo bo'lsa — yangi interface yarat (`shared/middleware/auth.go`'da `TokenVerifier` shu sababli).

### 3.3 Shared paketga nima qo'shiladi

`internal/shared/` ga **kamida 2 modul ishlatishi kerak**. Bitta modul uchun — shu modul ichida qoldir.

## 4. Database qoidalari

### 4.1 Query yozish workflow'i

**Har doim sqlc orqali** — manual pgx queryni `repository.go`'da yozma (dynamic WHERE bundan istisno).

```sql
-- queries/posts.sql

-- name: CreatePost :one
INSERT INTO posts (user_id, title, body) VALUES ($1, $2, $3) RETURNING *;

-- name: GetPostByID :one
SELECT * FROM posts WHERE id = $1;

-- name: ListPostsByUser :many
SELECT * FROM posts WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: DeletePost :execrows
DELETE FROM posts WHERE id = $1 AND user_id = $2;
```

Keyin:
```bash
make sqlc
```

Repository'da:
```go
func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*Post, error) {
    row, err := r.queries.GetPostByID(ctx, id)
    if errors.Is(err, pgx.ErrNoRows) {
        return nil, database.ErrNotFound
    }
    if err != nil {
        return nil, fmt.Errorf("get post: %w", err)
    }
    p := fromSQLC(row)
    return &p, nil
}
```

### 4.2 sqlc tag qo'llanmasi

| Tag | Qachon |
|---|---|
| `:one` | Aniq 1 qator (yo'q bo'lsa `pgx.ErrNoRows`) |
| `:many` | Slice qaytaradi |
| `:exec` | UPDATE/DELETE — RowsAffected kerak emas |
| `:execrows` | UPDATE/DELETE — `(int64, error)` — "topilmadi" tekshiruvi uchun |

### 4.3 Generated kodga TEGMA

`internal/shared/database/sqlc/*.go` — **DO NOT EDIT** ushbu kommentariy bilan. SQL'ni o'zgartir, `make sqlc` qayta yurit.

### 4.4 Migration qoidalari

- Yaratish: `make migrate-new NAME=add_orders_status`
- Har migration **reversible** bo'lsin — `up.sql` va `down.sql` ikkalasi to'liq.
- Production'da **destructive migration** (DROP COLUMN, RENAME) — alohida deploy qiladi (multi-step), bitta bo'lib emas.
- Migration faylni qo'lda **o'zgartirma** (push qilingan bo'lsa) — yangi migration yoz.

### 4.5 Domain ↔ persistence model mapping

sqlc'ning model (`sqlcdb.User`) **domain.User**'dan alohida. Repository'da convertor funksiya yoz:

```go
func fromSQLC(r sqlcdb.User) User {
    return User{ID: r.ID, Email: r.Email, ...}
}
```

Identik bo'lsa ham — bu chegara muhim. Sxema o'zgarsa, domain shokga uchramaydi.

### 4.6 Transaction

Pool'dan `Begin(ctx)` qil, sqlc'ning `WithTx(tx)` orqali queries ol:

```go
tx, err := r.pool.Begin(ctx)
if err != nil { return err }
defer tx.Rollback(ctx)  // commit bo'lsa noop

q := r.queries.WithTx(tx)
if _, err := q.CreatePost(ctx, ...); err != nil { return err }
if err := q.IncrementCount(ctx, userID); err != nil { return err }
return tx.Commit(ctx)
```

Buning uchun `pgRepo` strukturasiga `pool *pgxpool.Pool` qaytaring.

## 5. Xatolar — AppError pattern

### 5.1 Usecase qatlamida

Domain xatosi → `httpx.AppError` qaytar:

```go
func (u *usecase) GetByID(ctx context.Context, id uuid.UUID) (*User, error) {
    user, err := u.repo.GetByID(ctx, id)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, httpx.NotFound("user.not_found", "user not found")
        }
        return nil, httpx.Internal(err)
    }
    return user, nil
}
```

Mavjud helperlar: `httpx.NotFound`, `BadRequest`, `Unauthorized`, `Forbidden`, `Conflict`, `Internal`.

### 5.2 Handler qatlamida

```go
if err != nil {
    httpx.Error(c, err)   // AppError bo'lsa to'g'ri status; aks holda 500
    return
}
```

### 5.3 Code naming konvensiyasi

`<resource>.<reason>` — snake_case:
- `user.not_found`
- `user.email_taken`
- `auth.invalid_token`
- `auth.invalid_credentials`
- `request.malformed`
- `request.invalid`

Frontend bu kodlarga binoan localized xabar ko'rsatadi.

### 5.4 Xato yozishda qoidalar

- **Internal xatoni clientga ko'rsatma**: `httpx.Internal(err)` faqat `"internal server error"` qaytaradi, lekin loglarda to'liq.
- **fmt.Errorf("xxx: %w", err)** bilan o'rab boring (wrap), `%v` emas.
- **panic ishlatma** — `httpx.Internal(err)` qaytar.

## 5b. Swagger/OpenAPI annotatsiyalari — MAJBURIY

**Har bir HTTP handler funksiyasi yuqorisida swag annotatsiyasi bo'lishi shart.** Hech qachon annotatsiyasiz endpoint qo'shma — `/swagger/index.html` to'liq bo'lib turishi kerak.

### 5b.1 Standart shablon

```go
// MethodName godoc
// @Summary      Short title (one line)
// @Description  Longer explanation — what it does, side effects.
// @Tags         <module>
// @Accept       json
// @Produce      json
// @Security     BearerAuth          // faqat auth talab qiladigan endpointlarda
// @Param        body  body      <RequestType>           true   "Description"
// @Param        id    path      string                  true   "User ID (uuid)"
// @Param        page  query     int                     false  "Page number"
// @Success      200   {object}  <ResponseType>
// @Success      204
// @Failure      400   {object}  httpx.ErrorResponse
// @Failure      401   {object}  httpx.ErrorResponse
// @Failure      404   {object}  httpx.ErrorResponse
// @Failure      500   {object}  httpx.ErrorResponse
// @Router       /<path>  [<method>]
func (h *Handler) MethodName(c *gin.Context) { ... }
```

### 5b.2 Annotation qoidalari

| Tag | Majburiy | Tushuntirish |
|---|---|---|
| `@Summary` | ✅ | Bitta jumla, til — inglizcha (SwaggerUI universal). |
| `@Description` | ✅ | 1-3 jumla — endpoint nima qiladi, side effect nima. |
| `@Tags` | ✅ | Modul nomi — Swagger UI'da gruppalash uchun. |
| `@Accept` | request body bo'lsa | Odatda `json`. |
| `@Produce` | response bo'lsa | Odatda `json`. |
| `@Security` | auth kerakmi | `BearerAuth` — middleware.Auth bo'lgan endpointlarda. |
| `@Param` | parametr bor bo'lsa | `name location type required "desc"` formatda. |
| `@Success` | ✅ kamida bitta | Status code + qaytadigan tip. |
| `@Failure` | barchasi | Mumkin bo'lgan **barcha** xato statuslari (400, 401, 404, 409, 500). |
| `@Router` | ✅ | Yo'l + method, masalan `/users/me [get]`. |

### 5b.3 Refresh workflow

Handler annotatsiyasi yoki DTO o'zgarsa:

```bash
make swag      # docs/ qayta generatsiya bo'ladi
```

`docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml` — **generated, qo'lda tegma**.

### 5b.4 Tekshirish

Server'ni ishga tushirib (`make dev`), `http://localhost:8080/swagger/index.html` ochib ko'r:

- Yangi endpoint ro'yxatda bormi
- Request body misoli to'g'rimi
- Barcha mumkin bo'lgan xato statuslari ro'yxatdami
- `Authorize` tugmasi bilan token kiritib, protected endpoint ishlayaptimi

### 5b.5 Production rejim

`server.go`'da swagger faqat `cfg.Env != "production"` paytda mount qilinadi. Production'da `/swagger/*` 404 qaytaradi — bu xavfsizlik chorasi.

Agar production'da ham kerak bo'lsa (internal admin uchun), shartni o'zgartir va **kamida basic auth** bilan himoyala.

## 6. HTTP handler qoidalari

### 6.1 Standart shablon

```go
func (h *Handler) Create(c *gin.Context) {
    var req CreateRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        httpx.Error(c, httpx.BadRequest("request.malformed", "invalid request body"))
        return
    }
    if details, err := validator.Struct(req); err != nil {
        httpx.ErrorWithDetails(c, httpx.BadRequest("request.invalid", "validation failed"), details)
        return
    }

    result, err := h.uc.Create(c.Request.Context(), req)
    if err != nil {
        httpx.Error(c, err)
        return
    }
    httpx.OK(c, http.StatusCreated, toResponse(result))
}
```

- Handler **biznes logikasini bajarmaydi** — faqat parse + validate + delegate + respond.
- Har doim `c.Request.Context()`'ni usecase'ga uzating.
- Kontekstga maxsus qiymat (user_id) qo'shmaslik — middleware'dan `middleware.UserID(c)` orqali oling.

### 6.2 DTO va domain — alohida

`dto.go`'da request/response struct, `domain.go`'da entity. Ularni **aralashtirma**. Convertor funksiya: `toResponse(entity)`.

```go
type UserResponse struct {
    ID        string    `json:"id"`
    Email     string    `json:"email"`
    Name      string    `json:"name"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

func toResponse(u *User) UserResponse {
    return UserResponse{ID: u.ID.String(), Email: u.Email, ...}
}
```

`PasswordHash` kabi maydonlarni javobga **chiqarma**.

### 6.3 Validatsiya teglari

```go
type RegisterRequest struct {
    Email    string `json:"email" validate:"required,email"`
    Name     string `json:"name" validate:"required,min=1,max=100"`
    Password string `json:"password" validate:"required,min=8,max=128"`
}
```

JSON tag nomi xato javobida ko'rinadi (validator.RegisterTagNameFunc shunga sozlangan).

## 7. Yangi modul qo'shish — checklist

Misol uchun `post` moduli kerak bo'lsin:

1. **Migration**:
   ```bash
   make migrate-new NAME=create_posts
   # up.sql va down.sql ni yozing
   make migrate-up
   ```

2. **Query**:
   ```bash
   # queries/posts.sql yozing
   make sqlc
   ```

3. **Module fayllar** (`internal/modules/post/`):
   - `domain.go` — `Post` entity + `Repository` interface
   - `repository.go` — `pgRepo` + sqlc mapping
   - `usecase.go` — `Usecase` interface + concrete
   - `dto.go` — request/response struct'lar
   - `handler.go` — gin handlers
   - `routes.go` — `RegisterRoutes(rg, h, verifier)`

4. **Wiring** (`internal/server/server.go`):
   ```go
   postRepo := post.NewPostgresRepository(s.pool)
   postUC := post.NewUsecase(postRepo)
   post.RegisterRoutes(v1, post.NewHandler(postUC), tokens)
   ```

5. **Test** — kamida usecase uchun `fakeRepo` bilan unit test.

## 8. Logging qoidalari

`zap` ishlatiladi (`internal/shared/logger`):

```go
log.Info("user created",
    logger.String("user_id", u.ID.String()),
    logger.String("email", u.Email),
)

log.Error("payment failed",
    logger.Err(err),
    logger.String("order_id", orderID.String()),
)
```

- **Production'da** sezgir ma'lumotlarni (password, token, PII) logga yozma.
- **Strukturali field** ishlat, message ichida `%s` bilan format qilma.
- `log.Fatal` faqat startup'da (DB connect, config load) ishlatiladi.

## 9. Test qoidalari — MAJBURIY

**Har bir yangi API endpoint uchun test yozish shart.** Test yozilmagan endpoint — PR'ga qabul qilinmaydi. Bu qoida AI agent uchun ham, inson uchun ham.

### 9.1 Test piramidasi

| Qatlam | Test turi | Qachon | Tezligi |
|---|---|---|---|
| Domain entity | unit | logika murakkab bo'lsa | mikrosaniya |
| **Usecase** | **unit (fakeRepo)** | **HAR DOIM** | millisaniya |
| **Handler** | **HTTP test (httptest)** | **HAR DOIM** | millisaniya |
| Repository | integration (testcontainers postgres) | har repository metodi uchun | soniya |
| End-to-end | docker-compose + real HTTP | ihtiyoriy, smoke uchun | soniya-daqiqa |

**Minimum kafolat**: usecase + handler test'lari **har endpoint** uchun bor.

### 9.2 Usecase unit test — namuna

`internal/modules/user/usecase_test.go` ga qarang. Shablon:

```go
type fakeRepo struct { /* maps */ }
func (r *fakeRepo) Create(ctx context.Context, u *user.User) error { ... }
// barcha Repository metodlari

func TestRegister_DuplicateEmail(t *testing.T) {
    uc := user.NewUsecase(newFakeRepo())
    // ...
    _, err := uc.Register(context.Background(), in)

    var ae *httpx.AppError
    if !errors.As(err, &ae) || ae.Code != "user.email_taken" {
        t.Errorf("expected email_taken AppError, got %v", err)
    }
}
```

### 9.3 Handler HTTP test — namuna

`internal/modules/auth/handler_test.go` ga qarang. Shablon:

```go
func newTestServer(t *testing.T) *gin.Engine {
    gin.SetMode(gin.TestMode)
    repo := newFakeUserRepo()
    uc := user.NewUsecase(repo)
    tokens := auth.NewTokenIssuer(config.JWTConfig{Secret: "test", ...})
    authUC := auth.NewUsecase(uc, tokens)
    r := gin.New()
    auth.RegisterRoutes(r.Group("/api/v1"), auth.NewHandler(authUC))
    return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
    buf, _ := json.Marshal(body)
    req := httptest.NewRequest(method, path, bytes.NewReader(buf))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)
    return w
}

func TestLogin_InvalidCredentials(t *testing.T) {
    r := newTestServer(t)
    // register first
    _ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", ...)
    // wrong password
    w := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", auth.LoginRequest{
        Email: "...", Password: "wrong",
    })
    if w.Code != http.StatusUnauthorized {
        t.Fatalf("got %d, want 401", w.Code)
    }
}
```

### 9.4 Har endpoint uchun test cover qilish kerak holatlar

- ✅ **Happy path** — to'g'ri request, 2xx javob, response body to'g'ri.
- ✅ **Validation error** — invalid body → 400 + `request.invalid` code + details.
- ✅ **Auth error** — protected endpoint uchun token yo'q/buzilgan → 401.
- ✅ **Not found** — mavjud bo'lmagan resurs → 404 + to'g'ri code.
- ✅ **Conflict** — masalan duplicate email → 409.
- ✅ **Forbidden** — boshqa userning resursiga tegishga urinish (agar RBAC bo'lsa) → 403.

Minimum **kamida happy path + 1 ta xato holat** har endpoint uchun.

### 9.5 Integration test (repository)

[testcontainers-go](https://github.com/testcontainers/testcontainers-go) bilan real postgres ko'tarib `*pgxpool.Pool` ga `NewPostgresRepository`'ni qo'llang. **Mock DB ishlatma** — sxema xatolari prod'da paydo bo'ladi.

```go
func setupDB(t *testing.T) *pgxpool.Pool {
    ctx := context.Background()
    pg, err := postgres.Run(ctx, "postgres:16-alpine",
        postgres.WithDatabase("test"),
        postgres.WithUsername("test"),
        postgres.WithPassword("test"),
        testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
    )
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = pg.Terminate(ctx) })

    dsn, _ := pg.ConnectionString(ctx, "sslmode=disable")
    pool, err := pgxpool.New(ctx, dsn)
    if err != nil { t.Fatal(err) }
    t.Cleanup(pool.Close)

    if err := database.MigrateUp(dsn, "file://../../migrations"); err != nil {
        t.Fatal(err)
    }
    return pool
}
```

### 9.6 Test naming

`Test<Method>_<Case>` formatda — case'lar inglizcha:

- `TestRegister_Success`
- `TestRegister_DuplicateEmail`
- `TestLogin_InvalidCredentials`
- `TestRefresh_InvalidToken`
- `TestMe_Unauthorized`

### 9.7 CI da ishlash

```bash
make test    # race detector bilan
make cover   # coverage.html
```

Pull request oldidan **`make test` muvaffaqiyatli o'tishi shart**. CI'da ham shu buyruq.

### 9.8 Yo'l qo'yib bo'lmaydigan shortcut'lar

- ❌ "Vaqt yo'q, keyin yozaman" — endpoint test bilan birga keladi.
- ❌ "Trivialdir" — trivial bo'lsa, test ham trivial — yozish 30 soniya.
- ❌ Mock DB (`sqlmock`) repository test uchun — real postgres ishlat.
- ❌ Faqat happy path — kamida 1 ta xato holat ham.
- ❌ `t.Skip(...)` flaky test'ga — sababini topib fix qil.

## 10. Konfiguratsiya

- Yangi config qiymat qo'shilsa: `Config` struct'ga maydon + `setDefaults(v)` ga default qiymat + `configs/config.yaml`'ga misol + `.env.example`'ga `APP_*` versiya.
- Hech qachon **secret'larni codega yozma**. `APP_JWT_SECRET=...` env orqali.
- Production validatsiyasi: `Config.validate()` ichida `if env == "production" { ... }` bloki.

## 11. Naming konvensiyalari

| Element | Konvensiya | Misol |
|---|---|---|
| Package | qisqa, single-word, lowercase | `user`, `auth`, `httpx` |
| Interface | rolga ko'ra | `Repository`, `Usecase`, `TokenVerifier` |
| Constructor | `New<Type>` | `NewUsecase`, `NewPostgresRepository` |
| Error variable | `Err<Reason>` | `ErrEmailTaken`, `ErrNotFound` |
| HTTP route | RESTful, kebab-case yo'q, plural | `/users/me`, `/auth/login` |
| SQL query | PascalCase camelCase ham bo'lishi mumkin | `GetUserByEmail`, `ListPostsByUser` |
| Migration fayl | `seq_snake_case` | `000003_add_user_roles.up.sql` |
| AppError code | `<resource>.<reason>` | `user.not_found` |

## 12. Style — Go idioms

- **Faqat zarur paytda commentariy yoz**. Identifikator nomi yetarli bo'lsa, komment qo'shma.
- **Komment WHY tushuntirishi kerak, WHAT emas**. "Increments counter" — yomon. "Increments counter atomically to avoid race during high-load checkout" — yaxshi.
- **Receiver name**: `func (r *pgRepo) ...` — 1-3 harf, tip nomidan kelib chiqsin.
- **Error tekshiruvi**: `if err != nil { return ... }` — har doim ketma-ket, nested emas.
- **Context har doim birinchi parametr**: `func Do(ctx context.Context, ...)`.
- **defer ishlatish** resource cleanup uchun (`defer pool.Close()`, `defer tx.Rollback(ctx)`).
- **time.Duration** ishlat — `int` seconds emas (`timeout time.Duration`).

## 13. Backwards compatibility shimlar — TAQIQLANGAN

- Removed maydon uchun `// Deprecated` qoldirib o'tirma — o'chiraver.
- `// old: ...` kabi komment yozma.
- Versionlangan API kerak bo'lsa — `v1`, `v2` URL'larda emas, **alohida modul** sifatida.

## 14. Build/run buyruqlari (Makefile)

```bash
make help              # barcha buyruqlar
make install-tools     # air, migrate, sqlc, swag, golangci-lint o'rnatish
make db-up             # postgres docker'da ko'tarish
make dev               # air bilan hot reload
make run               # oddiy go run
make build             # static binary
make test              # race detector bilan
make cover             # coverage.html
make lint              # golangci-lint
make fmt               # gofmt + goimports
make sqlc              # SQL → Go generation
make swag              # handler annotatsiyalardan OpenAPI/Swagger generate
make swag-fmt          # annotatsiyalarni formatlash
make migrate-new NAME=...
make migrate-up / migrate-down / migrate-force V=...
make up / down         # to'liq docker stack
```

## 15. Agent uchun maxsus eslatmalar

- **Module nomi**: yangi loyihada `github.com/botspace/backend` o'rniga real nom turishi kerak. Refactor paytida tekshiring.
- **DO NOT** `vendor/` papkasini commit qiling — `.gitignore`'da bor.
- **DO NOT** `.env` ni commit qiling — faqat `.env.example`.
- **DO NOT** generated sqlc kodini qo'lda tahrirla.
- **DO NOT** yangi feature uchun `pkg/` ochma — hamma kod `internal/`'da.
- **DO NOT** GORM, sqlx, yoki boshqa ORM qo'shma — sqlc + pgx yetadi.
- **DO NOT** `log.Println` ishlat — `zap` orqali.
- **DO NOT** global state qo'shma — DI orqali server.go'da wire qil.
- **DO NOT** yangi endpoint qo'sh **swag annotatsiyasiz** — `make swag` tushib qoladi.
- **DO NOT** yangi endpoint qo'sh **test yozmasdan** — minimum 1 happy path + 1 error case.
- **DO NOT** `docs/` papkasini qo'lda tahrirla — `make swag` ishlat.
- **DO** har feature uchun `domain.go` da `Repository` interface yoz.
- **DO** har repository metodida `errors.Is(err, pgx.ErrNoRows)` tekshir.
- **DO** har handler'da DTO validatsiyani 2 qadamga ajrat: `ShouldBindJSON` → `validator.Struct`.
- **DO** har handler funksiyasi yuqorisida **`// @Summary ... @Router`** annotatsiyasini yoz.
- **DO** har handler uchun **HTTP test** yoz (`httptest.NewRecorder` + fakeRepo bilan).
- **DO** annotatsiya/DTO o'zgargach `make swag` yurit, generated `docs/` ni commitga qo'sh.
- **DO** har commit oldidan `make swag && go build ./... && go vet ./... && go test ./...` ishlatib ko'r.

## 16. Production checklist (deploy oldidan)

- [ ] `APP_JWT_SECRET` 32+ baytli random (`openssl rand -hex 32`).
- [ ] `APP_ENV=production`.
- [ ] Migration alohida job/init container'da, `main.go`'dan olib tashlangan.
- [ ] HTTPS reverse proxy (nginx/caddy/traefik) orqasida.
- [ ] DB credentialslar secret manager'da.
- [ ] Rate limiting middleware qo'shilgan.
- [ ] OpenTelemetry/Prometheus tracing — kerak bo'lsa `internal/shared/middleware/`ga.
- [ ] Backup strategiyasi (postgres pg_dump cron yoki managed service).
- [ ] Healthcheck endpoint (`/healthz`) load balancer'da ulangan.

---

**Savol bo'lsa**: oldin `README.md`'ni o'qing, keyin shu faylni. Ikkalasida ham yo'q bo'lsa — issue yarating.
