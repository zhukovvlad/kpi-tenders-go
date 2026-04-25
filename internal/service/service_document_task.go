package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type DocumentTaskService struct {
	repo         repository.Querier
	pythonClient workerPythonClient // nil when Python worker is not configured
	log          *slog.Logger
}

func NewDocumentTaskService(repo repository.Querier, pythonClient workerPythonClient, log *slog.Logger) *DocumentTaskService {
	return &DocumentTaskService{repo: repo, pythonClient: pythonClient, log: log}
}

func (s *DocumentTaskService) Create(ctx context.Context, params repository.CreateDocumentTaskParams) (repository.DocumentTask, error) {
	task, err := s.repo.CreateDocumentTask(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		if pgutil.IsUniqueViolation(err, "uq_document_tasks_document_module") {
			return repository.DocumentTask{}, errs.New(errs.CodeConflict, "task for this module already exists", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	if s.pythonClient == nil {
		return task, nil
	}

	// Detach from the request context so a client disconnect does not cancel
	// the best-effort trigger. Apply a short timeout to avoid stalling.
	triggerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	// Fetch the document to get storage_path for the Python worker trigger.
	doc, err := s.repo.GetDocument(triggerCtx, repository.GetDocumentParams{
		ID:             params.DocumentID,
		OrganizationID: params.OrganizationID,
	})
	if err != nil {
		// Task is already persisted — log and return without triggering Python.
		s.log.Error("documentTask: failed to fetch document for trigger", "task_id", task.ID, "document_id", params.DocumentID, "err", err)
		return task, nil
	}

	if err := s.pythonClient.Process(triggerCtx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  task.ModuleName,
		StoragePath: doc.StoragePath,
	}); err != nil {
		// Best-effort: task is already in DB, caller can retry.
		s.log.Error("documentTask: failed to trigger python worker", "task_id", task.ID, "err", err)
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
