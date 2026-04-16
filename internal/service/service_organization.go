package service

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

var innRegexp = regexp.MustCompile(`^\d{10}$`)

type OrganizationService struct {
	repo *repository.Queries
	db   *pgxpool.Pool
	log  *slog.Logger
}

func NewOrganizationService(repo *repository.Queries, db *pgxpool.Pool, log *slog.Logger) *OrganizationService {
	return &OrganizationService{repo: repo, db: db, log: log}
}

type RegisterParams struct {
	OrgName  string
	INN      string // optional, pass "" to skip; if provided must be 10 digits
	Email    string
	Password string
	FullName string
}

// parseINN trims and validates an INN string. Returns a valid pgtype.Text if
// non-empty, pgtype.Text{} (NULL) if empty, or a validation error if malformed.
func parseINN(raw string) (pgtype.Text, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return pgtype.Text{}, nil
	}
	if !innRegexp.MatchString(s) {
		return pgtype.Text{}, errs.New(errs.CodeValidationFailed, "INN must be exactly 10 digits", nil)
	}
	return pgtype.Text{String: s, Valid: true}, nil
}

// Register creates a new organization and its first admin user in a single
// transaction. On success it returns the created org and user records.
func (s *OrganizationService) Register(ctx context.Context, p RegisterParams) (repository.Organization, repository.User, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return repository.Organization{}, repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := s.repo.WithTx(tx)

	inn, err := parseINN(p.INN)
	if err != nil {
		return repository.Organization{}, repository.User{}, err
	}

	org, err := qtx.CreateOrganization(ctx, repository.CreateOrganizationParams{
		Name: p.OrgName,
		Inn:  inn,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "organizations_inn_key") {
			return repository.Organization{}, repository.User{}, errs.New(errs.CodeConflict, "INN already in use", err)
		}
		return repository.Organization{}, repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return repository.Organization{}, repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	user, err := qtx.CreateUser(ctx, repository.CreateUserParams{
		OrganizationID: org.ID,
		Email:          p.Email,
		PasswordHash:   string(hash),
		FullName:       p.FullName,
		Role:           "admin",
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "users_email_key") {
			return repository.Organization{}, repository.User{}, errs.New(errs.CodeConflict, "email already in use", err)
		}
		return repository.Organization{}, repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return repository.Organization{}, repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	s.log.Info("organization registered",
		slog.String("org_id", org.ID.String()),
		slog.String("user_id", user.ID.String()),
	)
	return org, user, nil
}

// GetByID returns an organization by its primary key.
func (s *OrganizationService) GetByID(ctx context.Context, id uuid.UUID) (repository.Organization, error) {
	org, err := s.repo.GetOrganizationByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Organization{}, errs.New(errs.CodeNotFound, "organization not found", err)
		}
		return repository.Organization{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return org, nil
}

// Update changes the name and/or INN of an organization.
// inn == nil means "leave unchanged"; inn pointing to "" means "clear INN".
// The update is performed atomically via a single SQL statement — no read-modify-write.
func (s *OrganizationService) Update(ctx context.Context, id uuid.UUID, name string, inn *string) (repository.Organization, error) {
	var innVal pgtype.Text
	setInn := inn != nil
	if inn != nil {
		parsed, err := parseINN(*inn)
		if err != nil {
			return repository.Organization{}, err
		}
		innVal = parsed
	}

	org, err := s.repo.UpdateOrganization(ctx, repository.UpdateOrganizationParams{
		ID:     id,
		Name:   name,
		Inn:    innVal,
		SetInn: setInn,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Organization{}, errs.New(errs.CodeNotFound, "organization not found", err)
		}
		if pgutil.IsUniqueViolation(err, "organizations_inn_key") {
			return repository.Organization{}, errs.New(errs.CodeConflict, "INN already in use", err)
		}
		return repository.Organization{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return org, nil
}

// Delete removes an organization and all its dependent records.
// Returns a not_found error when no row matched.
func (s *OrganizationService) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := s.repo.DeleteOrganization(ctx, id)
	if err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "organization not found", nil)
	}
	return nil
}

