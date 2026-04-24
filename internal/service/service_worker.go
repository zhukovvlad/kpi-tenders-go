package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// workerPythonClient is a consumer-side interface for the Python worker HTTP
// client. Using an interface here keeps WorkerService testable without a real
// HTTP server.
type workerPythonClient interface {
	Process(ctx context.Context, req pythonworker.ProcessRequest) error
}

// WorkerService handles callbacks from the Python worker and implements task
// chaining (e.g. convert → anonymize).
type WorkerService struct {
	repo         repository.Querier
	pythonClient workerPythonClient
	log          *slog.Logger
}

// NewWorkerService creates a new WorkerService.
func NewWorkerService(repo repository.Querier, pythonClient workerPythonClient, log *slog.Logger) *WorkerService {
	return &WorkerService{repo: repo, pythonClient: pythonClient, log: log}
}

// WorkerStatusUpdate is the request body from the Python worker when it
// reports progress on a task.
type WorkerStatusUpdate struct {
	Status        string          `json:"status"`
	CeleryTaskID  *string         `json:"celery_task_id,omitempty"`
	ResultPayload json.RawMessage `json:"result_payload,omitempty"`
	ErrorMessage  *string         `json:"error_message,omitempty"`
}

// HandleStatusUpdate persists the worker's status update and triggers the next
// module in the chain when applicable.
func (s *WorkerService) HandleStatusUpdate(ctx context.Context, taskID uuid.UUID, upd WorkerStatusUpdate) (repository.DocumentTask, error) {
	var celeryID pgtype.Text
	if upd.CeleryTaskID != nil {
		celeryID = pgtype.Text{String: *upd.CeleryTaskID, Valid: true}
	}

	var errMsg pgtype.Text
	if upd.ErrorMessage != nil {
		errMsg = pgtype.Text{String: *upd.ErrorMessage, Valid: true}
	}

	task, err := s.repo.UpdateWorkerTaskStatus(ctx, repository.UpdateWorkerTaskStatusParams{
		ID:            taskID,
		Status:        upd.Status,
		CeleryTaskID:  celeryID,
		ResultPayload: upd.ResultPayload,
		ErrorMessage:  errMsg,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "task not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "failed to update task", err)
	}

	// Chain: convert completed → trigger anonymize.
	if task.ModuleName == "convert" && task.Status == "completed" {
		if err := s.triggerAnonymize(ctx, task); err != nil {
			// Log but do not fail — the callback has already been persisted.
			s.log.Error("worker: failed to trigger anonymize", "task_id", task.ID, "err", err)
		}
	}

	return task, nil
}

type convertPayload struct {
	MDStoragePath string `json:"md_storage_path"`
}

func (s *WorkerService) triggerAnonymize(ctx context.Context, convertTask repository.DocumentTask) error {
	var payload convertPayload
	if err := json.Unmarshal(convertTask.ResultPayload, &payload); err != nil {
		return fmt.Errorf("parse convert payload: %w", err)
	}
	if payload.MDStoragePath == "" {
		return fmt.Errorf("md_storage_path is empty in convert result_payload")
	}

	// Idempotency guard: if an anonymize task already exists for this document,
	// skip creation. This prevents duplicate tasks when the worker retries a
	// "convert completed" callback.
	_, err := s.repo.GetDocumentTaskByDocumentModule(ctx, repository.GetDocumentTaskByDocumentModuleParams{
		DocumentID: convertTask.DocumentID,
		ModuleName: "anonymize",
	})
	if err == nil {
		s.log.Info("worker: anonymize task already exists, skipping", "document_id", convertTask.DocumentID)
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing anonymize task: %w", err)
	}

	anonTask, err := s.repo.CreateDocumentTaskInternal(ctx, repository.CreateDocumentTaskInternalParams{
		DocumentID: convertTask.DocumentID,
		ModuleName: "anonymize",
	})
	if err != nil {
		return fmt.Errorf("create anonymize task: %w", err)
	}

	return s.pythonClient.Process(ctx, pythonworker.ProcessRequest{
		TaskID:      anonTask.ID.String(),
		DocumentID:  anonTask.DocumentID.String(),
		ModuleName:  "anonymize",
		StoragePath: payload.MDStoragePath,
	})
}
