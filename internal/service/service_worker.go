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

// Module names used in task chaining.
const (
	moduleConvert   = "convert"
	moduleAnonymize = "anonymize"
)

// Task status constants used for chaining checks and orphan cleanup.
const (
	statusCompleted = "completed"
	statusFailed    = "failed"
)

// WorkerService handles callbacks from the Python worker and implements task
// chaining (e.g. convert → anonymize).
type WorkerService struct {
	repo         repository.Querier
	pythonClient workerPythonClient
	log          *slog.Logger
}

// NewWorkerService creates a new WorkerService.
// Panics if pythonClient is nil — it is a required dependency.
func NewWorkerService(repo repository.Querier, pythonClient workerPythonClient, log *slog.Logger) *WorkerService {
	if pythonClient == nil {
		panic("WorkerService: pythonClient is required")
	}
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

	// Chain: convert completed → trigger anonymize, then register artifact.
	// triggerAnonymize must run first: it reads md_storage_path from the original
	// result_payload, which registerConvertArtifacts will overwrite in the DB.
	// If triggering anonymize fails, skip artifact registration so that
	// md_storage_path remains in result_payload and the chain can be retried.
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
	}

	// Регистрация артефактов anonymize
	if task.ModuleName == moduleAnonymize && task.Status == statusCompleted {
		if err := runWithArtifactTimeout(ctx, task, s.registerAnonymizeArtifacts); err != nil {
			s.log.Error("worker: failed to register anonymize artifacts", "task_id", task.ID, "err", err)
		}
	}

	return task, nil
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

	// Create the anonymize task. ON CONFLICT (document_id, module_name) DO NOTHING
	// makes this atomic: if the task already exists pgx returns ErrNoRows.
	anonTask, err := s.repo.CreateDocumentTaskInternal(chainCtx, repository.CreateDocumentTaskInternalParams{
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
		return nil // воркер не вернул путь — пропустить
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
