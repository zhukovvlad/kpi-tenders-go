include .env
export

DB_URL ?= postgres://kpi:kpi_secret@localhost:5432/kpi_tenders?sslmode=disable
MIGRATE := migrate -path sql/migrations -database "$(DB_URL)"
PG_CONTAINER := kpi_postgres
PG_USER := kpi
PG_DB := kpi_tenders

## ── Docker ──────────────────────────────────────────

.PHONY: up down

up:
	docker compose up -d

down:
	docker compose down

## ── Database ────────────────────────────────────────

.PHONY: createdb dropdb migrate_up migrate_down

createdb:
	docker exec -it $(PG_CONTAINER) createdb --username=$(PG_USER) --owner=$(PG_USER) $(PG_DB)

dropdb:
	docker exec -it $(PG_CONTAINER) dropdb --username=$(PG_USER) $(PG_DB)

migrate_up:
	$(MIGRATE) up

migrate_down:
	$(MIGRATE) down 1

SQLC_VERSION ?= 1.30.0

## ── Code Generation ─────────────────────────────────

.PHONY: sqlc

sqlc:
	docker run --rm -v "$(CURDIR):/src" -w /src sqlc/sqlc:$(SQLC_VERSION) generate

## ── Tests ───────────────────────────────────────────

.PHONY: test

test:
	go test -v ./...

## ── Run ─────────────────────────────────────────────

.PHONY: run

run:
	go run cmd/api/main.go
