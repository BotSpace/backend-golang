# Go backend port — holat

Django backend (`../core/apps/…`, ~50k qator) → Go (jst-go clean architecture).
Kod uslubi ko'chirilmagan — idiomatik Go'da qaytadan yozilgan.

## ✅ TO'LIQ ISHLAYDI (build + test + migrate + e2e smoke — hammasi yashil)
`go build ./...` 0 xato · `go test ./...` 19/19 paket PASS · `go vet` toza ·
29 migration qo'llanadi (48 jadval) · server 176 route bilan ishga tushadi.

### Ko'chirilgan modullar (`internal/modules/`)
| Modul | Domen |
|---|---|
| auth, user | telefon+OTP register/confirm/login, JWT (rotation+revoke), reset, me, balance, admin |
| project | owner-scoped CRUD + soft-delete |
| plan | tarif + subscription (free/pro/max seed), usage/limit |
| release | relase + debug + environment (engine.Orchestrator orqali) |
| file | file upload/resource/used/monitoring |
| message | broadcast xabarlar |
| plugin | module katalog + project_module (CI plugin) |
| webhook | webhook endpoint mint/resolve + managed bot |
| credential | shifrlangan credential + internal resolve (AES-256-GCM) |
| payment | order/transaction/withdrawal/history + Click/Payme webhook |
| template | marketplace: template/submission/order/like/withdrawal + komissiya |
| cms | collection/field (Postgres) + document (MongoDB) + export |
| ticket, blog, administration, kyc, authbot, ws | support, blog, audit+stats, KYC, Telegram-login, websocket |

### Infratuzilma (`internal/shared/`)
sqlc (nullable→pointer), golang-migrate, RS256 JWT, httpx, validator, middleware,
`crypto` (AES-256-GCM), `engine` (Orchestrator/MongoProvisioner interface + Stub).

## ⚠️ Stub/keyin ulanadi (aniq belgilangan)
- **engine.Stub**: Docker/K8s deploy + Mongo provisioning no-op. Real `engine-golang` HTTP client ulanadi → deploy status hozircha `pending`.
- **OTP**: dev'da kod `1111` log'ga; prodda `APP_OTP_PROD=true` + real SMS sender.
- **Notifier** (payment→engine), **ReleaseRelay** (plugin module-trigger): interface bor, impl TODO.
- Telegram Bot API (authbot webhook, managed bot sync) alohida bot servisda qoladi.

## Ishga tushirish (local)
```
# postgres (db container, parol 23092005) 5433, redis 6380, mongo 27018 docker'da
export APP_DB_HOST=localhost APP_DB_PORT=5433 APP_DB_PASSWORD=23092005 APP_DB_NAME=botspace \
       APP_MONGO_URL=mongodb://localhost:27018 APP_CRYPTO_KEY=... APP_INTERNAL_TOKEN=...
make migrate-up   # yoki APP_DB_AUTO_MIGRATE=true
make run          # :8080, swagger /swagger/index.html
```

## Frontend moslash — ✅ BAJARILDI (3/3, browser'da tasdiqlangan)
Umumiy farqlar: UUID id · `{access_token,refresh_token}` JWT · `{items,total,limit,offset}` pagination · base `:8090`.
- **dashboard**: typecheck PASS, browser E2E — authbot login → home (real stats) → botlar ro'yxati. Console xatosiz.
- **admin**: typecheck PASS, agent Playwright bilan barcha sahifani tekshirdi (users/tickets/plans/templates/blog/kyc/projects/audit-log/settings). `Page<T>` chekkada `{items}`→eski `{results}` shaklga tarjima (UI o'zgармади). Standalone login sahifa qo'shildi.
- **constructor**: typecheck PASS, browser E2E — loyiha yuklash, node qo'shish→autosave PATCH 200, releases-gate, env upsert. Console xatosiz.

## ⚠️ Backend kamchiliklar — keyingi bosqich (parity uchun kerak, hozir yo'q)
Frontend adaptatsiyasida aniqlangan (fabrikatsiya emas — kod o'qib tasdiqlangan):
- **Debug/Release live-event streaming YO'Q**: `/api/v1/ws` — generic chat scaffold, domen event'lari emas (`release_status`, `debug_status`, `context`, `node_execution`, `error`). Constructor'ning debug SSE (context/errors/node-event) va bot-logs SSE oqimi backend'da yo'q. **Eng muhim kamchilik.**
- **Admin-wide endpointlar yo'q** (backend owner-scoped): admin project listing, admin transactions/orders, plan CRUD, credential-type CRUD, config/settings, monitoring, user ban / balance adjust, richer join'lar (ticket/withdrawal requester phone).
- **Credential**: `options/:resource`, `oauth/:provider/start`, `:id/test`, reveal — yo'q.
- **Release**: plan/months bilan yaratish, `running_bot_limit` (409 cap) server-side yo'q.
- **CMS**: constructor `name` bo'yicha, backend `:slug` bo'yicha kalitlaydi — mos kelmasligi mumkin. Collab-token (multi-editor) yo'q.
- Engine deploy/Mongo provisioning — `engine.Stub` (real `engine-golang` ulanishi kerak).
