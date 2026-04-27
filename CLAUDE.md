# go-kpi-tenders — CLAUDE.md

## Проект

Бэкенд-оркестратор SaaS-платформы для анализа тендерной документации.  
**Stack:** Go 1.22+, Gin, sqlc, pgx/v5, PostgreSQL 16 + pgvector, Redis, MinIO.

## Layout

```text
cmd/api/main.go               — точка входа, graceful shutdown
internal/config/              — конфигурация (cleanenv + .env)
internal/server/              — HTTP-слой: Server struct, роутер, middleware, хендлеры
internal/server/storage_iface.go — интерфейс storageClient (consumer-side, Go idiom)
internal/service/             — бизнес-логика
internal/repository/          — SQLC-генерируемый слой БД (сгенерирован sqlc)
internal/store/               — Store interface + SQLStore (transaction support)
internal/store/mock/          — MockStore для unit-тестов сервисов
internal/storage/             — MinIO/S3 клиент (upload, presigned URL, delete, SafeExt)
internal/pythonworker/        — Celery-издатель поверх Redis (LPUSH, protocol v2)
internal/watchdog/            — сторожевой таймер зависших задач (re-queue / fail после maxRetries)
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
    storageClient storageClient         // interface (server.storageClient); nil when S3 creds absent; upload degrades to 500
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
- `DocumentService` дополнительно принимает consumer-side interface `documentStorage` (только `PresignedURLWithParams`); `nil`-safe — при отсутствии S3 возвращает 500.
- `DocumentTaskService` принимает `repository.Querier` и тот же consumer-side interface `workerPythonClient`; `nil`-safe — при отсутствии Python-клиента триггер пропускается. После INSERT берёт `input_storage_path` из `document_tasks` (заполняется subquery в SQL), передаёт в `pythonClient.Process` (best-effort: ошибки логируются, наружу не пробрасываются). Для модуля `extract` перед INSERT читает `extraction_keys` текущего tenant и передаёт их в Celery kwargs.
- `WorkerService` принимает `repository.Querier` и consumer-side interface `workerPythonClient` (только `Process`); реализован `*pythonworker.Publisher`. Redis обязателен — `pythonworker.New` вызывается в `NewServer()`, при ошибке `NewServer()` возвращает `error`; завершение процесса происходит в `cmd/api/main.go`.
- `ExtractionKeyService` отвечает за `POST /api/v1/extraction-keys/resolve`: dedupe по `source_query`, генерация стабильного `key_name`, dedupe по `(organization_id, key_name)`.
- В `NewServer()` экземпляр `*pythonworker.Publisher` создаётся **один раз** через `cfg.RedisURL` и передаётся в оба сервиса (`DocumentTaskService` и `WorkerService`). `workerService` создаётся безусловно при успешной инициализации сервера. `srv.Close()` освобождает пул Redis-соединений при graceful shutdown.
- Watchdog запускается горутиной из `cmd/api/main.go`; завершение ожидается через `sync.WaitGroup` (`watchdogDone.Wait()`) после `watchdogCancel()` и до `srv.Close()` — иначе in-flight `LPUSH` может попасть на закрытый пул.
- `store.SQLStore` — production-реализация поверх `*pgxpool.Pool`.
- `mock.MockStore` — testify-mock для unit-тестов; `ExecTx` hand-written: вызывает `fn(m)` для propagation ошибок из транзакции.

### Ошибки

Сервисы возвращают `*errs.Error` с кодами из `pkg/errs`:

| Код | HTTP |
|-----|------|
| `internal_error` | 500 |
| `not_found` | 404 |
| `conflict` | 409 |
| `validation_failed` | 400 |
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
GET              /api/v1/documents                  (?parent_id=uuid → список артефактов; ?site_id=uuid → по объекту; иначе — корневые)
GET/DELETE       /api/v1/documents/:id
GET              /api/v1/documents/:id/url   (?download=true|false → presigned URL, TTL 15 мин)

POST/GET         /api/v1/tasks
GET/PATCH/DELETE /api/v1/tasks/:id      (status update)

POST             /api/v1/extraction-keys/resolve

PATCH            /internal/worker/tasks/:id/status  (worker callback, ServiceBearerAuth)

POST/GET         /api/v1/sites
GET/PATCH/DELETE /api/v1/sites/:id
```

### Заглушки / TODO

_Нет активных заглушек._

### Не реализовано

- Projects (таблица есть, хендлеров/сервисов/запросов нет)
- `catalog_positions` SQLC-запросы (таблица будет создана в 000002)

## База данных

Миграции: `sql/migrations/`, `make migrate_up`.  
После изменения SQL-запросов — `make sqlc`.

| Миграция | Таблицы / изменения |
|----------|---------------------|
| 000001 | Полная схема: organizations, users, construction_sites, documents (artifact_kind, parent_id CASCADE, UNIQUE id+organization_id), extraction_keys (UNIQUE NULLS NOT DISTINCT organization_id+key_name, idx_extraction_keys_org_created_at, idx_extraction_keys_org_norm_source_query), document_extracted_data (UNIQUE organization_id+document_id+key_id, composite FK document_id+organization_id, key_id+organization_id), document_tasks (retry_count, input_storage_path TEXT NOT NULL CHECK(btrim<>«»), UNIQUE document_id+module_name); все FK-индексы; idx_document_tasks_stale (WHERE status IN ('pending','processing')); idx_documents_root (WHERE parent_id IS NULL); idx_documents_artifact_kind UNIQUE (WHERE parent_id IS NOT NULL); documents_parent_artifact_kind_chk CHECK(parent_id IS NOT NULL → artifact_kind IS NOT NULL AND btrim<>«»); триггеры tenant isolation + запрет смены organization_id |

`catalog_positions.embedding` — тип `vector` без фиксированной размерности  
(зафиксируй как `vector(1536)` когда определишься с моделью эмбеддингов).

> **Примечание:** следующая миграция — `catalog_positions` (pgvector RAG), будет `000002`.

## Extraction flow

1. Пользователь вызывает `POST /api/v1/extraction-keys/resolve` с `source_query`.
2. Go ищет дубль по нормализованному `source_query`; если не нашёл — генерирует deterministic `key_name` (snake_case + transliteration) и проверяет дубль по `(organization_id, key_name)`.
3. При создании `POST /api/v1/tasks` с `module_name="extract"` Go читает все ключи tenant и отправляет их Python-воркеру в Celery protocol v2 `kwargs.extraction_keys`.
4. Когда worker присылает `completed`, `WorkerService` сохраняет результат в `document_extracted_data`. Поддерживаемые формы payload:
   - `{"extracted_data":[{"key_name":"...","value":...,"confidence":0.9}]}`
   - `[{"key_name":"...","value":...}]`
   - `{"key_name":{"value":...,"confidence":0.9}}`
   - `{"key_name": ...}`
5. Значения без известного `key_name` игнорируются; исходный JSON остаётся в `document_tasks.result_payload`.

## Стратегия тестирования

### Unit-тесты (без Docker)
```text
internal/service/service_auth_test.go               — AuthService: login, timing, JWT
internal/service/service_organization_test.go       — OrganizationService: register, conflicts
internal/service/service_user_test.go               — UserService: GetProfile, tenant isolation
internal/service/service_document_task_test.go      — DocumentTaskService: Create success, not found, conflict, db error, python trigger, extract kwargs, python error best-effort
internal/service/service_worker_test.go             — WorkerService: chaining, idempotency, errors, python client, CreateArtifactDocument idempotent upsert, InputStoragePath forwarding, extract result persistence
internal/server/errors_test.go                      — respondWithError маппинг
internal/server/health_test.go                      — health endpoint
internal/server/middleware_test.go                  — AuthMiddleware, ServiceBearerAuth
internal/server/handler_user_test.go                — GET /api/v1/auth/me
internal/server/handler_document_test.go            — POST /api/v1/documents/upload; GET ?parent_id= (valid, invalid UUID, parent not found)
internal/storage/client_test.go                     — PresignedURL, Upload, Delete error wrapping + TestSafeExt (10 кейсов)
internal/pythonworker/client_test.go                — buildCeleryMessage: поля, маршрутизация модулей, неизвестный модуль, Celery kwargs
internal/watchdog/watchdog_test.go                  — runOnce: re-queue, maxRetries exceeded, no tasks, CAS skip, best-effort publish error, pending status re-queue (6 кейсов)
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
tests/integration/repository_test.go  — CRUD, tenant isolation, artifact parent-child, cascade delete, root-list filtering
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
Watchdog: `WATCHDOG_INTERVAL` (default `2m`), `WATCHDOG_THRESHOLD` (default `10m`), `WATCHDOG_MAX_RETRIES` (default `5`), `WATCHDOG_BATCH_SIZE` (default `100`).

## Frontend

Живёт в отдельном репозитории: `/home/zhukovvlad/Projects/kpi-tenders/react-kpi-tenders`.  
CORS: в `local` — `AllowAllOrigins`, в prod — `https://*.kpi-tenders.kz`.  
Axios с `withCredentials: true`, токены только в Cookies (не localStorage).

## Развёртывание

Сейчас: локально через `docker-compose.yml`.  
CI: `.github/workflows/test.yml` — два job'а: `unit-tests` и `integration-tests`.
