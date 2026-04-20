// Package store wraps the SQLC-generated repository layer with transaction
// support. Keeping this file outside internal/repository/ ensures it survives
// `make sqlc` regeneration without mixing hand-written and generated code.
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-kpi-tenders/internal/repository"
)

// Store extends repository.Querier with atomic transaction execution.
// Services that need transactions accept Store; read-only services accept
// the lighter repository.Querier.
type Store interface {
	repository.Querier
	ExecTx(ctx context.Context, fn func(q repository.Querier) error) error
}

// SQLStore is the production implementation backed by a pgxpool.
type SQLStore struct {
	*repository.Queries
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) Store {
	return &SQLStore{Queries: repository.New(db), db: db}
}

// ExecTx begins a transaction, calls fn with a transaction-bound Querier,
// commits on success, and rolls back on any error.
func (s *SQLStore) ExecTx(ctx context.Context, fn func(q repository.Querier) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
