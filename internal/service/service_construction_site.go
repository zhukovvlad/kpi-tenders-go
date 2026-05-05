package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
		s.log.Error("create construction site failed", "err", err, "org_id", params.OrganizationID)
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
		s.log.Error("get construction site failed", "err", err, "id", id, "org_id", orgID)
		return repository.ConstructionSite{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return site, nil
}

func (s *ConstructionSiteService) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repository.ConstructionSite, error) {
	sites, err := s.repo.ListConstructionSitesByOrganization(ctx, orgID)
	if err != nil {
		s.log.Error("list construction sites failed", "err", err, "org_id", orgID)
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
		s.log.Error("update construction site failed", "err", err, "id", params.ID, "org_id", params.OrganizationID)
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
		s.log.Error("delete construction site failed", "err", err, "id", id, "org_id", orgID)
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "construction site not found", nil)
	}
	return nil
}

func (s *ConstructionSiteService) ListByParent(ctx context.Context, orgID, parentID uuid.UUID) ([]repository.ConstructionSite, error) {
	sites, err := s.repo.ListConstructionSitesByParent(ctx, repository.ListConstructionSitesByParentParams{
		OrganizationID: orgID,
		ParentID:       pgtype.UUID{Bytes: parentID, Valid: true},
	})
	if err != nil {
		s.log.Error("list construction sites by parent failed", "err", err, "org_id", orgID, "parent_id", parentID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return sites, nil
}

func (s *ConstructionSiteService) ListRoot(ctx context.Context, orgID uuid.UUID) ([]repository.ConstructionSite, error) {
	sites, err := s.repo.ListRootConstructionSites(ctx, orgID)
	if err != nil {
		s.log.Error("list root construction sites failed", "err", err, "org_id", orgID)
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return sites, nil
}

func (s *ConstructionSiteService) UpdateCover(ctx context.Context, id, orgID uuid.UUID, coverImagePath *string) (repository.ConstructionSite, error) {
	var cover pgtype.Text
	if coverImagePath != nil {
		cover = pgtype.Text{String: *coverImagePath, Valid: true}
	}
	site, err := s.repo.UpdateConstructionSiteCover(ctx, repository.UpdateConstructionSiteCoverParams{
		ID:             id,
		OrganizationID: orgID,
		CoverImagePath: cover,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ConstructionSite{}, errs.New(errs.CodeNotFound, "construction site not found", err)
		}
		s.log.Error("update site cover failed", "err", err, "id", id, "org_id", orgID)
		return repository.ConstructionSite{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return site, nil
}

func (s *ConstructionSiteService) UpdateType(ctx context.Context, id, orgID uuid.UUID, siteType *string) (repository.ConstructionSite, error) {
	var st pgtype.Text
	if siteType != nil {
		st = pgtype.Text{String: *siteType, Valid: true}
	}
	site, err := s.repo.UpdateConstructionSiteType(ctx, repository.UpdateConstructionSiteTypeParams{
		ID:             id,
		OrganizationID: orgID,
		SiteType:       st,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ConstructionSite{}, errs.New(errs.CodeNotFound, "construction site not found", err)
		}
		s.log.Error("update site type failed", "err", err, "id", id, "org_id", orgID)
		return repository.ConstructionSite{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return site, nil
}
