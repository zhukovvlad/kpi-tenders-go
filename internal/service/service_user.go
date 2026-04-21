package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type UserService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewUserService(repo repository.Querier, log *slog.Logger) *UserService {
	return &UserService{repo: repo, log: log}
}

type CreateUserParams struct {
	OrgID    uuid.UUID
	Email    string
	Password string
	FullName string
	Role     string // "admin" or "member"
}

func (s *UserService) Create(ctx context.Context, p CreateUserParams) (repository.User, error) {
	if p.Role != "admin" && p.Role != "member" {
		return repository.User{}, errs.New(errs.CodeValidationFailed, "role must be admin or member", nil)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	user, err := s.repo.CreateUser(ctx, repository.CreateUserParams{
		OrganizationID: p.OrgID,
		Email:          p.Email,
		PasswordHash:   string(hash),
		FullName:       p.FullName,
		Role:           p.Role,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "users_email_key") {
			return repository.User{}, errs.New(errs.CodeConflict, "email already in use", err)
		}
		return repository.User{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return user, nil
}

func (s *UserService) List(ctx context.Context, orgID uuid.UUID) ([]repository.ListUsersByOrganizationRow, error) {
	users, err := s.repo.ListUsersByOrganization(ctx, orgID)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return users, nil
}

type UpdateUserParams struct {
	UserID uuid.UUID
	OrgID  uuid.UUID
	Role   *string // nil = не менять
	Active *bool   // nil = не менять
}

func (s *UserService) Update(ctx context.Context, p UpdateUserParams) (repository.UpdateUserRow, error) {
	if p.Role != nil && *p.Role != "admin" && *p.Role != "member" {
		return repository.UpdateUserRow{}, errs.New(errs.CodeValidationFailed, "role must be admin or member", nil)
	}

	roleParam := pgtype.Text{}
	if p.Role != nil {
		roleParam = pgtype.Text{String: *p.Role, Valid: true}
	}

	activeParam := pgtype.Bool{}
	if p.Active != nil {
		activeParam = pgtype.Bool{Bool: *p.Active, Valid: true}
	}

	user, err := s.repo.UpdateUser(ctx, repository.UpdateUserParams{
		ID:             p.UserID,
		OrganizationID: p.OrgID,
		Role:           roleParam,
		IsActive:       activeParam,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.UpdateUserRow{}, errs.New(errs.CodeNotFound, "user not found", err)
		}
		return repository.UpdateUserRow{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return user, nil
}

func (s *UserService) Deactivate(ctx context.Context, userID, orgID uuid.UUID) (repository.UpdateUserRow, error) {
	f := false
	return s.Update(ctx, UpdateUserParams{
		UserID: userID,
		OrgID:  orgID,
		Active: &f,
	})
}
