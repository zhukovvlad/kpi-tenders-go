package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type InvitationService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewInvitationService(repo repository.Querier, log *slog.Logger) *InvitationService {
	return &InvitationService{repo: repo, log: log}
}

func (s *InvitationService) Create(ctx context.Context, orgID uuid.UUID, email, role string, invitedBy uuid.UUID, tokenHash string, expiresAt time.Time) (repository.UserInvitation, error) {
	inv, err := s.repo.CreateUserInvitation(ctx, repository.CreateUserInvitationParams{
		OrganizationID: orgID,
		Email:          email,
		Role:           role,
		InvitedBy:      pgtype.UUID{Bytes: invitedBy, Valid: true},
		TokenHash:      tokenHash,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "uq_user_invitations_org_email_active") {
			return repository.UserInvitation{}, errs.New(errs.CodeConflict, "active invitation for this email already exists", err)
		}
		s.log.Error("create invitation failed", "err", err, "org_id", orgID, "email", email)
		return repository.UserInvitation{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return inv, nil
}

func (s *InvitationService) GetByTokenHash(ctx context.Context, tokenHash string) (repository.UserInvitation, error) {
	inv, err := s.repo.GetUserInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.UserInvitation{}, errs.New(errs.CodeNotFound, "invitation not found", err)
		}
		s.log.Error("get invitation by token hash failed", "err", err)
		return repository.UserInvitation{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return inv, nil
}

func (s *InvitationService) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]repository.UserInvitation, error) {
	invitations, err := s.repo.ListUserInvitationsByOrg(ctx, orgID)
	if err != nil {
		s.log.Error("list invitations failed", "err", err, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return invitations, nil
}

func (s *InvitationService) Accept(ctx context.Context, id uuid.UUID) (repository.UserInvitation, error) {
	inv, err := s.repo.AcceptUserInvitation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.UserInvitation{}, errs.New(errs.CodeNotFound, "invitation not found or already accepted", err)
		}
		s.log.Error("accept invitation failed", "err", err, "id", id)
		return repository.UserInvitation{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return inv, nil
}

func (s *InvitationService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteUserInvitation(ctx, repository.DeleteUserInvitationParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		s.log.Error("delete invitation failed", "err", err, "id", id, "org_id", orgID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "invitation not found", nil)
	}
	return nil
}
