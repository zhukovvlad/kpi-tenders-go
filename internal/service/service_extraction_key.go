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

// ExtractionKeyService manages the extraction_keys reference table for a tenant.
// System keys (organization_id IS NULL) are visible to all tenants but can only
// be read — create/update/delete operate exclusively on tenant-scoped keys.
type ExtractionKeyService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewExtractionKeyService(repo repository.Querier, log *slog.Logger) *ExtractionKeyService {
	return &ExtractionKeyService{repo: repo, log: log}
}

// List returns all keys visible to the tenant: org-specific and system keys.
func (s *ExtractionKeyService) List(ctx context.Context, orgID uuid.UUID) ([]repository.ExtractionKey, error) {
	keys, err := s.repo.ListExtractionKeysByOrg(ctx, orgID)
	if err != nil {
		s.log.Error("list extraction keys failed", "err", err, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return keys, nil
}

// Get returns a single key visible to the tenant (org-specific or system).
// Returns CodeNotFound when the key does not exist or is not visible.
func (s *ExtractionKeyService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.ExtractionKey, error) {
	key, err := s.repo.GetExtractionKey(ctx, repository.GetExtractionKeyParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ExtractionKey{}, errs.New(errs.CodeNotFound, "extraction key not found", err)
		}
		s.log.Error("get extraction key failed", "err", err, "id", id, "org_id", orgID)
		return repository.ExtractionKey{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return key, nil
}

// Create inserts a new org-specific extraction key.
// Returns CodeConflict when a key with the same key_name already exists for the org.
func (s *ExtractionKeyService) Create(
	ctx context.Context,
	orgID uuid.UUID,
	keyName, sourceQuery, dataType string,
	displayName *string,
) (repository.ExtractionKey, error) {
	var dn pgtype.Text
	if displayName != nil {
		dn = pgtype.Text{String: *displayName, Valid: true}
	}
	key, err := s.repo.CreateExtractionKey(ctx, repository.CreateExtractionKeyParams{
		OrganizationID: orgID,
		KeyName:        keyName,
		SourceQuery:    sourceQuery,
		DataType:       dataType,
		DisplayName:    dn,
	})
	if err != nil {
		if pgutil.IsUniqueViolation(err, "uq_extraction_keys_org_name") {
			return repository.ExtractionKey{}, errs.New(errs.CodeConflict, "extraction key with this name already exists", err)
		}
		s.log.Error("create extraction key failed", "err", err, "org_id", orgID)
		return repository.ExtractionKey{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return key, nil
}

// Update partially updates an org-specific extraction key (PATCH semantics).
// System keys (organization_id IS NULL) cannot be updated; the query's WHERE
// clause will return no rows, resulting in CodeNotFound.
func (s *ExtractionKeyService) Update(
	ctx context.Context,
	id, orgID uuid.UUID,
	sourceQuery, dataType, displayName *string,
) (repository.ExtractionKey, error) {
	var sq, dt, dn pgtype.Text
	if sourceQuery != nil {
		sq = pgtype.Text{String: *sourceQuery, Valid: true}
	}
	if dataType != nil {
		dt = pgtype.Text{String: *dataType, Valid: true}
	}
	if displayName != nil {
		dn = pgtype.Text{String: *displayName, Valid: true}
	}
	key, err := s.repo.UpdateExtractionKey(ctx, repository.UpdateExtractionKeyParams{
		ID:             id,
		OrganizationID: orgID,
		SourceQuery:    sq,
		DataType:       dt,
		DisplayName:    dn,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ExtractionKey{}, errs.New(errs.CodeNotFound, "extraction key not found", err)
		}
		s.log.Error("update extraction key failed", "err", err, "id", id, "org_id", orgID)
		return repository.ExtractionKey{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return key, nil
}

// Delete removes an org-specific extraction key.
// System keys (organization_id IS NULL) cannot be deleted via this method.
// Returns CodeNotFound when the key does not exist or is a system key.
func (s *ExtractionKeyService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteExtractionKey(ctx, repository.DeleteExtractionKeyParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		s.log.Error("delete extraction key failed", "err", err, "id", id, "org_id", orgID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "extraction key not found", nil)
	}
	return nil
}
