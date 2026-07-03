# PORTING GUIDE — Django → Go (agentlar uchun majburiy qoidalar)

Django backend (`../core/apps/<app>/`) ni Go'ga ko'chiryapmiz. Kod **ko'chirilmaydi** —
idiomatik Go'da qaytadan yoziladi. Namuna sifatida **avval quyidagilarni o'qing**:
`internal/modules/user/`, `internal/modules/project/`, `internal/modules/auth/`.
Ular aynan bizning uslub. Undan chetga chiqmang.

## Modul tuzilishi (har domen uchun)
`internal/modules/<name>/` ichida:
- `domain.go` — entity struct(lar) + `Repository` interface (port) + filter/input structlar.
- `repository.go` — sqlc adapter (`sqlcdb.New(pool)`), domain⇄sqlc konvertatsiya, pgx xatolarini `database.ErrNotFound` ga tarjima.
- `usecase.go` — biznes logika, `Usecase` interface, xatolarni `httpx.*` ga map qilish.
- `dto.go` — request/response structlar (`validate:"..."` teglari) + `toResponse`.
- `handler.go` — gin handlerlar (godoc kommentlari bilan), `middleware.UserID(c)` orqali auth user.
- `routes.go` — `RegisterRoutes(rg *gin.RouterGroup, h *Handler, verifier middleware.TokenVerifier)`.
- `usecase_test.go` — kamida 1-2 ta test, in-memory fake repo bilan (DB kerak emas). `user/usecase_test.go` ga qarang.

## Qat'iy qoidalar
1. **Faqat o'z papkangizga yozing**: `internal/modules/<name>/`, bitta migration fayl, bitta queries fayl. `server.go`, `go.mod`, `sqlc.yaml`, `internal/shared/database/sqlc/*` (generatsiya) ga **TEGMANG** — ularni markaz (men) boshqaradi. `sqlc generate` ni O'ZINGIZ ishga tushirMANG.
2. **Migration**: `migrations/<NNNNNN>_<name>.up.sql` va `.down.sql`. Sizga berilgan raqamdan foydalaning. Toza schema, **UUID PK** (`gen_random_uuid()`), `created_at/updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`. Soft-delete kerak bo'lsa `deleted_at TIMESTAMPTZ`. FK: `owner_id UUID REFERENCES users(id) ON DELETE CASCADE`, `project_id UUID REFERENCES projects(id) ON DELETE CASCADE`. Django `id` (bigint) o'rniga UUID ishlating.
3. **Queries**: `queries/<name>.sql`, sqlc sintaksisi (`-- name: X :one|:many|:exec|:execrows`, `sqlc.arg()`, `sqlc.narg()`). Query nomlari **unikal** bo'lsin (masalan `ListMessages`, `CreateCredential`) — boshqa modul bilan to'qnashmasin. sqlc null uchun `*T`, jsonb uchun `[]byte`, numeric uchun `pgtype.Numeric`, nullable time uchun `pgtype.Timestamptz` beradi — repository.go da shularni domain tipiga o'giring (`user/repository.go` dagi `deref/strPtr/tsPtr/numericToString` namunasi).
4. **Ownership/xavfsizlik**: har amal foydalanuvchiga tegishli. Boshqa modulga import qilmang — SQL'da scope qiling. Masalan project-scoped resurs uchun query: `WHERE project_id = $1 AND EXISTS (SELECT 1 FROM projects p WHERE p.id = $1 AND p.owner_id = $2 AND p.deleted_at IS NULL)`. Ruxsat yo'q bo'lsa `database.ErrNotFound` → 404.
5. **Pul/pgtype.Numeric**: DecimalField → `NUMERIC(20,2)`. Domain'da string sifatida tuting (`numericToString`). Kirim uchun stringni tekshiring.
6. **Enum'lar**: Django TextChoices → Go `const` (string) + validatsiya. Alohida jadval yaratmang.
7. **Wiring snippet**: modulingiz `server.go` ga qanday ulanishini (repo→usecase→handler→RegisterRoutes qatorlari) yakuniy javobingizda **aniq bering** — men markazda qo'shaman.
8. Build: kodingiz kompilyatsiya bo'lishi kerak, lekin sqlc generatsiya markazda bo'lgani uchun siz `go build` qila olmasligingiz mumkin — bu normal. sqlc tip nomlarini deterministik taxmin qiling (`CreateXParams`, method `CreateX`). Aniq foydalangan sqlc method/param nomlarini javobda sanang.
9. `gofmt` toza, 120 ustun, godoc kommentlar inglizcha, muhim izohlar qisqa.

## Umumiy infrastruktura (mavjud, foydalaning)
- `internal/shared/httpx`: `httpx.OK(c, status, payload)`, `NoContent(c)`, `Error(c, err)`, `ErrorWithDetails`, `BadRequest/NotFound/Unauthorized/Forbidden/Conflict/TooManyRequests/Internal`.
- `internal/shared/middleware`: `Auth(verifier)`, `RequireRole(roles...)`, `UserID(c) string`, `Role(c) string`, `TokenVerifier`.
- `internal/shared/validator`: `validator.Struct(req)` → `(map[string]any, error)`.
- `internal/shared/database`: `database.ErrNotFound`, `NewPool`.
- sqlc paket: `sqlcdb "github.com/botspace/backend/internal/shared/database/sqlc"`.
- Rollar: `user.RoleUser/RoleAdmin/RoleSuperuser` — lekin `user` paketga import qilmang; string konstant ishlating yoki `middleware.RequireRole("admin","superuser")`.

## Migration raqamlari (to'qnashmaslik uchun — qat'iy)
- 000001–000004: BAND (users, refresh_tokens, sms_confirm, projects)
- 000010: plan          → PlanModel + UserSubscription
- 000011: release       → RelaseModel + DebugModel + EnviromentModel (project runtime)
- 000012: file          → FileModel + ResourceModel + StorageResourceModel + ProjectResourceSnapshot + DebugRunLog
- 000013: message       → MessageModel (broadcast)
- 000014: module        → ModuleModel + ProjectModuleModel
- 000015: webhook       → WebhookEndpointModel + ManagedBotModel
- 000020: credential    → Credential
- 000021: payment       → PaymentOrderModel + TransactionModel + WithdrawalRequestModel + HistoryModel
- 000022: template      → template marketplace (barcha modellar)
- 000023: cms           → CollectionModel + FieldModel + ExportTaskModel (+ Mongo hujjatlar drayveri)
- 000024: ticket        → tickets app
- 000025: blog          → blog app
- 000026: administration→ AuditLog
- 000027: authbot       → authbot app
- 000028: kyc/verification → UserVerificationModel (accounts KYC)
