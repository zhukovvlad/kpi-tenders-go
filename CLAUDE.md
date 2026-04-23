# go-kpi-tenders — CLAUDE.md

## Проект

Бэкенд-оркестратор SaaS-платформы для анализа тендерной документации.  
**Stack:** Go 1.22+, Gin, sqlc, pgx/v5, PostgreSQL 16 + pgvector, Redis, MinIO.

## Layout

```text
cmd/api/main.go               — точка входа, graceful shutdown
internal/config/              — конфигурация (cleanenv + .env)
internal/server/              — HTTP-слой: Server struct, роутер, middleware, хендлеры
internal/service/             — бизнес-логика
internal/repository/          — SQLC-генерируемый слой БД (сгенерирован sqlc)
internal/store/               — Store interface + SQLStore (transaction support)
internal/store/mock/          — MockStore для unit-тестов сервисов
internal/storage/             — MinIO/S3 клиент (upload, presigned URL, delete, SafeExt)
internal/pgutil/              — утилиты PostgreSQL (IsUniqueViolation)
pkg/errs/                     — структурированные ошибки приложения
pkg/logging/                  — slog-логгер
sql/migrations/               — миграции (только Go-сторона управляет схемой)
sql/query/                    — SQL-запросы для sqlc
tests/integration/            — интеграционные тесты (build tag: integration)
.github/workflows/test.yml    — CI: unit + integration jobs
```

## Паттерны кода

### Server struct

Всё строится вокруг `internal/server/server.go`:

```go
type Server struct {
    cfg           *config.Config
    log           *slog.Logger
    store         store.Store           // nil when pool == nil (tests without DB)
    storageClient *storage.Client       // nil when S3 creds absent; upload degrades to 500
    router        *gin.Engine
    authService, documentService, organizationService ...
}
```

Новые сервисы/клиенты добавляются как поля `Server` и инициализируются в `NewServer()`.
`storageClient` инициализируется через `switch`: оба ключа → init; только один → `log.Error` (мисконфигурация); ни одного → без S3 (штатно). `storage.New` валидирует `S3Endpoint` и `S3Bucket` перед созданием клиента.

### Store / Repository pattern

`store.Store` = `repository.Querier` + `ExecTx(ctx, fn func(Querier) error) error`.

- **Сервисы с транзакциями** (OrganizationService) принимают `store.Store`.
- **Сервисы без транзакций** (AuthService, DocumentService) принимают `repository.Querier`.
- `store.SQLStore` — production-реализация поверх `*pgxpool.Pool`.
- `mock.MockStore` — testify-mock для unit-тестов; `ExecTx` hand-written: вызывает `fn(m)` для propagation ошибок из транзакции.

### Ошибки

Сервисы возвращают `*errs.Error` с кодами из `pkg/errs`:

| Код | HTTP |
|-----|------|
| `internal_error` | 500 |
| `not_found` | 404 |
| `conflict` | 409 |
| `validation_failed` | 422 |
| `unauthorized` | 401 |
| `forbidden` | 403 |
| `not_implemented` | 501 |

Хендлеры вызывают `s.respondWithError(c, err)` — ручной маппинг запрещён.  
Детали БД (`pgconn`) наружу не пробрасываются.

### Авторизация

- **Web-клиент:** JWT в HttpOnly Cookies (`access_token` + `refresh_token`).
- **Python-воркер:** статический Bearer Token через `ServiceBearerAuth()` middleware.
- Timing-attack защита: `dummyHash` + `subtle.ConstantTimeCompare`.

## Текущее состояние API

### Реализовано

```text
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout
GET    /api/v1/auth/me

GET/PATCH/DELETE /api/v1/organizations/:id

POST             /api/v1/documents           (JSON, storage_path задаётся вручную)
POST             /api/v1/documents/upload    (multipart/form-data → S3 → БД)
GET              /api/v1/documents
GET/DELETE       /api/v1/documents/:id

POST/GET         /api/v1/tasks
GET/PATCH/DELETE /api/v1/tasks/:id      (status update)

POST/GET         /api/v1/sites
GET/PATCH/DELETE /api/v1/sites/:id
```

### Заглушки / TODO

```text
/internal/worker/*  — ServiceBearerAuth подключён, нужен PATCH /internal/worker/tasks/{id}/status
```

### Не реализовано

- Projects (таблица есть, хендлеров/сервисов/запросов нет)
- `catalog_positions` SQLC-запросы (таблица создана в 000002)

## База данных

Миграции: `sql/migrations/`, `make migrate_up`.  
После изменения SQL-запросов — `make sqlc`.

| Миграция | Таблицы |
|----------|---------|
| 000001 | organizations, users, projects, documents, document_tasks |
| 000002 | catalog_positions (vector + JSONB для RAG-поиска) |

`catalog_positions.embedding` — тип `vector` без фиксированной размерности  
(зафиксируй как `vector(1536)` когда определишься с моделью эмбеддингов).

## Стратегия тестирования

### Unit-тесты (без Docker)
```text
internal/service/service_auth_test.go               — AuthService: login, timing, JWT
internal/service/service_organization_test.go       — OrganizationService: register, conflicts
internal/service/service_user_test.go               — UserService: GetProfile, tenant isolation
internal/server/errors_test.go                      — respondWithError маппинг
internal/server/health_test.go                      — health endpoint
internal/server/middleware_test.go                  — AuthMiddleware, ServiceBearerAuth
internal/server/handler_user_test.go                — GET /api/v1/auth/me
internal/server/handler_document_test.go            — POST /api/v1/documents/upload
internal/storage/client_test.go                     — PresignedURL, Upload, Delete error wrapping + TestSafeExt (10 кейсов)
```

**Паттерн:**
```go
ms := new(mock.MockStore)
ms.On("ExecTx", mock.Anything, mock.Anything).Return(nil)   // вызовет fn(ms)
ms.On("CreateOrganization", mock.Anything, mock.Anything).Return(org, nil)
svc := service.NewOrganizationService(ms, log)
```

### Интеграционные тесты (требует Docker)
```text
tests/integration/main_test.go        — TestMain: testcontainers pgvector/pgvector:pg16 + миграции
tests/integration/repository_test.go  — CRUD + RAG cosine search по catalog_positions
tests/integration/storage_test.go     — Upload + PresignedURL против эфемерного MinIO-контейнера (testcontainers)
```

Build tag: `//go:build integration` — не запускаются при `go test ./...`.

## Команды

> **Правило:** всегда запускать тесты через `make`, не напрямую через `go test`.

```bash
make up               # поднять инфраструктуру (DB, Redis, S3)
make migrate_up       # применить миграции
make sqlc             # регенерировать repository из SQL
make test-unit        # ← unit-тесты (./internal/... ./cmd/... ./pkg/...) — без Docker
make test-integration # ← интеграционные (./tests/integration/...) — нужен Docker
make test             # ← всё вместе: unit + integration
make mock             # регенерировать MockStore через mockery
make run              # запустить сервер
make gen-secrets      # сгенерировать JWT/service секреты
```

## Конфигурация

Загружается из `.env` через `cleanenv`. Ключевые переменные:
`APP_ENV`, `APP_PORT`, `DB_URL`, `REDIS_URL`, `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`, `SERVICE_TOKEN`, `S3_*`.

## Frontend

Живёт в отдельном репозитории: `/home/zhukovvlad/Projects/kpi-tenders/react-kpi-tenders`.  
CORS: в `local` — `AllowAllOrigins`, в prod — `https://*.kpi-tenders.kz`.  
Axios с `withCredentials: true`, токены только в Cookies (не localStorage).

## Развёртывание

Сейчас: локально через `docker-compose.yml`.  
CI: `.github/workflows/test.yml` — два job'а: `unit-tests` и `integration-tests`.
