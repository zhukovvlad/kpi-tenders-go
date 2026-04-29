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

// ── mock extractionPipeline ──────────────────────────────────────────────────

type mockPipeline struct {
	mock.Mock
}

func (m *mockPipeline) Progress(ctx context.Context, req repository.ExtractionRequest) error {
	return m.Called(ctx, req).Error(0)
}

func (m *mockPipeline) OnResolveKeysCompleted(ctx context.Context, task repository.DocumentTask) error {
	return m.Called(ctx, task).Error(0)
}

func (m *mockPipeline) OnExtractCompleted(ctx context.Context, task repository.DocumentTask) error {
	return m.Called(ctx, task).Error(0)
}

func (m *mockPipeline) MarkRequestFailed(ctx context.Context, requestID uuid.UUID, msg string) {
	m.Called(ctx, requestID, msg)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestWorkerService(ms *storemock.MockStore, pc *mockPythonClient, pl *mockPipeline) *WorkerService {
	return NewWorkerService(ms, pc, pl, newTestLogger())
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
		panic("convertPayloadJSON: " + err.Error())
	}
	return b
}

// ── tests ─────────────────────────────────────────────────────────────────────

// status=processing — only update, no chaining or pipeline calls.
func TestWorkerService_HandleStatusUpdate_Processing(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	returnedTask := makeDocumentTask(taskID, docID, "convert", "processing", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == taskID && p.Status == "processing"
	})).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc, pl)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{Status: "processing"})

	require.NoError(t, err)
	assert.Equal(t, "processing", task.Status)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
	pl.AssertNotCalled(t, "Progress")
}

// convert completed — triggers anonymize singleton, registers artifact, and
// progresses any pending extraction_requests for the document.
func TestWorkerService_HandleStatusUpdate_ConvertCompleted_TriggersAnonymizeAndProgress(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	anonTaskID := uuid.New()
	artifactID := uuid.New()
	mdPath := "tenders/docs/test.md"

	pendingReq := repository.ExtractionRequest{
		ID:             uuid.New(),
		DocumentID:     docID,
		OrganizationID: uuid.New(),
		Anonymize:      false,
		Status:         requestStatusPending,
	}

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	anonTask := makeDocumentTask(anonTaskID, docID, "anonymize", "pending", nil)
	parentDoc := repository.Document{ID: docID, OrganizationID: uuid.New()}
	artifactDoc := repository.Document{ID: artifactID}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("CreateDocumentTaskSingleton", mock.Anything, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       docID,
		ModuleName:       "anonymize",
		InputStoragePath: mdPath,
	}).Return(anonTask, nil)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(parentDoc, nil)
	ms.On("CreateArtifactDocument", mock.Anything, mock.Anything).Return(artifactDoc, nil)
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.MatchedBy(func(p repository.UpdateTaskResultPayloadParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)
	ms.On("ListPendingExtractionRequestsByDocument", mock.Anything, docID).
		Return([]repository.ExtractionRequest{pendingReq}, nil)

	pc.On("Process", mock.Anything, pythonworker.ProcessRequest{
		TaskID:      anonTaskID.String(),
		DocumentID:  docID.String(),
		ModuleName:  "anonymize",
		StoragePath: mdPath,
	}).Return(nil)
	pl.On("Progress", mock.Anything, pendingReq).Return(nil)

	svc := newTestWorkerService(ms, pc, pl)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)
	ms.AssertExpectations(t)
	pc.AssertExpectations(t)
	pl.AssertExpectations(t)
}

// anonymize completed — registers artifacts and progresses pending requests.
func TestWorkerService_HandleStatusUpdate_AnonymizeCompleted_RegistersAndProgress(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

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
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("ListPendingExtractionRequestsByDocument", mock.Anything, docID).
		Return([]repository.ExtractionRequest{}, nil)

	svc := newTestWorkerService(ms, pc, pl)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: anonPayload,
	})

	require.NoError(t, err)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
	pl.AssertNotCalled(t, "Progress")
}

// status=failed without extraction_request_id — only persistence, no pipeline call.
func TestWorkerService_HandleStatusUpdate_Failed_NoRequest(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	errMsg := "boom"
	returnedTask := makeDocumentTask(taskID, docID, "convert", "failed", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == taskID && p.Status == "failed" && p.ErrorMessage.Valid && p.ErrorMessage.String == errMsg
	})).Return(returnedTask, nil)

	svc := newTestWorkerService(ms, pc, pl)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:       "failed",
		ErrorMessage: &errMsg,
	})

	require.NoError(t, err)
	assert.Equal(t, "failed", task.Status)
	ms.AssertExpectations(t)
	pl.AssertNotCalled(t, "MarkRequestFailed")
}

// resolve_keys failed for an extraction_request → pipeline.MarkRequestFailed
// is invoked with the request id from the task.
func TestWorkerService_HandleStatusUpdate_ResolveKeysFailed_MarksRequestFailed(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	requestID := uuid.New()
	errMsg := "gemini timeout"

	failedTask := makeDocumentTask(taskID, docID, moduleResolveKeys, statusFailed, nil)
	failedTask.ExtractionRequestID = pgtype.UUID{Bytes: requestID, Valid: true}
	failedTask.ErrorMessage = pgtype.Text{String: errMsg, Valid: true}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(failedTask, nil)
	pl.On("MarkRequestFailed", mock.Anything, requestID, errMsg).Return()

	svc := newTestWorkerService(ms, pc, pl)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:       statusFailed,
		ErrorMessage: &errMsg,
	})
	require.NoError(t, err)
	pl.AssertExpectations(t)
}

// pythonClient.Process fails during convert→anonymize chaining — no error
// surfaced; orphan task gets marked failed.
func TestWorkerService_HandleStatusUpdate_PythonClientError_NoErrorPropagated(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	anonTaskID := uuid.New()
	mdPath := "tenders/docs/test.md"

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	anonTask := makeDocumentTask(anonTaskID, docID, "anonymize", "pending", nil)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == taskID
	})).Return(returnedTask, nil)
	ms.On("CreateDocumentTaskSingleton", mock.Anything, mock.Anything).Return(anonTask, nil)
	// Orphan cleanup after Process fails.
	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.MatchedBy(func(p repository.UpdateWorkerTaskStatusParams) bool {
		return p.ID == anonTaskID && p.Status == statusFailed
	})).Return(repository.DocumentTask{}, nil)
	ms.On("ListPendingExtractionRequestsByDocument", mock.Anything, docID).
		Return([]repository.ExtractionRequest{}, nil)
	pc.On("Process", mock.Anything, mock.Anything).Return(errors.New("python down"))

	svc := newTestWorkerService(ms, pc, pl)
	task, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)
	ms.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// taskID not found — returns 404 error, no chaining.
func TestWorkerService_HandleStatusUpdate_TaskNotFound(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, pgx.ErrNoRows)

	svc := newTestWorkerService(ms, pc, pl)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{Status: "processing"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "task not found")
	ms.AssertExpectations(t)
}

// convert anonymize singleton already exists — idempotent skip; convert
// artifact still registered, pending requests still progressed.
func TestWorkerService_HandleStatusUpdate_ConvertCompleted_AnonAlreadyExists(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	mdPath := "tenders/docs/test.md"

	returnedTask := makeDocumentTask(taskID, docID, "convert", "completed", convertPayloadJSON(mdPath))
	parentDoc := repository.Document{ID: docID, OrganizationID: uuid.New()}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("CreateDocumentTaskSingleton", mock.Anything, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       docID,
		ModuleName:       "anonymize",
		InputStoragePath: mdPath,
	}).Return(repository.DocumentTask{}, pgx.ErrNoRows)
	ms.On("GetDocumentByID", mock.Anything, docID).Return(parentDoc, nil)
	ms.On("CreateArtifactDocument", mock.Anything, mock.Anything).Return(repository.Document{ID: uuid.New()}, nil)
	ms.On("UpdateTaskResultPayload", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("ListPendingExtractionRequestsByDocument", mock.Anything, docID).
		Return([]repository.ExtractionRequest{}, nil)

	svc := newTestWorkerService(ms, pc, pl)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: convertPayloadJSON(mdPath),
	})

	require.NoError(t, err)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// resolve_keys completed → delegated to pipeline.OnResolveKeysCompleted.
func TestWorkerService_HandleStatusUpdate_ResolveKeysCompleted_DelegatesToPipeline(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	requestID := uuid.New()
	resultPayload := json.RawMessage(`{"new_keys":[],"resolved_schema":[]}`)

	completed := makeDocumentTask(taskID, docID, moduleResolveKeys, statusCompleted, resultPayload)
	completed.ExtractionRequestID = pgtype.UUID{Bytes: requestID, Valid: true}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(completed, nil)
	pl.On("OnResolveKeysCompleted", mock.Anything, mock.MatchedBy(func(t repository.DocumentTask) bool {
		return t.ID == taskID && t.ModuleName == moduleResolveKeys
	})).Return(nil)

	svc := newTestWorkerService(ms, pc, pl)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        statusCompleted,
		ResultPayload: resultPayload,
	})
	require.NoError(t, err)
	pl.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// extract completed → delegated to pipeline.OnExtractCompleted.
func TestWorkerService_HandleStatusUpdate_ExtractCompleted_DelegatesToPipeline(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)
	pl := new(mockPipeline)

	taskID := uuid.New()
	docID := uuid.New()
	requestID := uuid.New()
	resultPayload := json.RawMessage(`{"price":"100"}`)

	completed := makeDocumentTask(taskID, docID, moduleExtract, statusCompleted, resultPayload)
	completed.ExtractionRequestID = pgtype.UUID{Bytes: requestID, Valid: true}

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(completed, nil)
	pl.On("OnExtractCompleted", mock.Anything, mock.MatchedBy(func(t repository.DocumentTask) bool {
		return t.ID == taskID && t.ModuleName == moduleExtract
	})).Return(nil)

	svc := newTestWorkerService(ms, pc, pl)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        statusCompleted,
		ResultPayload: resultPayload,
	})
	require.NoError(t, err)
	pl.AssertExpectations(t)
}
