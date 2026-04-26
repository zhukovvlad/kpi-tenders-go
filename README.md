# go-kpi-tenders

Бэкенд-оркестратор SaaS-платформы для анализа тендерной документации.

## Стек

| Компонент | Версия |
|-----------|--------|
| Go | 1.25+ |
| PostgreSQL + pgvector | 16 |
| Redis | 7 |
| MinIO | latest |
| Gin | 1.12 |
| sqlc | 1.30 |
| pgx/v5 | 5.9 |

## Быстрый старт

### 1. Зависимости

```bash
# Поднять PostgreSQL, Redis, MinIO
make up

# Применить миграции
make migrate_up
```

### 2. Конфигурация

Скопировать `.env.example` → `.env` и заполнить:

```bash
APP_ENV=local
APP_PORT=8080

DB_URL=postgres://kpi:kpi_secret@localhost:5432/kpi_tenders?sslmode=disable
REDIS_URL=redis://localhost:6379/0

JWT_ACCESS_SECRET=...
JWT_REFRESH_SECRET=...
SERVICE_TOKEN=...        # статический токен для Python-воркера

S3_ENDPOINT=localhost:9000
S3_ACCESS_KEY=minioadmin
S3_SECRET_KEY=minioadmin
S3_BUCKET=tenders
S3_USE_SSL=false

# Watchdog (опционально, показаны дефолты)
WATCHDOG_INTERVAL=2m
WATCHDOG_THRESHOLD=10m
WATCHDOG_MAX_RETRIES=5
WATCHDOG_BATCH_SIZE=100
```

Сгенерировать JWT и SERVICE_TOKEN:

```bash
make gen-secrets
```

### 3. Запуск

```bash
make run
```

## Структура проекта

```text
cmd/api/            — точка входа, graceful shutdown
internal/
  config/           — конфигурация (cleanenv + .env)
  server/           — HTTP-слой: роутер, middleware, хендлеры
  service/          — бизнес-логика
  repository/       — SQLC-генерируемый слой БД
  store/            — Store interface + транзакции
  storage/          — MinIO/S3 клиент
  pythonworker/     — Celery-издатель поверх Redis
  watchdog/         — сторожевой таймер зависших задач
  pgutil/           — утилиты PostgreSQL
pkg/
  errs/             — структурированные ошибки
  logging/          — slog-логгер
sql/
  migrations/       — миграции (golang-migrate)
  query/            — SQL-запросы для sqlc
tests/integration/  — интеграционные тесты (testcontainers)
```

## API

### Аутентификация

```text
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
GET  /api/v1/auth/me
```

Токены хранятся в HttpOnly Cookie (`access_token`, `refresh_token`).

### Организации

```text
GET    /api/v1/organizations/:id
PATCH  /api/v1/organizations/:id
DELETE /api/v1/organizations/:id
```

### Документы

```text
POST   /api/v1/documents              — создать запись вручную (JSON)
POST   /api/v1/documents/upload       — загрузить файл (multipart → S3 → БД)
GET    /api/v1/documents              — список документов
                                        ?site_id=uuid   → по объекту
                                        ?parent_id=uuid → артефакты документа
                                        (без параметров → корневые документы орг.)
GET    /api/v1/documents/:id
GET    /api/v1/documents/:id/url      — presigned URL (?download=true|false)
DELETE /api/v1/documents/:id
```

**Артефакты** — документы, создаваемые воркером как результат обработки (например, `convert_md`, `anonymize_doc`). Хранятся в той же таблице с `parent_id` и `artifact_kind`. При удалении родителя удаляются каскадно.

### Задачи обработки

```
POST   /api/v1/tasks
GET    /api/v1/tasks
GET    /api/v1/tasks/:id
PATCH  /api/v1/tasks/:id
DELETE /api/v1/tasks/:id
```

### Объекты строительства

```
POST   /api/v1/sites
GET    /api/v1/sites
GET    /api/v1/sites/:id
PATCH  /api/v1/sites/:id
DELETE /api/v1/sites/:id
```

### Internal (Python-воркер)

```
PATCH /internal/worker/tasks/:id/status
```

Требует `Authorization: Bearer <SERVICE_TOKEN>`.

## Тесты

```bash
make test-unit        # unit-тесты, без Docker
make test-integration # интеграционные, требует Docker (testcontainers)
make test             # всё вместе
```

## Кодогенерация

```bash
make sqlc   # регенерировать internal/repository/ из sql/query/
make mock   # регенерировать internal/store/mock/ из repository.Querier
```

После изменения SQL-запросов всегда запускать `make sqlc && make mock`.

## База данных

| Миграция | Изменения |
|----------|-----------|
| 000001 | Сквош: organizations, users, construction_sites, documents (`artifact_kind`, CASCADE delete), document_tasks (`retry_count`, `input_storage_path`, UNIQUE), watchdog-индекс, root-индекс, UNIQUE-индекс артефактов, триггеры tenant isolation |

## CI

`.github/workflows/test.yml` — два job'а: `unit-tests` и `integration-tests`.
