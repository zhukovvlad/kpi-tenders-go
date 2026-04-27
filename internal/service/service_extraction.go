package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// ExtractionService orchestrates the semantic mapping and data extraction
// pipeline for documents. It initiates the resolve_keys → extract chain.
type ExtractionService struct {
	repo         repository.Querier
	pythonClient workerPythonClient // nil when Python worker is not configured
	log          *slog.Logger
}

// NewExtractionService creates a new ExtractionService.
func NewExtractionService(repo repository.Querier, pythonClient workerPythonClient, log *slog.Logger) *ExtractionService {
	return &ExtractionService{repo: repo, pythonClient: pythonClient, log: log}
}

// existingKeyEntry is the representation of a known extraction key sent to
// Python so it can match user questions to already-defined keys.
type existingKeyEntry struct {
	KeyName     string `json:"key_name"`
	SourceQuery string `json:"source_query"`
	DataType    string `json:"data_type"`
}

// Initiate starts the extraction pipeline for a document.
//   - Fetches all extraction keys visible to the tenant (org-specific + system).
//   - Creates a resolve_keys document_task (idempotent).
//   - Enqueues a Celery resolve_keys task with the raw questions and existing keys.
//
// Returns the created document_task so the caller can return task_id to the client.
func (s *ExtractionService) Initiate(
	ctx context.Context,
	documentID, orgID uuid.UUID,
	questions []string,
) (repository.DocumentTask, error) {
	if len(questions) == 0 {
		return repository.DocumentTask{}, errs.New(errs.CodeValidationFailed, "questions must not be empty", nil)
	}

	// Validate ownership and retrieve the document's storage_path, which is
	// used as input_storage_path for the resolve_keys task (required by the
	// DB NOT NULL constraint; the Python worker may use it for context).
	doc, err := s.repo.GetDocument(ctx, repository.GetDocumentParams{
		ID:             documentID,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.DocumentTask{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	// Collect all extraction keys visible to this tenant.
	rawKeys, err := s.repo.ListExtractionKeysByOrg(ctx, orgID)
	if err != nil {
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "failed to fetch extraction keys", err)
	}
	existingKeys := make([]existingKeyEntry, len(rawKeys))
	for i, k := range rawKeys {
		existingKeys[i] = existingKeyEntry{
			KeyName:     k.KeyName,
			SourceQuery: k.SourceQuery,
			DataType:    k.DataType,
		}
	}

	// Create the resolve_keys task. ON CONFLICT DO NOTHING makes this
	// idempotent: pgx.ErrNoRows means the task already exists.
	task, err := s.repo.CreateDocumentTaskInternal(ctx, repository.CreateDocumentTaskInternalParams{
		DocumentID:       documentID,
		ModuleName:       moduleResolveKeys,
		InputStoragePath: doc.StoragePath,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.DocumentTask{}, errs.New(errs.CodeConflict, "extraction task already exists for this document", err)
	}
	if err != nil {
		return repository.DocumentTask{}, errs.New(errs.CodeInternalError, "failed to create extraction task", err)
	}

	if s.pythonClient == nil {
		return task, nil
	}

	// Detach from the request context so a client disconnect does not cancel
	// the best-effort trigger. Apply a short timeout to avoid stalling.
	triggerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if err := s.pythonClient.Process(triggerCtx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  moduleResolveKeys,
		StoragePath: doc.StoragePath,
		Kwargs: map[string]any{
			"raw_questions": questions,
			"existing_keys": existingKeys,
		},
	}); err != nil {
		// Best-effort: task is already persisted; caller can retry.
		s.log.Error("extraction: failed to trigger resolve_keys worker",
			"task_id", task.ID, "err", err)
	}

	return task, nil
}

// toOrgUUID converts a uuid.UUID to pgtype.UUID (nullable) with Valid=true.
func toOrgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
