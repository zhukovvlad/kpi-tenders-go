# go-kpi-tenders — CLAUDE.md

## Проект

Бэкенд-оркестратор SaaS-платформы для анализа тендерной документации.  
**Stack:** Go 1.22+, Gin, sqlc, pgx/v5, PostgreSQL 16 + pgvector, Redis, MinIO.

## Layout

```
cmd/api/main.go               — точка входа, graceful shutdown
internal/config/              — конфигурация (cleanenv + .env)
internal/server/              — HTTP-слой: Server struct, роутер, middleware, хендлеры
internal/service/             — бизнес-логика
internal/repository/          — SQLC-генерируемый слой БД (сгенерирован sqlc)
internal/store/               — Store interface + SQLStore (transaction support)
internal/store/mock/          — MockStore для unit-тестов сервисов
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
    cfg   *config.Config; log *slog.Logger
    store store.Store  // nil when pool == nil (tests without DB)
    router *gin.Engine
    authService, documentService, organizationService ...
}
```

Новые сервисы добавляются как поля `Server` и инициализируются в `NewServer()`.

### Store / Repository pattern

`store.Store` = `repository.Querier` + `ExecTx(ctx, fn func(Querier) error) error`.

- **Сервисы с транзакциями** (OrganizationService) принимают `store.Store`.
- **Сервисы без транзакций** (AuthService, DocumentService) принимают `repository.Querier`.
- `store.SQLStore` — production-реализация поверх `*pgxpool.Pool`.
- `mock.MockStore` — testify-mock для unit-тестов; `ExecTx` вызывает `fn(m)` если не настроена ошибка.

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

```
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout

GET/PATCH/DELETE /api/v1/organizations/:id

POST/GET         /api/v1/documents
GET/PATCH/DELETE /api/v1/documents/:id  (status update)

POST/GET         /api/v1/tasks
GET/PATCH/DELETE /api/v1/tasks/:id      (status update)
```

### Заглушки / TODO

```
/internal/worker/*  — ServiceBearerAuth подключён, нужен PATCH /internal/worker/tasks/{id}/status
```

### Не реализовано

- Projects (таблица есть, хендлеров/сервисов/запросов нет)
- Python worker integration endpoint
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
```
internal/service/service_auth_test.go         — AuthService: login, timing, JWT
internal/service/service_organization_test.go — OrganizationService: register, conflicts
internal/server/errors_test.go                — respondWithError маппинг
internal/server/health_test.go                — health endpoint
internal/server/middleware_test.go            — AuthMiddleware, ServiceBearerAuth
```

**Паттерн:**
```go
ms := new(mock.MockStore)
ms.On("ExecTx", mock.Anything, mock.Anything).Return(nil)   // вызовет fn(ms)
ms.On("CreateOrganization", mock.Anything, mock.Anything).Return(org, nil)
svc := service.NewOrganizationService(ms, log)
```

### Интеграционные тесты (требует Docker)
```
tests/integration/main_test.go        — TestMain: testcontainers pgvector/pgvector:pg16 + миграции
tests/integration/repository_test.go  — CRUD + RAG cosine search по catalog_positions
```

Build tag: `//go:build integration` — не запускаются при `go test ./...`.

## Команды

```bash
make up               # поднять инфраструктуру (DB, Redis, S3)
make migrate_up       # применить миграции
make sqlc             # регенерировать repository из SQL
make test             # unit-тесты (без Docker)
make test-unit        # unit-тесты explicit
make test-integration # интеграционные (нужен Docker)
make test-full        # всё вместе
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
