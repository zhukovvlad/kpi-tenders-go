package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type ContractKindService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewContractKindService(repo repository.Querier, log *slog.Logger) *ContractKindService {
	return &ContractKindService{repo: repo, log: log}
}

func (s *ContractKindService) List(ctx context.Context, orgID uuid.UUID) ([]repository.DocumentContractKind, error) {
	kinds, err := s.repo.ListContractKindsByOrg(ctx, orgID)
	if err != nil {
		s.log.Error("list contract kinds failed", "err", err, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return kinds, nil
}

func (s *ContractKindService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.DocumentContractKind, error) {
	kind, err := s.repo.GetContractKind(ctx, repository.GetContractKindParams{
		ID:      id,
		Column2: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentContractKind{}, errs.New(errs.CodeNotFound, "contract kind not found", err)
		}
		s.log.Error("get contract kind failed", "err", err, "id", id, "org_id", orgID)
		return repository.DocumentContractKind{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return kind, nil
}

func (s *ContractKindService) Create(ctx context.Context, orgID uuid.UUID, displayName string, sortOrder int16, isActive bool) (repository.DocumentContractKind, error) {
	kind, err := s.repo.CreateContractKind(ctx, repository.CreateContractKindParams{
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		DisplayName:    displayName,
		SortOrder:      sortOrder,
		IsActive:       isActive,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "uq_contract_kinds_org_name") {
			return repository.DocumentContractKind{}, errs.New(errs.CodeConflict, "contract kind with this name already exists", err)
		}
		s.log.Error("create contract kind failed", "err", err, "org_id", orgID)
		return repository.DocumentContractKind{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return kind, nil
}

func (s *ContractKindService) Update(ctx context.Context, id, orgID uuid.UUID, displayName string, sortOrder int16, isActive bool) (repository.DocumentContractKind, error) {
	kind, err := s.repo.UpdateContractKind(ctx, repository.UpdateContractKindParams{
		ID:             id,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		DisplayName:    displayName,
		SortOrder:      sortOrder,
		IsActive:       isActive,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentContractKind{}, errs.New(errs.CodeNotFound, "contract kind not found", err)
		}
		if pgutil.IsUniqueViolation(err, "uq_contract_kinds_org_name") {
			return repository.DocumentContractKind{}, errs.New(errs.CodeConflict, "contract kind with this name already exists", err)
		}
		s.log.Error("update contract kind failed", "err", err, "id", id, "org_id", orgID)
		return repository.DocumentContractKind{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return kind, nil
}

func (s *ContractKindService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteContractKind(ctx, repository.DeleteContractKindParams{
		ID:             id,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
	})
	if err != nil {
		s.log.Error("delete contract kind failed", "err", err, "id", id, "org_id", orgID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "contract kind not found", nil)
	}
	return nil
}
