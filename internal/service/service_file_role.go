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

type FileRoleService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewFileRoleService(repo repository.Querier, log *slog.Logger) *FileRoleService {
	return &FileRoleService{repo: repo, log: log}
}

func (s *FileRoleService) List(ctx context.Context, orgID uuid.UUID) ([]repository.DocumentFileRole, error) {
	roles, err := s.repo.ListFileRolesByOrg(ctx, orgID)
	if err != nil {
		s.log.Error("list file roles failed", "err", err, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return roles, nil
}

func (s *FileRoleService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.DocumentFileRole, error) {
	role, err := s.repo.GetFileRole(ctx, repository.GetFileRoleParams{
		ID:      id,
		Column2: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentFileRole{}, errs.New(errs.CodeNotFound, "file role not found", err)
		}
		s.log.Error("get file role failed", "err", err, "id", id, "org_id", orgID)
		return repository.DocumentFileRole{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return role, nil
}

func (s *FileRoleService) Create(ctx context.Context, orgID uuid.UUID, displayName string, sortOrder int16, isActive bool) (repository.DocumentFileRole, error) {
	role, err := s.repo.CreateFileRole(ctx, repository.CreateFileRoleParams{
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		DisplayName:    displayName,
		SortOrder:      sortOrder,
		IsActive:       isActive,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "uq_file_roles_org_name") {
			return repository.DocumentFileRole{}, errs.New(errs.CodeConflict, "file role with this name already exists", err)
		}
		s.log.Error("create file role failed", "err", err, "org_id", orgID)
		return repository.DocumentFileRole{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return role, nil
}

func (s *FileRoleService) Update(ctx context.Context, id, orgID uuid.UUID, displayName string, sortOrder int16, isActive bool) (repository.DocumentFileRole, error) {
	role, err := s.repo.UpdateFileRole(ctx, repository.UpdateFileRoleParams{
		ID:             id,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		DisplayName:    displayName,
		SortOrder:      sortOrder,
		IsActive:       isActive,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentFileRole{}, errs.New(errs.CodeNotFound, "file role not found", err)
		}
		if pgutil.IsUniqueViolation(err, "uq_file_roles_org_name") {
			return repository.DocumentFileRole{}, errs.New(errs.CodeConflict, "file role with this name already exists", err)
		}
		s.log.Error("update file role failed", "err", err, "id", id, "org_id", orgID)
		return repository.DocumentFileRole{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return role, nil
}

func (s *FileRoleService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteFileRole(ctx, repository.DeleteFileRoleParams{
		ID:             id,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
	})
	if err != nil {
		s.log.Error("delete file role failed", "err", err, "id", id, "org_id", orgID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "file role not found", nil)
	}
	return nil
}
