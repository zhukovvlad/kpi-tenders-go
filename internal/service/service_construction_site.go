package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type ConstructionSiteService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewConstructionSiteService(repo repository.Querier, log *slog.Logger) *ConstructionSiteService {
	return &ConstructionSiteService{repo: repo, log: log}
}

func (s *ConstructionSiteService) Create(ctx context.Context, params repository.CreateConstructionSiteParams) (repository.ConstructionSite, error) {
	site, err := s.repo.CreateConstructionSite(ctx, params)
	if err != nil {
		return repository.ConstructionSite{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return site, nil
}

func (s *ConstructionSiteService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.ConstructionSite, error) {
	site, err := s.repo.GetConstructionSite(ctx, repository.GetConstructionSiteParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ConstructionSite{}, errs.New(errs.CodeNotFound, "construction site not found", err)
		}
		return repository.ConstructionSite{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return site, nil
}

func (s *ConstructionSiteService) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repository.ConstructionSite, error) {
	sites, err := s.repo.ListConstructionSitesByOrganization(ctx, orgID)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return sites, nil
}

func (s *ConstructionSiteService) Update(ctx context.Context, params repository.UpdateConstructionSiteParams) (repository.ConstructionSite, error) {
	site, err := s.repo.UpdateConstructionSite(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ConstructionSite{}, errs.New(errs.CodeNotFound, "construction site not found", err)
		}
		return repository.ConstructionSite{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return site, nil
}

func (s *ConstructionSiteService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteConstructionSite(ctx, repository.DeleteConstructionSiteParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "construction site not found", nil)
	}
	return nil
}
