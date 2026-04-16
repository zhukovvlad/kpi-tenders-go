package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"go-kpi-tenders/internal/repository"
)

type DocumentService struct {
	repo *repository.Queries
	log  *slog.Logger
}

func NewDocumentService(repo *repository.Queries, log *slog.Logger) *DocumentService {
	return &DocumentService{repo: repo, log: log}
}

func (s *DocumentService) Get(ctx context.Context, id uuid.UUID) (repository.Document, error) {
	return s.repo.GetDocument(ctx, id)
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
