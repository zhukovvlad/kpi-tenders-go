package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

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

// extractionPipeline is the consumer-side interface for the chunk of
// extraction-request orchestration that lives in ExtractionService. Keeping
// it narrow lets WorkerService stay testable and avoids circular dependencies.
type extractionPipeline interface {
	Progress(ctx context.Context, req repository.ExtractionRequest) error
	OnResolveKeysCompleted(ctx context.Context, task repository.DocumentTask) error
	OnExtractCompleted(ctx context.Context, task repository.DocumentTask) error
	MarkRequestFailed(ctx context.Context, requestID uuid.UUID, msg string)
}

// Module names used in task chaining.
const (
	moduleConvert     = "convert"
	moduleAnonymize   = "anonymize"
	moduleResolveKeys = "resolve_keys"
	moduleExtract     = "extract"
)

// Task status constants used for chaining checks and orphan cleanup.
const (
	statusCompleted = "completed"
	statusFailed    = "failed"
)

// WorkerService handles callbacks from the Python worker and implements task
// chaining (convert → anonymize) plus delegation to ExtractionService for
// extraction_request progression.
type WorkerService struct {
	repo         repository.Querier
	pythonClient workerPythonClient
	pipeline     extractionPipeline
	log          *slog.Logger
}

// NewWorkerService creates a new WorkerService. pythonClient and pipeline are
// both required dependencies.
func NewWorkerService(
	repo repository.Querier,
	pythonClient workerPythonClient,
	pipeline extractionPipeline,
	log *slog.Logger,
) *WorkerService {
	if pythonClient == nil {
		panic("WorkerService: pythonClient is required")
	}
	if pipeline == nil {
		panic("WorkerService: pipeline is required")
	}
	return &WorkerService{repo: repo, pythonClient: pythonClient, pipeline: pipeline, log: log}
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

	// Chain: convert completed → trigger anonymize, register artifact, and
	// progress any pending extraction_requests on the document (anonymize=false
	// requests can fire resolve_keys as soon as MD is ready).
	if task.ModuleName == moduleConvert && task.Status == statusCompleted {
		if err := s.triggerAnonymize(ctx, task); err != nil {
			// Log but do not fail — the callback has already been persisted.
			// Intentionally skip artifact registration here to preserve the
			// original result_payload (md_storage_path) for a future retry.
			s.log.Error("worker: failed to trigger anonymize", "task_id", task.ID, "err", err)
		} else {
			if err := runWithArtifactTimeout(ctx, task, s.registerConvertArtifacts); err != nil {
				s.log.Error("worker: failed to register convert artifacts", "task_id", task.ID, "err", err)
			}
		}
		s.progressPendingRequests(ctx, task.DocumentID)
	}

	// Register anonymize artifacts and progress pending requests that wanted
	// the anonymized version.
	if task.ModuleName == moduleAnonymize && task.Status == statusCompleted {
		if err := runWithArtifactTimeout(ctx, task, s.registerAnonymizeArtifacts); err != nil {
			s.log.Error("worker: failed to register anonymize artifacts", "task_id", task.ID, "err", err)
		}
		s.progressPendingRequests(ctx, task.DocumentID)
	}

	// Chain: resolve_keys completed → upsert keys, persist resolved_schema,
	// enqueue extract. Delegated to ExtractionService for orchestration.
	if task.ModuleName == moduleResolveKeys && task.Status == statusCompleted {
		if err := s.pipeline.OnResolveKeysCompleted(ctx, task); err != nil {
			s.log.Error("worker: pipeline OnResolveKeysCompleted failed", "task_id", task.ID, "err", err)
		}
	}

	// Persist extracted data and mark request completed.
	if task.ModuleName == moduleExtract && task.Status == statusCompleted {
		if err := s.pipeline.OnExtractCompleted(ctx, task); err != nil {
			s.log.Error("worker: pipeline OnExtractCompleted failed", "task_id", task.ID, "err", err)
		}
	}

	// Failed resolve_keys / extract → mark the owning extraction_request failed.
	if task.Status == statusFailed && task.ExtractionRequestID.Valid &&
		(task.ModuleName == moduleResolveKeys || task.ModuleName == moduleExtract) {
		msg := "task failed"
		if task.ErrorMessage.Valid {
			msg = task.ErrorMessage.String
		}
		s.pipeline.MarkRequestFailed(ctx, uuid.UUID(task.ExtractionRequestID.Bytes), msg)
	}

	return task, nil
}

// progressPendingRequests advances every pending/running extraction_request on
// the given document. Best-effort: errors per request are logged and the loop
// continues so one failing request does not stall the others.
func (s *WorkerService) progressPendingRequests(ctx context.Context, documentID uuid.UUID) {
	requests, err := s.repo.ListPendingExtractionRequestsByDocument(ctx, documentID)
	if err != nil {
		s.log.Error("worker: list pending extraction_requests",
			"document_id", documentID, "err", err)
		return
	}
	for _, req := range requests {
		if err := s.pipeline.Progress(ctx, req); err != nil {
			s.log.Error("worker: progress extraction_request",
				"request_id", req.ID, "document_id", documentID, "err", err)
		}
	}
}

type convertPayload struct {
	MDStoragePath string `json:"md_storage_path"`
	CharCount     int    `json:"char_count"`
	SectionCount  int    `json:"section_count"`
}

type anonymizePayload struct {
	AnonymizedStoragePath  string `json:"anonymized_storage_path"`
	EntitiesMapStoragePath string `json:"entities_map_storage_path"`
	EntityCount            int    `json:"entity_count"`
}

type convertResultFinal struct {
	MDDocumentID string `json:"md_document_id"`
	CharCount    int    `json:"char_count"`
	SectionCount int    `json:"section_count"`
}

type anonymizeResultFinal struct {
	AnonymizedDocumentID  string `json:"anonymized_document_id"`
	EntitiesMapDocumentID string `json:"entities_map_document_id"`
	EntityCount           int    `json:"entity_count"`
}

func (s *WorkerService) triggerAnonymize(ctx context.Context, convertTask repository.DocumentTask) error {
	var payload convertPayload
	if err := json.Unmarshal(convertTask.ResultPayload, &payload); err != nil {
		return fmt.Errorf("parse convert payload: %w", err)
	}
	if payload.MDStoragePath == "" {
		return fmt.Errorf("md_storage_path is empty in convert result_payload")
	}

	// Detach from the request context so chaining work is not cancelled when
	// the callback request lifecycle ends (worker disconnect, client timeout).
	chainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	// Create the anonymize task. ON CONFLICT on the singleton partial index
	// makes this atomic: if the task already exists pgx returns ErrNoRows.
	anonTask, err := s.repo.CreateDocumentTaskSingleton(chainCtx, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       convertTask.DocumentID,
		ModuleName:       moduleAnonymize,
		InputStoragePath: payload.MDStoragePath,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Task already exists — idempotent skip.
		s.log.Info("worker: anonymize task already exists, skipping", "document_id", convertTask.DocumentID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("create anonymize task: %w", err)
	}

	if err := s.pythonClient.Process(chainCtx, pythonworker.ProcessRequest{
		TaskID:      anonTask.ID.String(),
		DocumentID:  anonTask.DocumentID.String(),
		ModuleName:  moduleAnonymize,
		StoragePath: payload.MDStoragePath,
	}); err != nil {
		// Mark orphaned task as failed so it does not linger in pending forever.
		if markErr := s.markTaskFailed(chainCtx, anonTask.ID, err.Error()); markErr != nil {
			s.log.Error("worker: failed to mark orphaned anonymize task as failed",
				"task_id", anonTask.ID, "err", markErr)
		}
		return fmt.Errorf("trigger anonymize: %w", err)
	}
	return nil
}

// markTaskFailed sets a task's status to "failed" with the provided error message.
func (s *WorkerService) markTaskFailed(ctx context.Context, taskID uuid.UUID, msg string) error {
	_, err := s.repo.UpdateWorkerTaskStatus(ctx, repository.UpdateWorkerTaskStatusParams{
		ID:           taskID,
		Status:       statusFailed,
		ErrorMessage: pgtype.Text{String: msg, Valid: true},
	})
	return err
}

// fileNameFromPath returns the last path component of a storage path.
// Uses path.Base (slash-separated) which correctly handles trailing slashes and
// other edge cases that strings.Split cannot handle.
func fileNameFromPath(storagePath string) string {
	if storagePath == "" {
		return ""
	}
	return path.Base(storagePath)
}

// registerArtifact creates a Document record for a worker-produced artifact file.
// uploaded_by is inherited from the parent document.
// Idempotent: a second call with the same (parent_id, artifact_kind) returns the
// existing artifact document without creating a duplicate.
func (s *WorkerService) registerArtifact(
	ctx context.Context,
	parent repository.Document,
	storagePath string,
	fileName string,
	mimeType string,
	kind string,
) (repository.Document, error) {
	return s.repo.CreateArtifactDocument(ctx, repository.CreateArtifactDocumentParams{
		OrganizationID: parent.OrganizationID,
		SiteID:         parent.SiteID,
		UploadedBy:     parent.UploadedBy,
		ParentID:       pgtype.UUID{Bytes: parent.ID, Valid: true},
		FileName:       fileName,
		StoragePath:    storagePath,
		MimeType:       pgtype.Text{String: mimeType, Valid: mimeType != ""},
		ArtifactKind:   pgtype.Text{String: kind, Valid: true},
	})
}

func (s *WorkerService) registerConvertArtifacts(ctx context.Context, task repository.DocumentTask) error {
	var payload convertPayload
	if err := json.Unmarshal(task.ResultPayload, &payload); err != nil {
		return fmt.Errorf("parse convert payload: %w", err)
	}
	if payload.MDStoragePath == "" {
		return nil // worker did not return a path; skip
	}

	parent, err := s.repo.GetDocumentByID(ctx, task.DocumentID)
	if err != nil {
		return fmt.Errorf("get parent document: %w", err)
	}

	artifact, err := s.registerArtifact(ctx, parent,
		payload.MDStoragePath,
		fileNameFromPath(payload.MDStoragePath),
		"text/markdown",
		"convert_md",
	)
	if err != nil {
		return fmt.Errorf("create convert artifact document: %w", err)
	}

	finalPayload := convertResultFinal{
		MDDocumentID: artifact.ID.String(),
		CharCount:    payload.CharCount,
		SectionCount: payload.SectionCount,
	}
	raw, err := json.Marshal(finalPayload)
	if err != nil {
		return fmt.Errorf("marshal final convert payload: %w", err)
	}
	_, err = s.repo.UpdateTaskResultPayload(ctx, repository.UpdateTaskResultPayloadParams{
		ID:            task.ID,
		ResultPayload: raw,
	})
	return err
}

func (s *WorkerService) registerAnonymizeArtifacts(ctx context.Context, task repository.DocumentTask) error {
	var payload anonymizePayload
	if err := json.Unmarshal(task.ResultPayload, &payload); err != nil {
		return fmt.Errorf("parse anonymize payload: %w", err)
	}

	parent, err := s.repo.GetDocumentByID(ctx, task.DocumentID)
	if err != nil {
		return fmt.Errorf("get parent document: %w", err)
	}

	var anonDocID, entitiesDocID string

	if payload.AnonymizedStoragePath != "" {
		doc, err := s.registerArtifact(ctx, parent,
			payload.AnonymizedStoragePath,
			fileNameFromPath(payload.AnonymizedStoragePath),
			"text/markdown",
			"anonymize_doc",
		)
		if err != nil {
			return fmt.Errorf("create anonymized artifact: %w", err)
		}
		anonDocID = doc.ID.String()
	}

	if payload.EntitiesMapStoragePath != "" {
		doc, err := s.registerArtifact(ctx, parent,
			payload.EntitiesMapStoragePath,
			fileNameFromPath(payload.EntitiesMapStoragePath),
			"application/json",
			"anonymize_entities",
		)
		if err != nil {
			return fmt.Errorf("create entities map artifact: %w", err)
		}
		entitiesDocID = doc.ID.String()
	}

	// No artifacts to register — both paths are empty. Avoid overwriting result_payload
	// with empty IDs (entity_count would be preserved but document IDs would be cleared).
	if anonDocID == "" && entitiesDocID == "" {
		return nil
	}

	finalPayload := anonymizeResultFinal{
		AnonymizedDocumentID:  anonDocID,
		EntitiesMapDocumentID: entitiesDocID,
		EntityCount:           payload.EntityCount,
	}
	raw, err := json.Marshal(finalPayload)
	if err != nil {
		return fmt.Errorf("marshal final anonymize payload: %w", err)
	}
	_, err = s.repo.UpdateTaskResultPayload(ctx, repository.UpdateTaskResultPayloadParams{
		ID:            task.ID,
		ResultPayload: raw,
	})
	return err
}

// runWithArtifactTimeout runs fn in a new context detached from ctx
// (so client disconnect does not cancel it) with a 30 s deadline.
// The cancel function is called immediately when fn returns, preventing context leak.
func runWithArtifactTimeout(
	ctx context.Context,
	task repository.DocumentTask,
	fn func(context.Context, repository.DocumentTask) error,
) error {
	artifactCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return fn(artifactCtx, task)
}

