package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// workerPythonClient is the small publisher surface WorkerService needs from
// the Redis/Celery integration. Keeping it consumer-side makes chaining tests
// independent from Redis and from the concrete pythonworker.Publisher type.
type workerPythonClient interface {
	Process(ctx context.Context, req pythonworker.ProcessRequest) error
}

// Module names used in worker routing, task chaining, and result handling.
const (
	moduleConvert   = "convert"
	moduleAnonymize = "anonymize"
	moduleExtract   = "extract"
)

// Task status constants used for chaining checks, result handling, and orphan cleanup.
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

	// Register anonymize artifacts.
	if task.ModuleName == moduleAnonymize && task.Status == statusCompleted {
		if err := runWithArtifactTimeout(ctx, task, s.registerAnonymizeArtifacts); err != nil {
			s.log.Error("worker: failed to register anonymize artifacts", "task_id", task.ID, "err", err)
		}
	}

	// Extract results are persisted as structured, queryable rows. The callback
	// itself still succeeds even if this best-effort materialization fails; the
	// original JSON stays in document_tasks.result_payload for diagnosis/replay.
	if task.ModuleName == moduleExtract && task.Status == statusCompleted {
		if err := runWithArtifactTimeout(ctx, task, s.saveExtractedData); err != nil {
			s.log.Error("worker: failed to save extracted data", "task_id", task.ID, "err", err)
		}
	}

	return task, nil
}

// convertPayload is the raw result expected from the Python convert worker
// before Go rewrites result_payload to document IDs.
type convertPayload struct {
	MDStoragePath string `json:"md_storage_path"`
	CharCount     int    `json:"char_count"`
	SectionCount  int    `json:"section_count"`
}

// anonymizePayload is the raw result expected from the Python anonymize worker
// before Go registers the produced artifacts as documents.
type anonymizePayload struct {
	AnonymizedStoragePath  string `json:"anonymized_storage_path"`
	EntitiesMapStoragePath string `json:"entities_map_storage_path"`
	EntityCount            int    `json:"entity_count"`
}

// convertResultFinal is the stable payload stored after convert artifacts have
// been registered in the documents table.
type convertResultFinal struct {
	MDDocumentID string `json:"md_document_id"`
	CharCount    int    `json:"char_count"`
	SectionCount int    `json:"section_count"`
}

// anonymizeResultFinal is the stable payload stored after anonymize artifacts
// have been registered in the documents table.
type anonymizeResultFinal struct {
	AnonymizedDocumentID  string `json:"anonymized_document_id"`
	EntitiesMapDocumentID string `json:"entities_map_document_id"`
	EntityCount           int    `json:"entity_count"`
}

// extractedValueEntry is the worker-result shape after normalizing the few JSON
// formats we accept into one key/value/confidence tuple.
type extractedValueEntry struct {
	KeyName    string
	Value      json.RawMessage
	Confidence pgtype.Float8
}

// triggerAnonymize creates and publishes the next task in the convert →
// anonymize chain using the markdown path returned by the convert worker.
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

// registerConvertArtifacts records the markdown artifact produced by convert
// and rewrites the task payload from storage paths to document IDs.
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

// registerAnonymizeArtifacts records anonymized markdown and entity-map
// artifacts produced by anonymize and rewrites the task payload to document IDs.
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

// saveExtractedData materializes a completed extract task's JSON payload into
// document_extracted_data rows keyed by the tenant's extraction_keys.
func (s *WorkerService) saveExtractedData(ctx context.Context, task repository.DocumentTask) error {
	if len(task.ResultPayload) == 0 || string(task.ResultPayload) == "null" {
		return nil
	}

	// document_extracted_data uses composite FKs with organization_id, so resolve
	// the document tenant first and then only match keys from the same tenant.
	orgID, err := s.repo.GetDocumentOrganizationID(ctx, task.DocumentID)
	if err != nil {
		return fmt.Errorf("get document organization: %w", err)
	}

	keys, err := s.repo.ListExtractionKeysByOrganization(ctx, orgID)
	if err != nil {
		return fmt.Errorf("list extraction keys: %w", err)
	}
	keyIDs := make(map[string]uuid.UUID, len(keys))
	for _, key := range keys {
		keyIDs[key.KeyName] = key.ID
	}

	entries, err := parseExtractedValueEntries(task.ResultPayload)
	if err != nil {
		return fmt.Errorf("parse extract payload: %w", err)
	}
	var upsertErrors []string
	for _, entry := range entries {
		keyID, ok := keyIDs[entry.KeyName]
		if !ok || len(entry.Value) == 0 {
			continue
		}
		if _, err := s.repo.UpsertDocumentExtractedData(ctx, repository.UpsertDocumentExtractedDataParams{
			OrganizationID: orgID,
			DocumentID:     task.DocumentID,
			KeyID:          keyID,
			ExtractedValue: entry.Value,
			Confidence:     entry.Confidence,
		}); err != nil {
			s.log.Error("worker: failed to save extracted key",
				"task_id", task.ID,
				"document_id", task.DocumentID,
				"key_name", entry.KeyName,
				"key_id", keyID,
				"err", err,
			)
			upsertErrors = append(upsertErrors, fmt.Sprintf("%s: %v", entry.KeyName, err))
		}
	}
	if len(upsertErrors) > 0 {
		return fmt.Errorf("save extracted data: %s", strings.Join(upsertErrors, "; "))
	}
	return nil
}

// parseExtractedValueEntries accepts the wire formats observed/expected from
// extraction workers:
//   - {"extracted_data":[{"key_name":"...","value":...,"confidence":0.9}]}
//   - [{"key_name":"...","value":...}]
//   - {"key_name":{"value":...,"confidence":0.9}} or {"key_name": ...}
//
// Unknown keys are ignored later by saveExtractedData, which keeps callbacks
// tolerant to model output that contains explanatory metadata.
func parseExtractedValueEntries(raw json.RawMessage) ([]extractedValueEntry, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err == nil {
		for _, field := range []string{"extracted_data", "data", "results"} {
			if nested, ok := envelope[field]; ok {
				return parseExtractedValueEntries(nested)
			}
		}
	}

	var array []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &array); err == nil {
		entries := make([]extractedValueEntry, 0, len(array))
		for _, item := range array {
			keyName := rawString(item["key_name"])
			if keyName == "" {
				keyName = rawString(item["key"])
			}
			value := item["value"]
			if len(value) == 0 {
				value = item["extracted_value"]
			}
			if keyName == "" || len(value) == 0 {
				continue
			}
			entries = append(entries, extractedValueEntry{
				KeyName:    keyName,
				Value:      value,
				Confidence: rawConfidence(item["confidence"]),
			})
		}
		return entries, nil
	}

	if envelope == nil {
		return nil, fmt.Errorf("payload must be a JSON object or array")
	}
	entries := make([]extractedValueEntry, 0, len(envelope))
	for keyName, value := range envelope {
		if keyName == "" || isExtractEnvelopeField(keyName) {
			continue
		}
		var objectValue map[string]json.RawMessage
		if err := json.Unmarshal(value, &objectValue); err == nil {
			if nestedValue, ok := objectValue["value"]; ok {
				entries = append(entries, extractedValueEntry{
					KeyName:    keyName,
					Value:      nestedValue,
					Confidence: rawConfidence(objectValue["confidence"]),
				})
				continue
			}
			if nestedValue, ok := objectValue["extracted_value"]; ok {
				entries = append(entries, extractedValueEntry{
					KeyName:    keyName,
					Value:      nestedValue,
					Confidence: rawConfidence(objectValue["confidence"]),
				})
				continue
			}
		}
		entries = append(entries, extractedValueEntry{KeyName: keyName, Value: value})
	}
	return entries, nil
}

// isExtractEnvelopeField identifies non-data top-level fields that may appear
// beside model output and should not be treated as extraction key names.
func isExtractEnvelopeField(field string) bool {
	switch field {
	case "metadata", "usage", "model", "errors":
		return true
	default:
		return false
	}
}

// rawString decodes a JSON string field and returns empty string on absence or
// type mismatch.
func rawString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// rawConfidence decodes an optional numeric confidence into pgtype.Float8.
func rawConfidence(raw json.RawMessage) pgtype.Float8 {
	var f float64
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: f, Valid: true}
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
