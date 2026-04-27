package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
)

// ── mock pythonClient ────────────────────────────────────────────────────────

type mockPythonClient struct {
	mock.Mock
}

func (m *mockPythonClient) Process(ctx context.Context, req pythonworker.ProcessRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestWorkerService(ms *storemock.MockStore, pc *mockPythonClient) *WorkerService {
	return NewWorkerService(ms, pc, newTestLogger())
}

func makeDocumentTask(id, docID uuid.UUID, module, status string, payload json.RawMessage) repository.DocumentTask {
	return repository.DocumentTask{
		ID:            id,
		DocumentID:    docID,
		ModuleName:    module,
		Status:        status,
		ResultPayload: payload,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

func convertPayloadJSON(mdPath string) json.RawMessage {
	b, err := json.Marshal(map[string]any{
		"md_storage_path": mdPath,
		"char_count":      42,
		"section_count":   3,
	})
	if err != nil {
		panic("convertPayloadJSON: failed to marshal payload: " + err.Error())
	}
	return b
}

// ── tests ─────────────────────────────────────────────────────────────────────

// 1. status=processing — only update, no chaining.
func TestWorkerService_HandleStatusUpdate_Processing(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	returnedTask := makeDocumentTask(taskID, docID, "convert", "processing", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == taskID && p.Status == "processing"
	})).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{Status: "processing"})

	require.NoError(t, err)
	assert.Equal(t, "processing", task.Status)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 2. status=completed, module=convert — update + trigger anonymize + register artifact.
func TestWorkerService_HandleStatusUpdate_ConvertCompleted_TriggersAnonymize(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	anonTaskID := uuid.New()
	artifactID := uuid.New()
	mdPath := "tenders/docs/test.md"

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	anonTask := makeDocumentTask(anonTaskID, docID, "anonymize", "pending", nil)
	parentDoc := repository.Document{ID: docID, OrganizationID: uuid.New()}
	artifactDoc := repository.Document{ID: artifactID}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("CreateDocumentTaskInternal", mock.Anything, repository.CreateDocumentTaskInternalParams{
		DocumentID:       docID,
		ModuleName:       "anonymize",
		InputStoragePath: mdPath,
	}).Return(anonTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(parentDoc, nil)
	ms.On("CreateArtifactDocument", mock.Anything, mock.Anything).Return(artifactDoc, nil)
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.MatchedBy(func(p repository.UpdateTaskResultPayloadParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)

	pc.On("Process", mock.Anything, pythonworker.ProcessRequest{
		TaskID:      anonTaskID.String(),
		DocumentID:  docID.String(),
		ModuleName:  "anonymize",
		StoragePath: mdPath,
	}).Return(nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)
	assert.Equal(t, "convert", task.ModuleName)
	ms.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// 3. status=completed, module=anonymize — only update, no chaining.
func TestWorkerService_HandleStatusUpdate_AnonymizeCompleted_NoChaining(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	returnedTask := makeDocumentTask(taskID, docID, "anonymize", "completed", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{Status: "completed"})

	require.NoError(t, err)
	assert.Equal(t, "anonymize", task.ModuleName)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 4. status=failed — only update.
func TestWorkerService_HandleStatusUpdate_Failed(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	errMsg := "something went wrong"
	returnedTask := makeDocumentTask(taskID, docID, "convert", "failed", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == taskID && p.Status == "failed" && p.ErrorMessage.Valid && p.ErrorMessage.String == errMsg
	})).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:       "failed",
		ErrorMessage: &errMsg,
	})

	require.NoError(t, err)
	assert.Equal(t, "failed", task.Status)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 5. pythonClient.Process fails during chaining — no error returned (only logged).
func TestWorkerService_HandleStatusUpdate_PythonClientError_NoErrorPropagated(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	anonTaskID := uuid.New()
	mdPath := "tenders/docs/test.md"

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	anonTask := makeDocumentTask(anonTaskID, docID, "anonymize", "pending", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)
	ms.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).Return(anonTask, nil)
	// Orphan cleanup: markTaskFailed is called when Process errors out.
	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == anonTaskID && p.Status == statusFailed
	})).Return(repository.DocumentTask{}, nil)
	// registerConvertArtifacts must NOT run when triggerAnonymize fails —
	// so GetDocumentByID, CreateArtifactDocument, UpdateTaskResultPayload are not expected.
	pc.On("Process", mock.Anything, mock.Anything).Return(errors.New("python worker down"))

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	// No error propagated — callback already persisted.
	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)
	ms.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// 6. taskID not found — returns 404 error.
func TestWorkerService_HandleStatusUpdate_TaskNotFound(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, pgx.ErrNoRows)

	svc := newTestWorkerService(ms, pc)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{Status: "processing"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "task not found")
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 7. DB returns ErrNoRows from CreateDocumentTaskInternal (ON CONFLICT DO NOTHING) —
// anonymize task already exists, creation is skipped idempotently.
func TestWorkerService_HandleStatusUpdate_ConvertCompleted_AnonAlreadyExists_Idempotent(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	artifactID := uuid.New()
	mdPath := "tenders/docs/test.md"

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	parentDoc := repository.Document{ID: docID, OrganizationID: uuid.New()}
	artifactDoc := repository.Document{ID: artifactID}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	// ON CONFLICT DO NOTHING: pgx returns ErrNoRows when nothing was inserted.
	ms.On("CreateDocumentTaskInternal", mock.Anything, repository.CreateDocumentTaskInternalParams{
		DocumentID:       docID,
		ModuleName:       "anonymize",
		InputStoragePath: mdPath,
	}).Return(repository.DocumentTask{}, pgx.ErrNoRows)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(parentDoc, nil)
	ms.On("CreateArtifactDocument", mock.Anything, mock.Anything).Return(artifactDoc, nil)
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.MatchedBy(func(p repository.UpdateTaskResultPayloadParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 8. convert completed → artifact document created and result_payload updated with MD document UUID.
func TestWorkerService_HandleStatusUpdate_ConvertCompleted_RegistersArtifact(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	anonTaskID := uuid.New()
	artifactID := uuid.New()
	mdPath := "tenders/docs/test.md"

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	anonTask := makeDocumentTask(anonTaskID, docID, "anonymize", "pending", nil)
	parentDoc := repository.Document{ID: docID, OrganizationID: uuid.New()}
	artifactDoc := repository.Document{ID: artifactID}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).Return(anonTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(parentDoc, nil)
	ms.On("CreateArtifactDocument", mock.Anything, mock.MatchedBy(func(p repository.CreateArtifactDocumentParams) bool {
		return p.ArtifactKind.String == "convert_md" &&
			p.MimeType.String == "text/markdown" &&
			p.StoragePath == mdPath &&
			p.FileName == "test.md" &&
			p.ParentID == (pgtype.UUID{Bytes: docID, Valid: true})
	})).Return(artifactDoc, nil)
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.MatchedBy(func(p repository.UpdateTaskResultPayloadParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)
	pc.On("Process", mock.Anything, mock.Anything).Return(nil)

	svc := newTestWorkerService(ms, pc)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	require.NoError(t, err)
	ms.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// 9. anonymize completed → two artifact documents created; result_payload updated.
func TestWorkerService_HandleStatusUpdate_AnonymizeCompleted_RegistersArtifacts(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	anonArtifactID := uuid.New()
	entitiesArtifactID := uuid.New()

	anonPayload, _ := json.Marshal(map[string]any{
		"anonymized_storage_path":   "tenders/docs/anon.md",
		"entities_map_storage_path": "tenders/docs/entities.json",
		"entity_count":              5,
	})
	returnedTask := makeDocumentTask(taskID, docID, "anonymize", "completed", anonPayload)
	parentDoc := repository.Document{ID: docID, OrganizationID: uuid.New()}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(parentDoc, nil)
	ms.On("CreateArtifactDocument", mock.Anything, mock.MatchedBy(func(p repository.CreateArtifactDocumentParams) bool {
		return p.ArtifactKind.String == "anonymize_doc"
	})).Return(repository.Document{ID: anonArtifactID}, nil).Once()
	ms.On("CreateArtifactDocument", mock.Anything, mock.MatchedBy(func(p repository.CreateArtifactDocumentParams) bool {
		return p.ArtifactKind.String == "anonymize_entities"
	})).Return(repository.Document{ID: entitiesArtifactID}, nil).Once()
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.MatchedBy(func(p repository.UpdateTaskResultPayloadParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: anonPayload,
	})

	require.NoError(t, err)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// ── resolve_keys / extract helpers ───────────────────────────────────────────

func resolveKeysPayloadJSON(newKeys []resolveKeysNewKey, schema []extractionSchemaEntry, mdDocID string) json.RawMessage {
	b, err := json.Marshal(resolveKeysPayload{
		NewKeys:        newKeys,
		ResolvedSchema: schema,
		MDDocumentID:   mdDocID,
	})
	if err != nil {
		panic("resolveKeysPayloadJSON: " + err.Error())
	}
	return b
}

func extractPayloadJSON(data map[string]string) json.RawMessage {
	b, err := json.Marshal(data)
	if err != nil {
		panic("extractPayloadJSON: " + err.Error())
	}
	return b
}

// ── resolve_keys tests ────────────────────────────────────────────────────────

// 10. resolve_keys completed → upsert new keys, get md doc, create extract task, trigger Process.
func TestWorkerService_HandleStatusUpdate_ResolveKeysCompleted_TriggersExtract(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()
	mdDocID := uuid.New()
	extractTaskID := uuid.New()
	mdPath := "tenders/docs/anon.md"

	newKey := resolveKeysNewKey{
		KeyName:     "contract_value",
		SourceQuery: "What is the contract value?",
		DataType:    "string",
	}
	schema := []extractionSchemaEntry{{KeyName: "contract_value", DataType: "string"}}
	resultPayload := resolveKeysPayloadJSON([]resolveKeysNewKey{newKey}, schema, mdDocID.String())

	returnedTask := makeDocumentTask(taskID, docID, "resolve_keys", "completed", resultPayload)
	doc := repository.Document{ID: docID, OrganizationID: orgID}
	mdDoc := repository.Document{ID: mdDocID, OrganizationID: orgID, StoragePath: mdPath}
	extractTask := makeDocumentTask(extractTaskID, docID, "extract", "pending", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	ms.On("UpsertExtractionKey", mock.Anything, repository.UpsertExtractionKeyParams{
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		KeyName:        "contract_value",
		SourceQuery:    "What is the contract value?",
		DataType:       "string",
	}).Return(repository.ExtractionKey{}, nil)
	ms.On("GetDocumentByID", mock.Anything, mdDocID).Return(mdDoc, nil)
	ms.On("CreateDocumentTaskInternal", mock.Anything, repository.CreateDocumentTaskInternalParams{
		DocumentID:       docID,
		ModuleName:       moduleExtract,
		InputStoragePath: mdPath,
	}).Return(extractTask, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(req pythonworker.ProcessRequest) bool {
		return req.TaskID == extractTaskID.String() &&
			req.DocumentID == docID.String() &&
			req.ModuleName == moduleExtract &&
			req.StoragePath == mdPath
	})).Return(nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: resultPayload,
	})

	require.NoError(t, err)
	assert.Equal(t, "resolve_keys", task.ModuleName)
	ms.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// 11. resolve_keys completed but extract task already exists — idempotent skip, no Process call.
func TestWorkerService_HandleStatusUpdate_ResolveKeysCompleted_ExtractAlreadyExists_Idempotent(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()
	mdDocID := uuid.New()
	mdPath := "tenders/docs/anon.md"

	schema := []extractionSchemaEntry{{KeyName: "price", DataType: "number"}}
	resultPayload := resolveKeysPayloadJSON(nil, schema, mdDocID.String())

	returnedTask := makeDocumentTask(taskID, docID, "resolve_keys", "completed", resultPayload)
	doc := repository.Document{ID: docID, OrganizationID: orgID}
	mdDoc := repository.Document{ID: mdDocID, OrganizationID: orgID, StoragePath: mdPath}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	ms.On("GetDocumentByID", mock.Anything, mdDocID).Return(mdDoc, nil)
	// ON CONFLICT DO NOTHING → pgx.ErrNoRows when extract task already exists.
	ms.On("CreateDocumentTaskInternal", mock.Anything, repository.CreateDocumentTaskInternalParams{
		DocumentID:       docID,
		ModuleName:       moduleExtract,
		InputStoragePath: mdPath,
	}).Return(repository.DocumentTask{}, pgx.ErrNoRows)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: resultPayload,
	})

	require.NoError(t, err)
	assert.Equal(t, "resolve_keys", task.ModuleName)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// ── extract tests ─────────────────────────────────────────────────────────────

// 12. extract completed → parse payload, look up keys, upsert extracted data.
func TestWorkerService_HandleStatusUpdate_ExtractCompleted_UpsertsData(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()
	keyID := uuid.New()

	extractData := map[string]string{"contract_value": "1 000 000 тенге"}
	resultPayload := extractPayloadJSON(extractData)

	returnedTask := makeDocumentTask(taskID, docID, "extract", "completed", resultPayload)
	doc := repository.Document{ID: docID, OrganizationID: orgID}
	key := repository.ExtractionKey{ID: keyID, KeyName: "contract_value"}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	ms.On("GetExtractionKeysByNames", mock.Anything, mock.MatchedBy(func(p repository.GetExtractionKeysByNamesParams) bool {
		return p.OrganizationID == orgID && len(p.KeyNames) == 1 && p.KeyNames[0] == "contract_value"
	})).Return([]repository.ExtractionKey{key}, nil)
	ms.On("BatchUpsertExtractedData", mock.Anything, mock.MatchedBy(func(p repository.BatchUpsertExtractedDataParams) bool {
		return p.OrganizationID == orgID &&
			p.DocumentID == docID &&
			len(p.KeyIds) == 1 && p.KeyIds[0] == keyID &&
			len(p.ExtractedValues) == 1 && p.ExtractedValues[0] == "1 000 000 тенге"
	})).Return(nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: resultPayload,
	})

	require.NoError(t, err)
	assert.Equal(t, "extract", task.ModuleName)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 13. extract completed — key name not found in DB → log warn, no upsert call.
func TestWorkerService_HandleStatusUpdate_ExtractCompleted_UnknownKeySkipped(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()

	extractData := map[string]string{"unknown_field": "some value"}
	resultPayload := extractPayloadJSON(extractData)

	returnedTask := makeDocumentTask(taskID, docID, "extract", "completed", resultPayload)
	doc := repository.Document{ID: docID, OrganizationID: orgID}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	// GetExtractionKeysByNames returns empty slice — key is unknown to the tenant.
	ms.On("GetExtractionKeysByNames", mock.Anything, mock.Anything).Return([]repository.ExtractionKey{}, nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: resultPayload,
	})

	require.NoError(t, err)
	assert.Equal(t, "extract", task.ModuleName)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
	ms.AssertNotCalled(t, "BatchUpsertExtractedData")
}

// 14. extract completed — payload contains null value for a known key → null skipped, only non-null upserted.
func TestWorkerService_HandleStatusUpdate_ExtractCompleted_NullValueSkipped(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()
	keyID := uuid.New()

	// Python worker returns null for "missing_field" and a real value for "contract_value".
	resultPayload := json.RawMessage(`{"contract_value":"500 000 тенге","missing_field":null}`)

	returnedTask := makeDocumentTask(taskID, docID, "extract", "completed", resultPayload)
	doc := repository.Document{ID: docID, OrganizationID: orgID}
	key := repository.ExtractionKey{ID: keyID, KeyName: "contract_value"}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	ms.On("GetExtractionKeysByNames", mock.Anything, mock.Anything).Return([]repository.ExtractionKey{key}, nil)
	// Only the non-null key must be upserted.
	ms.On("BatchUpsertExtractedData", mock.Anything, mock.MatchedBy(func(p repository.BatchUpsertExtractedDataParams) bool {
		return p.OrganizationID == orgID &&
			p.DocumentID == docID &&
			len(p.KeyIds) == 1 && p.KeyIds[0] == keyID &&
			len(p.ExtractedValues) == 1 && p.ExtractedValues[0] == "500 000 тенге"
	})).Return(nil)

	svc := newTestWorkerService(ms, pc)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: resultPayload,
	})

	require.NoError(t, err)
	assert.Equal(t, "extract", task.ModuleName)
	ms.AssertExpectations(t)
}
