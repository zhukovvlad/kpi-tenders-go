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

type DocumentService struct {
	repo *repository.Queries
	log  *slog.Logger
}

func NewDocumentService(repo *repository.Queries, log *slog.Logger) *DocumentService {
	return &DocumentService{repo: repo, log: log}
}

func (s *DocumentService) Get(ctx context.Context, id uuid.UUID) (repository.Document, error) {
	doc, err := s.repo.GetDocument(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.Document{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		return repository.Document{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return doc, nil
}

func (s *DocumentService) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repository.Document, error) {
	return s.repo.ListDocumentsByOrganization(ctx, orgID)
}

func (s *DocumentService) Create(ctx context.Context, params repository.CreateDocumentParams) (repository.Document, error) {
	return s.repo.CreateDocument(ctx, params)
}

func (s *DocumentService) UpdateStatus(ctx context.Context, params repository.UpdateDocumentStatusParams) (repository.Document, error) {
	return s.repo.UpdateDocumentStatus(ctx, params)
}

func (s *DocumentService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteDocument(ctx, id)
}
