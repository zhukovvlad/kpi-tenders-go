package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// Artifact kinds produced by the worker pipeline. Mirrored in
// 000001_init_schema.up.sql column comment on documents.artifact_kind and in
// service_worker.go.
const (
	artifactKindConvertMD     = "convert_md"
	artifactKindAnonymizeDoc  = "anonymize_doc"
	artifactKindAnonymizeMap  = "anonymize_entities"
)

// extraction_request status values. Mirrors the CHECK constraint in 000003.
const (
	requestStatusPending   = "pending"
	requestStatusRunning   = "running"
	requestStatusCompleted = "completed"
	requestStatusFailed    = "failed"
)

// pipelineTimeout bounds best-effort pipeline work detached from the request
// context (Redis publish, prerequisite-task creation, etc.).
const pipelineTimeout = 30 * time.Second

// ExtractionService owns the lifecycle of extraction_requests:
//   - Initiate — create a request and start its pipeline.
//   - Progress — decide what module to enqueue next given the document's
//     current artifact state.
//   - OnResolveKeysCompleted — store resolved_schema, upsert new keys,
//     enqueue extract.
//   - OnExtractCompleted — persist extracted data, mark request completed.
//
// WorkerService delegates to these methods from worker callbacks.
type ExtractionService struct {
	repo         repository.Querier
	pythonClient workerPythonClient
	log          *slog.Logger
}

// NewExtractionService creates a new ExtractionService. pythonClient may be
// nil in unit tests that exercise creation but not enqueue side effects;
// production callers must provide a real client.
func NewExtractionService(repo repository.Querier, pythonClient workerPythonClient, log *slog.Logger) *ExtractionService {
	return &ExtractionService{repo: repo, pythonClient: pythonClient, log: log}
}

// existingKeyEntry is the payload sent to Python's resolve_keys so the LLM can
// match user questions to already-defined keys.
type existingKeyEntry struct {
	KeyName     string `json:"key_name"`
	SourceQuery string `json:"source_query"`
	DataType    string `json:"data_type"`
}

// resolveKeysPayload is the result_payload returned by Python's resolve_keys
// task. md_document_id is intentionally NOT part of the contract — Go finds
// the target artifact itself based on extraction_request.anonymize.
type resolveKeysPayload struct {
	NewKeys        []resolveKeysNewKey     `json:"new_keys"`
	ResolvedSchema []extractionSchemaEntry `json:"resolved_schema"`
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

// Initiate creates a new extraction_request and immediately progresses it.
// Returns the persisted request so the caller can return its id to the client.
func (s *ExtractionService) Initiate(
	ctx context.Context,
	documentID, orgID uuid.UUID,
	questions []string,
	anonymize bool,
) (repository.ExtractionRequest, error) {
	if len(questions) == 0 {
		return repository.ExtractionRequest{}, errs.New(errs.CodeValidationFailed, "questions must not be empty", nil)
	}

	// Validate ownership: GetDocument enforces (id, organization_id).
	if _, err := s.repo.GetDocument(ctx, repository.GetDocumentParams{
		ID:             documentID,
		OrganizationID: orgID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ExtractionRequest{}, errs.New(errs.CodeNotFound, "document not found", err)
		}
		return repository.ExtractionRequest{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}

	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		return repository.ExtractionRequest{}, errs.New(errs.CodeInternalError, "marshal questions", err)
	}

	req, err := s.repo.CreateExtractionRequest(ctx, repository.CreateExtractionRequestParams{
		DocumentID:     documentID,
		OrganizationID: orgID,
		Questions:      questionsJSON,
		Anonymize:      anonymize,
	})
	if err != nil {
		return repository.ExtractionRequest{}, errs.New(errs.CodeInternalError, "failed to create extraction request", err)
	}

	// Best-effort progress: client disconnect or pipeline error must not
	// invalidate the request — the next callback will progress it.
	progressCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pipelineTimeout)
	defer cancel()

	if err := s.Progress(progressCtx, req); err != nil {
		s.log.Error("extraction: failed to progress request",
			"request_id", req.ID, "document_id", documentID, "err", err)
	}

	return req, nil
}

// GetRequest returns an extraction_request scoped to the calling tenant.
// Maps pgx.ErrNoRows to errs.CodeNotFound.
func (s *ExtractionService) GetRequest(ctx context.Context, id, orgID uuid.UUID) (repository.ExtractionRequest, error) {
	req, err := s.repo.GetExtractionRequest(ctx, repository.GetExtractionRequestParams{
		ID:             id,
		OrganizationID: orgID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.ExtractionRequest{}, errs.New(errs.CodeNotFound, "extraction request not found", err)
		}
		return repository.ExtractionRequest{}, errs.New(errs.CodeInternalError, "internal server error", err)
	}
	return req, nil
}

// ExtractionAnswer is a single key→value pair returned to the client.
type ExtractionAnswer struct {
	KeyName        string  `json:"key_name"`
	DataType       string  `json:"data_type"`
	ExtractedValue *string `json:"extracted_value"`
}

// GetAnswers returns extracted values for the keys listed in req.resolved_schema.
// Returns an empty slice when the request has not progressed past resolve_keys
// or when no values have been extracted yet.
func (s *ExtractionService) GetAnswers(ctx context.Context, req repository.ExtractionRequest) ([]ExtractionAnswer, error) {
	if len(req.ResolvedSchema) == 0 {
		return []ExtractionAnswer{}, nil
	}
	var schema []extractionSchemaEntry
	if err := json.Unmarshal(req.ResolvedSchema, &schema); err != nil {
		return nil, errs.New(errs.CodeInternalError, "parse resolved_schema", err)
	}
	if len(schema) == 0 {
		return []ExtractionAnswer{}, nil
	}
	keyNames := make([]string, len(schema))
	for i, e := range schema {
		keyNames[i] = e.KeyName
	}
	rows, err := s.repo.ListExtractedDataForKeys(ctx, repository.ListExtractedDataForKeysParams{
		DocumentID:     req.DocumentID,
		OrganizationID: req.OrganizationID,
		KeyNames:       keyNames,
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "list extracted data", err)
	}
	out := make([]ExtractionAnswer, 0, len(rows))
	for _, r := range rows {
		var val *string
		if r.ExtractedValue.Valid {
			v := r.ExtractedValue.String
			val = &v
		}
		out = append(out, ExtractionAnswer{
			KeyName:        r.KeyName,
			DataType:       r.DataType,
			ExtractedValue: val,
		})
	}
	return out, nil
}

// Progress decides what to do next for a request, based on document artifacts:
//   - If the target artifact (anonymize_doc when anonymize=true, convert_md
//     otherwise) exists, enqueue resolve_keys.
//   - Otherwise, ensure the prerequisite singleton task (convert or anonymize)
//     is created. The convert→anonymize chain in WorkerService takes it from
//     there; once the prereq completes, WorkerService calls Progress again.
//
// The function is idempotent: if the per-request resolve_keys task already
// exists, no duplicate is enqueued.
func (s *ExtractionService) Progress(ctx context.Context, req repository.ExtractionRequest) error {
	if req.Status == requestStatusCompleted || req.Status == requestStatusFailed {
		return nil
	}

	doc, err := s.repo.GetDocumentByID(ctx, req.DocumentID)
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}

	artifacts, err := s.repo.ListDocumentsByParent(ctx, repository.ListDocumentsByParentParams{
		ParentID:       pgtype.UUID{Bytes: doc.ID, Valid: true},
		OrganizationID: doc.OrganizationID,
	})
	if err != nil {
		return fmt.Errorf("list artifacts: %w", err)
	}

	mdArtifact := findArtifact(artifacts, artifactKindConvertMD)
	anonArtifact := findArtifact(artifacts, artifactKindAnonymizeDoc)

	// Determine the target artifact for extract.
	var target *repository.Document
	if req.Anonymize {
		target = anonArtifact
	} else {
		target = mdArtifact
	}

	if target != nil {
		return s.enqueueResolveKeys(ctx, req, doc, target)
	}

	// Prerequisite missing. Ensure the right singleton task exists.
	switch {
	case req.Anonymize && mdArtifact != nil:
		// MD ready, anonymize artifact missing — ensure anonymize task exists.
		// WorkerService chains convert→anonymize automatically, but an existing
		// MD with no anonymize artifact means anonymize hasn't been triggered
		// yet (e.g. the document was uploaded ready-converted). Trigger it.
		return s.ensureAnonymizeTask(ctx, doc, mdArtifact)
	default:
		// No MD yet — kick off convert. anonymize=true case will be handled
		// by the existing convert→anonymize chain in WorkerService.
		return s.ensureConvertTask(ctx, doc)
	}
}

// OnResolveKeysCompleted is invoked by WorkerService after a resolve_keys task
// finishes successfully. It upserts new keys, persists resolved_schema on the
// request, and enqueues the extract task.
func (s *ExtractionService) OnResolveKeysCompleted(ctx context.Context, task repository.DocumentTask) error {
	if !task.ExtractionRequestID.Valid {
		return fmt.Errorf("resolve_keys task %s has no extraction_request_id", task.ID)
	}
	requestID := uuid.UUID(task.ExtractionRequestID.Bytes)

	var payload resolveKeysPayload
	if err := json.Unmarshal(task.ResultPayload, &payload); err != nil {
		return fmt.Errorf("parse resolve_keys payload: %w", err)
	}
	if len(payload.ResolvedSchema) == 0 {
		return fmt.Errorf("resolved_schema is empty in resolve_keys result_payload")
	}

	chainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pipelineTimeout)
	defer cancel()

	req, err := s.repo.GetExtractionRequestByID(chainCtx, requestID)
	if err != nil {
		return fmt.Errorf("get extraction request: %w", err)
	}

	// Upsert new keys returned by Python. Best-effort per key.
	orgUUID := pgtype.UUID{Bytes: req.OrganizationID, Valid: true}
	for _, k := range payload.NewKeys {
		if _, err := s.repo.UpsertExtractionKey(chainCtx, repository.UpsertExtractionKeyParams{
			OrganizationID: orgUUID,
			KeyName:        k.KeyName,
			SourceQuery:    k.SourceQuery,
			DataType:       k.DataType,
		}); err != nil {
			s.log.Error("extraction: failed to upsert key",
				"key_name", k.KeyName, "request_id", requestID, "err", err)
		}
	}

	// Persist resolved_schema for the GET endpoint.
	schemaJSON, err := json.Marshal(payload.ResolvedSchema)
	if err != nil {
		return fmt.Errorf("marshal resolved_schema: %w", err)
	}
	if _, err := s.repo.SetExtractionRequestResolvedSchema(chainCtx, repository.SetExtractionRequestResolvedSchemaParams{
		ID:             requestID,
		ResolvedSchema: schemaJSON,
	}); err != nil {
		return fmt.Errorf("save resolved_schema: %w", err)
	}

	// Find the target artifact for extract.
	doc, err := s.repo.GetDocumentByID(chainCtx, req.DocumentID)
	if err != nil {
		return fmt.Errorf("get document: %w", err)
	}
	artifacts, err := s.repo.ListDocumentsByParent(chainCtx, repository.ListDocumentsByParentParams{
		ParentID:       pgtype.UUID{Bytes: doc.ID, Valid: true},
		OrganizationID: doc.OrganizationID,
	})
	if err != nil {
		return fmt.Errorf("list artifacts: %w", err)
	}
	var target *repository.Document
	if req.Anonymize {
		target = findArtifact(artifacts, artifactKindAnonymizeDoc)
	} else {
		target = findArtifact(artifacts, artifactKindConvertMD)
	}
	if target == nil {
		// Race: anonymize artifact was registered after Progress fired
		// resolve_keys but disappeared before this callback. Should not happen
		// in production, but fail loud rather than silently picking a wrong one.
		return fmt.Errorf("target artifact missing for extraction_request %s (anonymize=%v)",
			requestID, req.Anonymize)
	}

	return s.enqueueExtract(chainCtx, req, target, payload.ResolvedSchema)
}

// OnExtractCompleted is invoked by WorkerService after an extract task
// finishes. It persists the extracted values into document_extracted_data and
// marks the extraction_request as completed.
func (s *ExtractionService) OnExtractCompleted(ctx context.Context, task repository.DocumentTask) error {
	if !task.ExtractionRequestID.Valid {
		return fmt.Errorf("extract task %s has no extraction_request_id", task.ID)
	}
	requestID := uuid.UUID(task.ExtractionRequestID.Bytes)

	var extractedData map[string]*string
	if err := json.Unmarshal(task.ResultPayload, &extractedData); err != nil {
		return fmt.Errorf("parse extract payload: %w", err)
	}

	req, err := s.repo.GetExtractionRequestByID(ctx, requestID)
	if err != nil {
		return fmt.Errorf("get extraction request: %w", err)
	}

	if len(extractedData) > 0 {
		if err := s.persistExtractedData(ctx, req, extractedData); err != nil {
			return fmt.Errorf("persist extracted data: %w", err)
		}
	}

	if _, err := s.repo.SetExtractionRequestStatus(ctx, repository.SetExtractionRequestStatusParams{
		ID:     requestID,
		Status: requestStatusCompleted,
	}); err != nil {
		return fmt.Errorf("mark request completed: %w", err)
	}
	return nil
}

// MarkRequestFailed sets the request status to failed with an error message.
// Best-effort: never returns an error to the caller (logged instead) so a
// failed task callback always finishes its persistence path.
func (s *ExtractionService) MarkRequestFailed(ctx context.Context, requestID uuid.UUID, msg string) {
	_, err := s.repo.SetExtractionRequestStatus(ctx, repository.SetExtractionRequestStatusParams{
		ID:           requestID,
		Status:       requestStatusFailed,
		ErrorMessage: pgtype.Text{String: msg, Valid: true},
	})
	if err != nil {
		s.log.Error("extraction: failed to mark request failed",
			"request_id", requestID, "err", err)
	}
}

// persistExtractedData maps key_name → key_id for the request's tenant and
// upserts non-null values into document_extracted_data in a single batch.
func (s *ExtractionService) persistExtractedData(
	ctx context.Context,
	req repository.ExtractionRequest,
	extractedData map[string]*string,
) error {
	keyNames := make([]string, 0, len(extractedData))
	for name := range extractedData {
		keyNames = append(keyNames, name)
	}
	keys, err := s.repo.GetExtractionKeysByNames(ctx, repository.GetExtractionKeysByNamesParams{
		KeyNames:       keyNames,
		OrganizationID: req.OrganizationID,
	})
	if err != nil {
		return fmt.Errorf("get keys by names: %w", err)
	}
	keyIDByName := make(map[string]uuid.UUID, len(keys))
	for _, k := range keys {
		keyIDByName[k.KeyName] = k.ID
	}

	keyIDs := make([]uuid.UUID, 0, len(extractedData))
	values := make([]string, 0, len(extractedData))
	for name, v := range extractedData {
		if v == nil {
			continue
		}
		id, ok := keyIDByName[name]
		if !ok {
			s.log.Warn("extraction: extract returned unknown key, skipping",
				"key_name", name, "request_id", req.ID)
			continue
		}
		keyIDs = append(keyIDs, id)
		values = append(values, *v)
	}
	if len(keyIDs) == 0 {
		return nil
	}
	return s.repo.BatchUpsertExtractedData(ctx, repository.BatchUpsertExtractedDataParams{
		OrganizationID:  req.OrganizationID,
		DocumentID:      req.DocumentID,
		KeyIds:          keyIDs,
		ExtractedValues: values,
	})
}

// enqueueResolveKeys creates the resolve_keys document_task bound to the
// request and publishes the Celery message. Idempotent: a duplicate request
// (per-request UNIQUE) returns pgx.ErrNoRows, which is logged and ignored.
func (s *ExtractionService) enqueueResolveKeys(
	ctx context.Context,
	req repository.ExtractionRequest,
	doc repository.Document,
	target *repository.Document,
) error {
	rawKeys, err := s.repo.ListExtractionKeysByOrg(ctx, doc.OrganizationID)
	if err != nil {
		return fmt.Errorf("list extraction keys: %w", err)
	}
	existing := make([]existingKeyEntry, len(rawKeys))
	for i, k := range rawKeys {
		existing[i] = existingKeyEntry{
			KeyName:     k.KeyName,
			SourceQuery: k.SourceQuery,
			DataType:    k.DataType,
		}
	}

	var questions []string
	if err := json.Unmarshal(req.Questions, &questions); err != nil {
		return fmt.Errorf("parse questions: %w", err)
	}

	// resolve_keys does not download a file, but document_tasks.input_storage_path
	// is NOT NULL. Use the target artifact's path as the canonical reference.
	task, err := s.repo.CreateDocumentTaskForRequest(ctx, repository.CreateDocumentTaskForRequestParams{
		DocumentID:          req.DocumentID,
		ModuleName:          moduleResolveKeys,
		InputStoragePath:    target.StoragePath,
		ExtractionRequestID: pgtype.UUID{Bytes: req.ID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// resolve_keys already exists for this request — nothing to do.
		return nil
	}
	if err != nil {
		return fmt.Errorf("create resolve_keys task: %w", err)
	}

	// Transition request to running on first enqueue.
	if req.Status == requestStatusPending {
		if _, err := s.repo.SetExtractionRequestStatus(ctx, repository.SetExtractionRequestStatusParams{
			ID:     req.ID,
			Status: requestStatusRunning,
		}); err != nil {
			s.log.Error("extraction: failed to set request running",
				"request_id", req.ID, "err", err)
		}
	}

	if s.pythonClient == nil {
		return nil
	}
	if err := s.pythonClient.Process(ctx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  moduleResolveKeys,
		StoragePath: target.StoragePath,
		Kwargs: map[string]any{
			"raw_questions": questions,
			"existing_keys": existing,
		},
	}); err != nil {
		s.markTaskFailed(ctx, task.ID, err.Error())
		return fmt.Errorf("publish resolve_keys: %w", err)
	}
	return nil
}

// enqueueExtract creates the extract document_task bound to the request and
// publishes the Celery message. Idempotent via per-request UNIQUE.
func (s *ExtractionService) enqueueExtract(
	ctx context.Context,
	req repository.ExtractionRequest,
	target *repository.Document,
	schema []extractionSchemaEntry,
) error {
	task, err := s.repo.CreateDocumentTaskForRequest(ctx, repository.CreateDocumentTaskForRequestParams{
		DocumentID:          req.DocumentID,
		ModuleName:          moduleExtract,
		InputStoragePath:    target.StoragePath,
		ExtractionRequestID: pgtype.UUID{Bytes: req.ID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create extract task: %w", err)
	}

	if s.pythonClient == nil {
		return nil
	}
	if err := s.pythonClient.Process(ctx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  moduleExtract,
		StoragePath: target.StoragePath,
		Kwargs: map[string]any{
			"extraction_schema": schema,
		},
	}); err != nil {
		s.markTaskFailed(ctx, task.ID, err.Error())
		return fmt.Errorf("publish extract: %w", err)
	}
	return nil
}

// ensureConvertTask creates the singleton convert task for a document and
// publishes the Celery message. Idempotent: a duplicate returns pgx.ErrNoRows
// which is treated as success (task already in flight).
func (s *ExtractionService) ensureConvertTask(ctx context.Context, doc repository.Document) error {
	task, err := s.repo.CreateDocumentTaskSingleton(ctx, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       doc.ID,
		ModuleName:       moduleConvert,
		InputStoragePath: doc.StoragePath,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Already exists — Progress will be retried after it completes.
		return nil
	}
	if err != nil {
		return fmt.Errorf("create convert task: %w", err)
	}
	if s.pythonClient == nil {
		return nil
	}
	if err := s.pythonClient.Process(ctx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  moduleConvert,
		StoragePath: doc.StoragePath,
	}); err != nil {
		s.markTaskFailed(ctx, task.ID, err.Error())
		return fmt.Errorf("publish convert: %w", err)
	}
	return nil
}

// ensureAnonymizeTask creates the singleton anonymize task using the markdown
// artifact as input. Same idempotency semantics as ensureConvertTask.
func (s *ExtractionService) ensureAnonymizeTask(
	ctx context.Context,
	doc repository.Document,
	md *repository.Document,
) error {
	task, err := s.repo.CreateDocumentTaskSingleton(ctx, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       doc.ID,
		ModuleName:       moduleAnonymize,
		InputStoragePath: md.StoragePath,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create anonymize task: %w", err)
	}
	if s.pythonClient == nil {
		return nil
	}
	if err := s.pythonClient.Process(ctx, pythonworker.ProcessRequest{
		TaskID:      task.ID.String(),
		DocumentID:  task.DocumentID.String(),
		ModuleName:  moduleAnonymize,
		StoragePath: md.StoragePath,
	}); err != nil {
		s.markTaskFailed(ctx, task.ID, err.Error())
		return fmt.Errorf("publish anonymize: %w", err)
	}
	return nil
}

// markTaskFailed flips a document_task to failed. Best-effort: errors are
// logged because the only caller is already on an error path.
func (s *ExtractionService) markTaskFailed(ctx context.Context, taskID uuid.UUID, msg string) {
	_, err := s.repo.UpdateWorkerTaskStatus(ctx, repository.UpdateWorkerTaskStatusParams{
		ID:           taskID,
		Status:       statusFailed,
		ErrorMessage: pgtype.Text{String: msg, Valid: true},
	})
	if err != nil {
		s.log.Error("extraction: failed to mark task failed",
			"task_id", taskID, "err", err)
	}
}

// findArtifact returns the first document with the given artifact_kind, or
// nil if no match. Returns a pointer into the slice to avoid copying.
func findArtifact(docs []repository.Document, kind string) *repository.Document {
	for i := range docs {
		if docs[i].ArtifactKind.Valid && docs[i].ArtifactKind.String == kind {
			return &docs[i]
		}
	}
	return nil
}
