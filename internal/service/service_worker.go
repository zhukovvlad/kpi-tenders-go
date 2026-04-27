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

	// Chain: resolve_keys completed → insert new keys + trigger extract.
	if task.ModuleName == moduleResolveKeys && task.Status == statusCompleted {
		if err := s.triggerExtract(ctx, task); err != nil {
			s.log.Error("worker: failed to trigger extract from resolve_keys", "task_id", task.ID, "err", err)
		}
	}

	// Persist extracted data when extract completes.
	if task.ModuleName == moduleExtract && task.Status == statusCompleted {
		if err := runWithArtifactTimeout(ctx, task, s.handleExtractCompleted); err != nil {
			s.log.Error("worker: failed to persist extracted data", "task_id", task.ID, "err", err)
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

// ── resolve_keys → extract chain ─────────────────────────────────────────────

// resolveKeysPayload is the result_payload structure returned by the Python
// resolve_keys worker after matching user questions to extraction keys.
type resolveKeysPayload struct {
	// NewKeys are keys the LLM identified as new (not in existing_keys).
	// Go must INSERT them into extraction_keys before chaining.
	NewKeys []resolveKeysNewKey `json:"new_keys"`
	// ResolvedSchema is the full list of keys to extract, covering both new
	// and existing keys that matched the user's questions.
	ResolvedSchema []extractionSchemaEntry `json:"resolved_schema"`
	// MDDocumentID is the UUID of the markdown artifact document in the DB.
	// The extract worker reads this file from MinIO.
	MDDocumentID string `json:"md_document_id"`
}

type resolveKeysNewKey struct {
	KeyName     string `json:"key_name"`
	SourceQuery string `json:"source_query"`
	DataType    string `json:"data_type"`
}

type extractionSchemaEntry struct {
	KeyName  string `json:"key_name"`
	DataType string `json:"data_type"`
}

// triggerExtract processes a completed resolve_keys task:
//  1. Inserts new extraction keys returned by Python.
//  2. Looks up the MD artifact document to get its storage_path.
//  3. Creates an extract task (idempotent via ON CONFLICT DO NOTHING).
//  4. Enqueues the extract Celery task with extraction_schema and md_document_id.
func (s *WorkerService) triggerExtract(ctx context.Context, resolveTask repository.DocumentTask) error {
	var payload resolveKeysPayload
	if err := json.Unmarshal(resolveTask.ResultPayload, &payload); err != nil {
		return fmt.Errorf("parse resolve_keys payload: %w", err)
	}
	if payload.MDDocumentID == "" {
		return fmt.Errorf("md_document_id is empty in resolve_keys result_payload")
	}
	if len(payload.ResolvedSchema) == 0 {
		return fmt.Errorf("resolved_schema is empty in resolve_keys result_payload")
	}

	// Detach from the request context so chaining survives worker disconnect.
	chainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	// Fetch the document to get the tenant's organization_id.
	doc, err := s.repo.GetDocumentByID(chainCtx, resolveTask.DocumentID)
	if err != nil {
		return fmt.Errorf("get document for resolve_keys chain: %w", err)
	}
	orgUUID := pgtype.UUID{Bytes: doc.OrganizationID, Valid: true}

	// Insert new keys returned by Python. Best-effort per key: a single
	// failure does not abort the whole chain.
	for _, k := range payload.NewKeys {
		if _, err := s.repo.UpsertExtractionKey(chainCtx, repository.UpsertExtractionKeyParams{
			OrganizationID: orgUUID,
			KeyName:        k.KeyName,
			SourceQuery:    k.SourceQuery,
			DataType:       k.DataType,
		}); err != nil {
			s.log.Error("worker: failed to upsert extraction key",
				"key_name", k.KeyName, "document_id", resolveTask.DocumentID, "err", err)
		}
	}

	// Get the MD document's storage_path for the extract task's input_storage_path.
	mdDocID, err := uuid.Parse(payload.MDDocumentID)
	if err != nil {
		return fmt.Errorf("parse md_document_id %q: %w", payload.MDDocumentID, err)
	}
	mdDoc, err := s.repo.GetDocumentByID(chainCtx, mdDocID)
	if err != nil {
		return fmt.Errorf("get md document %s: %w", payload.MDDocumentID, err)
	}
	if mdDoc.OrganizationID != doc.OrganizationID {
		return fmt.Errorf("md document %s does not belong to the organization of document %s",
			payload.MDDocumentID, resolveTask.DocumentID)
	}

	// Create the extract task. ON CONFLICT DO NOTHING → pgx.ErrNoRows means
	// the task already exists (idempotent retry of the resolve_keys callback).
	extractTask, err := s.repo.CreateDocumentTaskInternal(chainCtx, repository.CreateDocumentTaskInternalParams{
		DocumentID:       resolveTask.DocumentID,
		ModuleName:       moduleExtract,
		InputStoragePath: mdDoc.StoragePath,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.log.Info("worker: extract task already exists, skipping",
			"document_id", resolveTask.DocumentID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("create extract task: %w", err)
	}

	if err := s.pythonClient.Process(chainCtx, pythonworker.ProcessRequest{
		TaskID:      extractTask.ID.String(),
		DocumentID:  extractTask.DocumentID.String(),
		ModuleName:  moduleExtract,
		StoragePath: mdDoc.StoragePath,
		Kwargs: map[string]any{
			"extraction_schema": payload.ResolvedSchema,
			"md_document_id":    payload.MDDocumentID,
		},
	}); err != nil {
		if markErr := s.markTaskFailed(chainCtx, extractTask.ID, err.Error()); markErr != nil {
			s.log.Error("worker: failed to mark orphaned extract task as failed",
				"task_id", extractTask.ID, "err", markErr)
		}
		return fmt.Errorf("trigger extract: %w", err)
	}
	return nil
}

// ── extract callback handler ──────────────────────────────────────────────────

// handleExtractCompleted persists the data extracted by the Python extract
// worker into document_extracted_data. The result_payload is expected to be a
// flat JSON object mapping key_name → extracted_value (string).
func (s *WorkerService) handleExtractCompleted(ctx context.Context, task repository.DocumentTask) error {
	// Parse extract result_payload: {"key_name": "value", ...}
	var extractedData map[string]string
	if err := json.Unmarshal(task.ResultPayload, &extractedData); err != nil {
		return fmt.Errorf("parse extract payload: %w", err)
	}
	if len(extractedData) == 0 {
		return nil // nothing to persist
	}

	// Get the document to determine organization_id for tenant scoping.
	doc, err := s.repo.GetDocumentByID(ctx, task.DocumentID)
	if err != nil {
		return fmt.Errorf("get document for extract: %w", err)
	}

	// Collect the key names present in the payload.
	keyNames := make([]string, 0, len(extractedData))
	for name := range extractedData {
		keyNames = append(keyNames, name)
	}

	// Look up extraction keys by name for this tenant (includes system keys).
	keys, err := s.repo.GetExtractionKeysByNames(ctx, repository.GetExtractionKeysByNamesParams{
		KeyNames:       keyNames,
		OrganizationID: doc.OrganizationID,
	})
	if err != nil {
		return fmt.Errorf("get extraction keys by names: %w", err)
	}

	// Build name → ID index for fast lookup.
	keyIDByName := make(map[string]uuid.UUID, len(keys))
	for _, k := range keys {
		keyIDByName[k.KeyName] = k.ID
	}

	// Upsert each extracted value. Log-and-continue on individual failures so
	// a single bad row does not abort the whole batch.
	for keyName, value := range extractedData {
		keyID, ok := keyIDByName[keyName]
		if !ok {
			s.log.Warn("worker: extract returned unknown key name, skipping",
				"key_name", keyName, "document_id", task.DocumentID)
			continue
		}
		if err := s.repo.UpsertExtractedDatum(ctx, repository.UpsertExtractedDatumParams{
			OrganizationID: doc.OrganizationID,
			DocumentID:     task.DocumentID,
			KeyID:          keyID,
			ExtractedValue: pgtype.Text{String: value, Valid: true},
		}); err != nil {
			s.log.Error("worker: failed to upsert extracted datum",
				"key_name", keyName, "document_id", task.DocumentID, "err", err)
		}
	}

	return nil
}
