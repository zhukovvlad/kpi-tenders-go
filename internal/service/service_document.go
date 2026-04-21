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

type DocumentService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewDocumentService(repo repository.Querier, log *slog.Logger) *DocumentService {
	return &DocumentService{repo: repo, log: log}
}

func (s *DocumentService) Create(ctx context.Context, params repository.CreateDocumentParams) (repository.Document, error) {
	doc, err := s.repo.CreateDocument(ctx, params)
	if err != nil {
		return repository.Document{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}

func (s *DocumentService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.Document, error) {
	doc, err := s.repo.GetDocument(ctx, repository.GetDocumentParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Document{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		return repository.Document{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}

func (s *DocumentService) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repository.Document, error) {
	docs, err := s.repo.ListDocumentsByOrganization(ctx, orgID)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) ListBySite(ctx context.Context, orgID, siteID uuid.UUID) ([]repository.Document, error) {
	docs, err := s.repo.ListDocumentsBySite(ctx, repository.ListDocumentsBySiteParams{
		OrganizationID: orgID,
		SiteID:         pgtype.UUID{Bytes: siteID, Valid: true},
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteDocument(ctx, repository.DeleteDocumentParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "document not found", nil)
	}
	return nil
}
