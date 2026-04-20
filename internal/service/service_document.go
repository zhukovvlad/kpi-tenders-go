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
	docs, err := s.repo.ListDocumentsByOrganization(ctx, orgID)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) ListBySite(ctx context.Context, siteID uuid.UUID) ([]repository.Document, error) {
	docs, err := s.repo.ListDocumentsBySite(ctx, pgtype.UUID{Bytes: siteID, Valid: true})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return docs, nil
}

func (s *DocumentService) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteDocument(ctx, id); err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return nil
}

// ── Document Tasks ──────────────────────────────────

func (s *DocumentService) CreateTask(ctx context.Context, params repository.CreateDocumentTaskParams) (repository.DocumentTask, error) {
	task, err := s.repo.CreateDocumentTask(ctx, params)
	if err != nil {
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return task, nil
}

func (s *DocumentService) GetTask(ctx context.Context, id uuid.UUID) (repository.DocumentTask, error) {
	task, err := s.repo.GetDocumentTask(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "task not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return task, nil
}

func (s *DocumentService) ListTasks(ctx context.Context, documentID uuid.UUID) ([]repository.DocumentTask, error) {
	tasks, err := s.repo.ListTasksByDocument(ctx, documentID)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return tasks, nil
}

func (s *DocumentService) UpdateTaskStatus(ctx context.Context, params repository.UpdateDocumentTaskStatusParams) (repository.DocumentTask, error) {
	task, err := s.repo.UpdateDocumentTaskStatus(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "task not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return task, nil
}

func (s *DocumentService) DeleteTask(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteDocumentTask(ctx, id); err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return nil
}
