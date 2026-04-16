// Package pgutil provides helpers for working with PostgreSQL-specific errors.
package pgutil

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation reports whether err is a PostgreSQL unique-violation
// (SQLSTATE 23505) for the given constraint name.
// Pass constraint == "" to match any unique violation.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
	}
	return false
}
