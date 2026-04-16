package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"go-kpi-tenders/internal/repository"
)

var (
	ErrOrgNotFound      = errors.New("organization not found")
	ErrEmailTaken       = errors.New("email already in use")
	ErrINNTaken         = errors.New("INN already in use")
)

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
	INN      string // optional, pass "" to skip
	Email    string
	Password string
	FullName string
}

// Register creates a new organization and its first admin user in a single
// transaction. On success it returns the created org and user records.
func (s *OrganizationService) Register(ctx context.Context, p RegisterParams) (repository.Organization, repository.User, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return repository.Organization{}, repository.User{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qtx := s.repo.WithTx(tx)

	inn := pgtype.Text{}
	if p.INN != "" {
		inn = pgtype.Text{String: p.INN, Valid: true}
	}

	org, err := qtx.CreateOrganization(ctx, repository.CreateOrganizationParams{
		Name: p.OrgName,
		Inn:  inn,
	})
	if err != nil {
		if isUniqueViolation(err, "organizations_inn_key") {
			return repository.Organization{}, repository.User{}, ErrINNTaken
		}
		return repository.Organization{}, repository.User{}, fmt.Errorf("create org: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
	if err != nil {
		return repository.Organization{}, repository.User{}, fmt.Errorf("hash password: %w", err)
	}

	user, err := qtx.CreateUser(ctx, repository.CreateUserParams{
		OrganizationID: org.ID,
		Email:          p.Email,
		PasswordHash:   string(hash),
		FullName:       p.FullName,
		Role:           "admin",
	})
	if err != nil {
		if isUniqueViolation(err, "users_email_key") {
			return repository.Organization{}, repository.User{}, ErrEmailTaken
		}
		return repository.Organization{}, repository.User{}, fmt.Errorf("create user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return repository.Organization{}, repository.User{}, fmt.Errorf("commit tx: %w", err)
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
			return repository.Organization{}, ErrOrgNotFound
		}
		return repository.Organization{}, fmt.Errorf("get org: %w", err)
	}
	return org, nil
}

// Update changes the name and/or INN of an organization.
func (s *OrganizationService) Update(ctx context.Context, id uuid.UUID, name, inn string) (repository.Organization, error) {
	innVal := pgtype.Text{}
	if inn != "" {
		innVal = pgtype.Text{String: inn, Valid: true}
	}

	org, err := s.repo.UpdateOrganization(ctx, repository.UpdateOrganizationParams{
		ID:   id,
		Name: name,
		Inn:  innVal,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Organization{}, ErrOrgNotFound
		}
		if isUniqueViolation(err, "organizations_inn_key") {
			return repository.Organization{}, ErrINNTaken
		}
		return repository.Organization{}, fmt.Errorf("update org: %w", err)
	}
	return org, nil
}

// Delete removes an organization and all its dependent records.
func (s *OrganizationService) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteOrganization(ctx, id); err != nil {
		return fmt.Errorf("delete org: %w", err)
	}
	return nil
}

// isUniqueViolation checks whether err is a PostgreSQL unique-constraint
// violation for the given constraint name.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		// 23505 = unique_violation
		if pgErr.SQLState() == "23505" {
			return constraint == "" || contains(err.Error(), constraint)
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
