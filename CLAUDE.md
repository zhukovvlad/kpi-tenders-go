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
- `DocumentTaskService` принимает `repository.Querier` и тот же consumer-side interface `workerPythonClient`; `nil`-safe — при отсутствии Python-клиента триггер пропускается. После INSERT берёт `input_storage_path` из `document_tasks` (заполняется subquery в SQL), передаёт в `pythonClient.Process` (best-effort: ошибки логируются, наружу не пробрасываются).
- `WorkerService` принимает `repository.Querier`, `workerPythonClient` (`Process`) и `extractionPipeline` (`Progress`, `OnResolveKeysCompleted`, `OnExtractCompleted`, `MarkRequestFailed`). Pipeline обязателен; реализован `*service.ExtractionService`. Redis обязателен — `pythonworker.New` вызывается в `NewServer()`, при ошибке `NewServer()` возвращает `error`; завершение процесса происходит в `cmd/api/main.go`.
- `ExtractionService` владеет жизненным циклом `extraction_requests`: `Initiate(ctx, docID, orgID, questions, anonymize)` валидирует документ → создаёт `extraction_requests` row → вызывает `Progress(ctx, req)`. `Progress` смотрит существующие артефакты документа (`ListDocumentsByParent`) и решает что запустить:
  - есть нужный артефакт (`anonymize_doc` для `anonymize=true`, `convert_md` для `false`) → `enqueueResolveKeys` (создаёт per-request `resolve_keys`-таску через `CreateDocumentTaskForRequest`, переводит запрос в `running`). При ошибке публикации в Redis вызывает `MarkRequestFailed` (extraction_request не зависает в `running`).
  - есть MD, но нет anonymize-артефакта при `anonymize=true` → `ensureAnonymizeTask` (singleton).
  - нет MD → `ensureConvertTask` (singleton). convert→anonymize цепочка в `WorkerService` подхватит дальше.
  - `enqueueExtract` аналогично вызывает `MarkRequestFailed` при ошибке публикации.
- `WorkerService.HandleStatusUpdate` после `convert completed`: **сначала** безусловно вызывает `registerConvertArtifacts` (создаёт `convert_md` Document в БД), **затем** `triggerAnonymize`. Порядок критичен: если `triggerAnonymize` упадёт (Redis down), `convert_md` артефакт всё равно существует и `ExtractionService.Progress` найдёт его через `ListDocumentsByParent` — иначе `anonymize=false` запросы зависают навсегда. После обоих шагов вызывается `progressPendingRequests`. При `convert/anonymize failed` вызывается `failPendingRequests` — все pending `extraction_requests` переводятся в `failed` через `pipeline.MarkRequestFailed` (best-effort: ошибки логируются, цикл продолжается).
- На `resolve_keys completed` Worker делегирует в `pipeline.OnResolveKeysCompleted`: парсит `{new_keys, resolved_schema}` (без `md_document_id`), upsert ключей, сохраняет `resolved_schema` на запросе, ищет артефакт по `parent_id+artifact_kind` и создаёт `extract`-таску через `CreateDocumentTaskForRequest`. На `extract completed` — `pipeline.OnExtractCompleted`: batch upsert `document_extracted_data` (параллельные массивы `key_ids`/`extracted_values` через `unnest`), затем `extraction_requests.status = 'completed'`. Оба метода создают `chainCtx` через `context.WithoutCancel + pipelineTimeout` — DB-операции не зависят от жизни HTTP-запроса воркера. На `failed` resolve_keys/extract с непустым `extraction_request_id` — `pipeline.MarkRequestFailed`.
- В `NewServer()` экземпляр `*pythonworker.Publisher` создаётся **один раз** через `cfg.RedisURL` и передаётся в `DocumentTaskService`, `ExtractionService` и `WorkerService`. Порядок инициализации важен: `ExtractionService` строится **до** `WorkerService` и подаётся в него как pipeline. `srv.Close()` освобождает пул Redis-соединений при graceful shutdown.
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

### sqlc naming quirk

`$N::type` cast генерирует поле `ColumnN` в структуре параметров — sqlc не выводит имя из выражения. Вместо этого используй `sqlc.arg(name)` — тогда поле называется `Name` и тип выводится из схемы (`pgtype.UUID` для `uuid`, etc.):

```sql
-- Плохо: генерирует Column2 uuid.UUID
WHERE organization_id = $2::uuid

-- Хорошо: генерирует OrganizationID pgtype.UUID
WHERE organization_id = sqlc.arg(organization_id)
```

При этом сервис должен оборачивать `uuid.UUID` в `pgtype.UUID{Bytes: id, Valid: true}` при передаче в сгенерированную функцию.

### Авторизация

- **Web-клиент:** JWT в HttpOnly Cookies (`access_token` + `refresh_token`).
- **Python-воркер:** статический Bearer Token через `ServiceBearerAuth()` middleware.
- Timing-attack защита: `dummyHash` + `subtle.ConstantTimeCompare`.
- **Роли:** `member`, `admin`, `owner`.
  - `owner` — суперпользователь без привязки к тенанту; JWT содержит `OrgID = uuid.Nil`.
  - `AdminOnly()` пропускает только `admin` (owner исключён — его OrgID = uuid.Nil сломает tenant-scoped запросы).
  - `OwnerOnly()` пропускает только `owner`.
  - `TenantScopedOnly()` — отклоняет owner-токены на tenant-scoped маршрутах (sites, documents, tasks, extraction-requests, contract-kinds, file-roles, comparison-sessions) с 403; без этого owner получит DB-ошибку (FK/empty result) вместо чёткого отказа.
  - `isOwner(c)` — package-level хелпер в `handler_organization.go` для bypass tenant-scoping в хендлерах.
  - Хендлеры `GetOrganization`, `UpdateOrganization`, `DeleteOrganization` не проверяют `id == orgID` для owner.
  - `service_user.go::GetProfile`: если `orgID == uuid.Nil` — вызывает `GetUserByID` без org-фильтра (для `/api/v1/auth/me` owner).

## Текущее состояние API

### Реализовано

```text
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout
GET    /api/v1/auth/me

GET/PATCH/DELETE /api/v1/organizations/:id
GET              /api/v1/organizations/:id/users              (OwnerOnly; список пользователей любой org)
PATCH            /api/v1/organizations/:id/users/:user_id     (OwnerOnly; изменить роль / активность)
DELETE           /api/v1/organizations/:id/users/:user_id     (OwnerOnly; деактивировать пользователя)

POST             /api/v1/documents           (JSON, storage_path задаётся вручную)
POST             /api/v1/documents/upload    (multipart/form-data → S3 → БД)
GET              /api/v1/documents                  (?parent_id=uuid → список артефактов; ?site_id=uuid → по объекту; иначе — корневые)
GET/DELETE       /api/v1/documents/:id
GET              /api/v1/documents/:id/url   (?download=true|false → presigned URL, TTL 15 мин)
PATCH            /api/v1/documents/:id/meta  (contract_kind_id, file_role_id, bundle_id)
GET              /api/v1/documents/:id/extraction-requests  (?limit=20&offset=0 → список запросов экстракции)
GET              /api/v1/documents/:id/answers              (все ответы по всем extraction_requests документа)

POST             /api/v1/tasks
GET              /api/v1/tasks          (?document_id=uuid → задачи одного документа; ?document_ids=uuid,uuid,… → батч до 100 документов)
GET/PATCH/DELETE /api/v1/tasks/:id      (status update)

PATCH            /internal/worker/tasks/:id/status  (worker callback, ServiceBearerAuth)

POST/GET         /api/v1/sites
GET              /api/v1/sites/root         (корневые объекты; → []SiteListItem с breadcrumbs, contract_kinds, aggregate_status, extracted_count)
GET/PATCH/DELETE /api/v1/sites/:id
GET              /api/v1/sites/:id/children (дочерние объекты; → []SiteListItem с breadcrumbs предков)
PATCH            /api/v1/sites/:id/cover    ({ cover_image_path })
PATCH            /api/v1/sites/:id/type     ({ site_type })
GET              /api/v1/sites/:id/audit-log (?limit=50&offset=0)
GET              /api/v1/sites/:id/events   (человекочитаемые события; → []SiteEvent с kind, actor_name, message)

POST             /api/v1/documents/:id/extract       (создаёт extraction_request; body: { questions, anonymize? }, default anonymize=true; → 201 { extraction_request_id, status })
GET              /api/v1/extraction-requests/:id     (status + resolved_schema + answers; tenant-scoped)

GET/POST         /api/v1/extraction-keys              (CRUD ключей экстракции; системные org_id=NULL шарятся между тенантами; UNIQUE NULLS NOT DISTINCT)
GET/PATCH/DELETE /api/v1/extraction-keys/:id

GET/POST         /api/v1/contract-kinds
GET/PATCH/DELETE /api/v1/contract-kinds/:id

GET/POST         /api/v1/file-roles
GET/PATCH/DELETE /api/v1/file-roles/:id

GET/POST         /api/v1/invitations         (AdminOnly; POST возвращает { invitation, token })
DELETE           /api/v1/invitations/:id     (AdminOnly)

GET/POST         /api/v1/comparison-sessions
GET/DELETE       /api/v1/comparison-sessions/:id
POST             /api/v1/comparison-sessions/:id/documents
DELETE           /api/v1/comparison-sessions/:id/documents/:doc_id
```

### Заглушки / TODO

_Нет активных заглушек._

### Не реализовано

- Projects (таблица есть, хендлеров/сервисов/запросов нет)
- `catalog_positions` SQLC-запросы (pgvector RAG, отдельной миграцией позже)

## База данных

Миграции: `sql/migrations/`, `make migrate_up`.  
После изменения SQL-запросов — `make sqlc`.

| Миграция | Таблицы / изменения |
|----------|---------------------|
| 000001 | Полная схема: organizations, users, construction_sites, documents (artifact_kind, parent_id CASCADE), document_tasks (retry_count, input_storage_path, UNIQUE document_id+module_name); все FK-индексы; idx_document_tasks_stale; триггеры tenant isolation |
| 000002 | `extraction_keys` (org_id nullable, key_name, source_query, data_type; UNIQUE NULLS NOT DISTINCT org+name) + `document_extracted_data` (org_id, document_id, key_id, extracted_value; composite FK doc+org → documents; `uq_extracted_data_doc_key` UNIQUE (org_id, document_id, key_id); trigger `trg_check_extracted_data_key_org` блокирует cross-tenant key_id; триггеры `trg_immut_org_*` через `prevent_organization_id_change()` из 000001 запрещают изменение org_id после вставки; `idx_extracted_data_key_org`) + composite UNIQUE constraint `uq_documents_id_org` на таблице documents |
| 000003 | `extraction_requests` (id, document_id, organization_id, questions jsonb, anonymize bool default true, status, resolved_schema jsonb, error_message; composite FK doc+org → documents; CHECK questions = непустой jsonb-массив; immut org_id триггер; `idx_extraction_requests_doc_pending`). В `document_tasks` добавлена колонка `extraction_request_id` (FK CASCADE на extraction_requests). Старый `UNIQUE(document_id, module_name)` снесён; заменён двумя partial-индексами: `uq_document_tasks_doc_singleton (document_id, module_name) WHERE module_name IN ('convert','anonymize')` и `uq_document_tasks_request_module (extraction_request_id, module_name) WHERE module_name IN ('resolve_keys','extract')`. CHECK `document_tasks_module_request_chk` форсирует инвариант: convert/anonymize ⇔ extraction_request_id IS NULL; resolve_keys/extract ⇔ NOT NULL. |
| 000004 | `document_contract_kinds` + `document_file_roles` (org-specific + системные с nullable org_id); `user_invitations` (хранит sha256-hash токена); `site_audit_log` (INSERT-only); `comparison_sessions` + `comparison_session_documents`. Новые поля: `construction_sites.{cover_image_path,site_type,last_activity_at}`, `documents.{contract_kind_id,file_role_id,bundle_id}`, `extraction_keys.{display_name,is_active,category}`, `users.{last_login_at}` + role CHECK. Вью `v_site_status`, триггеры `propagate_site_activity`, `check_document_kind_role_org`. |
| 000005 | `users.organization_id` → nullable (owner не привязан к org). CHECK `users_role_chk` расширен: `role IN ('admin', 'member', 'owner')`. Новый CHECK `users_owner_org_chk`: `owner ⟺ organization_id IS NULL`. |

> **Примечание:** следующая миграция — `catalog_positions` (pgvector RAG), будет `000006`.

## Стратегия тестирования

### Unit-тесты (без Docker)
```text
internal/service/service_auth_test.go               — AuthService: login, timing, JWT
internal/service/service_organization_test.go       — OrganizationService: register, conflicts
internal/service/service_user_test.go               — UserService: GetProfile, tenant isolation
internal/service/service_document_task_test.go      — DocumentTaskService: Create success, not found, conflict, db error, python trigger, python error best-effort, ListByDocuments happy/empty/error (9 кейсов)
internal/service/service_worker_test.go             — WorkerService: status persistence, convert→anonymize cтейтинг через CreateDocumentTaskSingleton, прогрессия pending extraction_requests после convert/anonymize, делегирование OnResolveKeysCompleted/OnExtractCompleted в pipeline-mock, MarkRequestFailed на failed-таски, idempotent reuse anonymize, не-найденная таска (10 кейсов)
internal/service/service_extraction_test.go         — ExtractionService: валидация, 404 на документ, прогрессия в трёх ветках (нет MD → convert; MD есть, anonymize=false → resolve_keys; MD есть, anonymize=true, нет anon → anonymize; есть anon → resolve_keys на anon-пути), best-effort progress, OnResolveKeysCompleted full flow + missing extraction_request_id, OnExtractCompleted с null-значениями + статус=completed, Progress no-op на терминальном статусе, GetRequest 404 (11 кейсов)
internal/server/errors_test.go                      — respondWithError маппинг
internal/server/health_test.go                      — health endpoint
internal/server/middleware_test.go                  — AuthMiddleware, ServiceBearerAuth, AdminOnly (owner=403), OwnerOnly (admin/member blocked), TenantScopedOnly (owner=403, admin/member pass)
internal/server/handler_user_test.go                — GET /api/v1/auth/me
internal/server/handler_document_test.go            — POST /api/v1/documents/upload; GET ?parent_id=; GET /:id; DELETE /:id; GET /:id/url (16 кейсов)
internal/server/handler_extraction_test.go          — POST /api/v1/documents/:id/extract: no auth, invalid id, missing/empty questions, not found, success (extraction_request_id), db error, anonymize=false propagation (8 кейсов)
internal/server/handler_document_task_test.go      — GET /api/v1/tasks: no auth, no params, both params, invalid id, single success/error, batch success/invalid uuid/too many/error, empty document_ids regression (11 кейсов)
internal/server/handler_comparison_session_test.go — ListComparisonSessions, GetComparisonSession, CreateComparisonSession, DeleteComparisonSession, AddDocumentToSession, RemoveDocumentFromSession: no auth, 400/404, success, DB error (19 кейсов)
internal/server/handler_contract_kind_test.go      — ListContractKinds, CreateContractKind, GetContractKind, DeleteContractKind, UpdateContractKind: no auth, 400/404/409, success (13 кейсов)
internal/server/handler_invitation_test.go         — CreateInvitation: no auth, non-admin (403), invalid email/role (400), conflict (409), local-env includes token, production omits token (7 кейсов)
internal/server/handler_extraction_request_test.go — GET /api/v1/extraction-requests/:id: no auth, invalid UUID, not found, DB error, success pending (empty schema → no DB call) (5 кейсов)
internal/server/handler_extraction_key_test.go      — List, Get (404), Create (409 на дупликат), Update (partial PATCH), Delete: no auth, 400/404/409, success (13 кейсов)
internal/server/handler_construction_site_test.go   — ListRoot (empty, SiteListItem с meta), ListChildren (breadcrumbs, extracted_count), ListSiteEvents (actor, kind, message, empty, 404): no auth, 400/404, success (11 кейсов)
internal/service/service_contract_kind_test.go     — ContractKindService: List, Get (404), Create (unique 409), Update (404/409), Delete (0 rows 404) (14 кейсов)
internal/storage/client_test.go                     — PresignedURL, Upload, Delete error wrapping + TestSafeExt (10 кейсов)
internal/pythonworker/client_test.go                — buildCeleryMessage: поля, маршрутизация модулей, kwargs passthrough, nil kwargs, неизвестный модуль (5 кейсов)
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
