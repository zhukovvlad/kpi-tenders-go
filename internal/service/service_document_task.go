package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"go-kpi-tenders/internal/pgutil"
	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// DocumentTaskService coordinates public document-task CRUD operations and the
// best-effort initial publish to the Python worker queue.
type DocumentTaskService struct {
	repo         repository.Querier
	pythonClient workerPythonClient // nil when Python worker is not configured
	log          *slog.Logger
}

// NewDocumentTaskService constructs a task service. pythonClient may be nil in
// tests or degraded local setups; in that case tasks are persisted but not queued.
func NewDocumentTaskService(repo repository.Querier, pythonClient workerPythonClient, log *slog.Logger) *DocumentTaskService {
	return &DocumentTaskService{repo: repo, pythonClient: pythonClient, log: log}
}

// Create validates a public task request, persists it, and queues the worker
// message. Only modules whose inputs can be derived from the original document
// are accepted here; chained modules are created by WorkerService.
func (s *DocumentTaskService) Create(ctx context.Context, params repository.CreateDocumentTaskParams) (repository.DocumentTask, error) {
	// Validate before INSERT so callers get a clear validation_failed error
	// instead of a persisted task that can never be queued.
	if err := pythonworker.ValidateModule(params.ModuleName); err != nil {
		return repository.DocumentTask{}, errs.New(errs.CodeValidationFailed, fmt.Sprintf("unsupported module: %q", params.ModuleName), err)
	}

	// The public API accepts 'convert' and 'extract'. Modules like 'anonymize'
	// require a derived artifact path as input and are triggered internally
	// by the worker service after convert completes.
	if params.ModuleName != moduleConvert && params.ModuleName != moduleExtract {
		return repository.DocumentTask{}, errs.New(errs.CodeValidationFailed,
			fmt.Sprintf("module %q cannot be created via the public API; only %q and %q are allowed", params.ModuleName, moduleConvert, moduleExtract), nil)
	}

	var extractionKeys []map[string]any
	if params.ModuleName == moduleExtract {
		// Load keys before INSERT. If the tenant key list cannot be read, avoid
		// creating a pending extract task that would be queued without its schema.
		var err error
		extractionKeys, err = extractionKeyPayloads(ctx, s.repo, params.OrganizationID)
		if err != nil {
			return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
		}
	}

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

	// input_storage_path was filled by the SQL subquery (= document.storage_path).
	// Use it directly instead of an extra round-trip to fetch the document.
	req := pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  task.ModuleName,
		StoragePath: task.InputStoragePath,
	}
	if task.ModuleName == moduleExtract {
		req.Kwargs = map[string]any{"extraction_keys": extractionKeys}
	}

	if err := s.pythonClient.Process(triggerCtx, req); err != nil {
		// Best-effort: task is already in DB, caller can retry.
		s.log.Error("documentTask: failed to trigger python worker", "task_id", task.ID, "err", err)
	}

	return task, nil
}

// Get returns one task visible to the given organization.
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

// ListByDocument returns all tasks for a tenant-owned document.
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

// UpdateStatus changes task status through the public tenant-scoped API.
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

// Delete removes one task when it belongs to the provided organization.
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
