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
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/store"
	"go-kpi-tenders/pkg/errs"
)

var innRegexp = regexp.MustCompile(`^\d{10}$`)

type OrganizationService struct {
	store store.Store
	log   *slog.Logger
}

func NewOrganizationService(s store.Store, log *slog.Logger) *OrganizationService {
	return &OrganizationService{store: s, log: log}
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
// transaction. bcrypt is run before the transaction to avoid holding a
// DB connection open during the expensive hash computation.
func (s *OrganizationService) Register(ctx context.Context, p RegisterParams) (repository.Organization, repository.User, error) {
	inn, err := parseINN(p.INN)
	if err != nil {
		return repository.Organization{}, repository.User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return repository.Organization{}, repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	var org repository.Organization
	var user repository.User

	txErr := s.store.ExecTx(ctx, func(q repository.Querier) error {
		var txErr error

		org, txErr = q.CreateOrganization(ctx, repository.CreateOrganizationParams{
			Name: p.OrgName,
			Inn:  inn,
		})
		if txErr != nil {
			if pgutil.IsUniqueViolation(txErr, "organizations_inn_key") {
				return errs.New(errs.CodeConflict, "INN already in use", txErr)
			}
			return errs.New(errs.CodeInternalError, "internal server error", txErr)
		}

		user, txErr = q.CreateUser(ctx, repository.CreateUserParams{
			OrganizationID: org.ID,
			Email:          p.Email,
			PasswordHash:   string(hash),
			FullName:       p.FullName,
			Role:           "admin",
		})
		if txErr != nil {
			if pgutil.IsUniqueViolation(txErr, "users_email_key") {
				return errs.New(errs.CodeConflict, "email already in use", txErr)
			}
			return errs.New(errs.CodeInternalError, "internal server error", txErr)
		}

		return nil
	})

	if txErr != nil {
		return repository.Organization{}, repository.User{}, txErr
	}

	s.log.Info("organization registered",
		slog.String("org_id", org.ID.String()),
		slog.String("user_id", user.ID.String()),
	)
	return org, user, nil
}

// GetByID returns an organization by its primary key.
func (s *OrganizationService) GetByID(ctx context.Context, id uuid.UUID) (repository.Organization, error) {
	org, err := s.store.GetOrganizationByID(ctx, id)
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

	org, err := s.store.UpdateOrganization(ctx, repository.UpdateOrganizationParams{
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
func (s *OrganizationService) Delete(ctx context.Context, id uuid.UUID) error {
	rows, err := s.store.DeleteOrganization(ctx, id)
	if err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "organization not found", nil)
	}
	return nil
}
