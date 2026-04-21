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

## ── Code Generation ─────────────────────────────────

.PHONY: sqlc gen-secrets

sqlc:
	go tool sqlc generate

gen-secrets:
	go run ./scripts/gen-secrets

## ── Tests ───────────────────────────────────────────

.PHONY: test test-unit test-integration mock

# Unit tests only (no Docker required).
test-unit:
	go test -v -race -count=1 ./internal/... ./cmd/... ./pkg/...

# Integration tests — requires Docker (testcontainers spins up pgvector/pgvector:pg16).
test-integration:
	go test -v -race -tags integration -timeout 120s -count=1 ./tests/integration/...

# Run everything: unit + integration.
test: test-unit test-integration

# Regenerate MockStore from the Store interface via mockery.
# NOTE: after regeneration, restore the hand-written ExecTx method in
# internal/store/mock/mock_store.go (see file header for details).
mock:
	go run github.com/vektra/mockery/v2@v2.53.6 \
		--dir=internal/store \
		--name=Store \
		--output=internal/store/mock \
		--outpkg=mock \
		--filename=mock_store.go

## ── Run ─────────────────────────────────────────────

.PHONY: run

run:
	go run cmd/api/main.go
