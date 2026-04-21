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

type DocumentTaskService struct {
	repo repository.Querier
	log  *slog.Logger
}

func NewDocumentTaskService(repo repository.Querier, log *slog.Logger) *DocumentTaskService {
	return &DocumentTaskService{repo: repo, log: log}
}

func (s *DocumentTaskService) Create(ctx context.Context, params repository.CreateDocumentTaskParams) (repository.DocumentTask, error) {
	task, err := s.repo.CreateDocumentTask(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return task, nil
}

func (s *DocumentTaskService) Get(ctx context.Context, id, orgID uuid.UUID) (repository.DocumentTask, error) {
	task, err := s.repo.GetDocumentTask(ctx, repository.GetDocumentTaskParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "task not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return task, nil
}

func (s *DocumentTaskService) ListByDocument(ctx context.Context, documentID, orgID uuid.UUID) ([]repository.DocumentTask, error) {
	tasks, err := s.repo.ListTasksByDocument(ctx, repository.ListTasksByDocumentParams{
		DocumentID:     documentID,
		OrganizationID: orgID,
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return tasks, nil
}

func (s *DocumentTaskService) UpdateStatus(ctx context.Context, params repository.UpdateDocumentTaskStatusParams) (repository.DocumentTask, error) {
	task, err := s.repo.UpdateDocumentTaskStatus(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "task not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return task, nil
}

func (s *DocumentTaskService) Delete(ctx context.Context, id, orgID uuid.UUID) error {
	rows, err := s.repo.DeleteDocumentTask(ctx, repository.DeleteDocumentTaskParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		return errs.New(errs.CodeInternalError, "internal server error", err)
	}
	if rows == 0 {
		return errs.New(errs.CodeNotFound, "task not found", nil)
	}
	return nil
}
