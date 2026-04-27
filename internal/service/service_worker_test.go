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

func TestWorkerService_HandleStatusUpdate_ExtractCompleted_SavesExtractedData(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()
	keyID := uuid.New()

	payload, _ := json.Marshal(map[string]any{
		"extracted_data": []map[string]any{
			{
				"key_name":   "advance_payment_percent",
				"value":      15.5,
				"confidence": 0.91,
			},
		},
	})
	returnedTask := makeDocumentTask(taskID, docID, "extract", "completed", payload)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentOrganizationID", mock.Anything, docID).Return(orgID, nil)
	ms.On("ListExtractionKeysByOrganization", mock.Anything, orgID).
		Return([]repository.ExtractionKey{{
			ID:             keyID,
			OrganizationID: orgID,
			KeyName:        "advance_payment_percent",
			SourceQuery:    "Какой процент аванса?",
			DataType:       "number",
		}}, nil)
	ms.On("UpsertDocumentExtractedData", mock.Anything, mock.MatchedBy(func(p repository.UpsertDocumentExtractedDataParams) bool {
		return p.OrganizationID == orgID &&
			p.DocumentID == docID &&
			p.KeyID == keyID &&
			string(p.ExtractedValue) == "15.5" &&
			p.Confidence.Valid &&
			p.Confidence.Float64 == 0.91
	})).Return(repository.DocumentExtractedDatum{ID: uuid.New()}, nil)

	svc := newTestWorkerService(ms, pc)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: payload,
	})

	require.NoError(t, err)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

func TestParseExtractedValueEntries_AcceptsSupportedPayloadShapes(t *testing.T) {
	cases := []struct {
		name              string
		payload           string
		wantKey           string
		wantValue         string
		wantConfidence    float64
		wantConfidenceSet bool
		wantLen           int
	}{
		{
			name:              "enveloped array",
			payload:           `{"extracted_data":[{"key_name":"advance_payment_percent","value":15.5,"confidence":0.91,"extra":"ignored"}]}`,
			wantKey:           "advance_payment_percent",
			wantValue:         `15.5`,
			wantConfidence:    0.91,
			wantConfidenceSet: true,
			wantLen:           1,
		},
		{
			name:              "plain array",
			payload:           `[{"key":"advance_payment_percent","extracted_value":15,"confidence":0.8}]`,
			wantKey:           "advance_payment_percent",
			wantValue:         `15`,
			wantConfidence:    0.8,
			wantConfidenceSet: true,
			wantLen:           1,
		},
		{
			name:              "nested object value",
			payload:           `{"advance_payment_percent":{"value":15,"confidence":0.75}}`,
			wantKey:           "advance_payment_percent",
			wantValue:         `15`,
			wantConfidence:    0.75,
			wantConfidenceSet: true,
			wantLen:           1,
		},
		{
			name:      "direct object value with metadata ignored",
			payload:   `{"advance_payment_percent":15,"metadata":{"model":"test"},"usage":{"tokens":10}}`,
			wantKey:   "advance_payment_percent",
			wantValue: `15`,
			wantLen:   1,
		},
		{
			name:    "unknown envelope fields only",
			payload: `{"metadata":{"model":"test"},"usage":{"tokens":10}}`,
			wantLen: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseExtractedValueEntries(json.RawMessage(tc.payload))

			require.NoError(t, err)
			require.Len(t, entries, tc.wantLen)
			if tc.wantLen == 0 {
				return
			}
			assert.Equal(t, tc.wantKey, entries[0].KeyName)
			assert.JSONEq(t, tc.wantValue, string(entries[0].Value))
			assert.Equal(t, tc.wantConfidenceSet, entries[0].Confidence.Valid)
			if tc.wantConfidenceSet {
				assert.Equal(t, tc.wantConfidence, entries[0].Confidence.Float64)
			}
		})
	}
}

func TestWorkerService_HandleStatusUpdate_ExtractCompleted_ContinuesAfterUpsertError(t *testing.T) {
	ctx := context.Background()
	ms := new(storemock.MockStore)
	pc := new(mockPythonClient)

	taskID := uuid.New()
	docID := uuid.New()
	orgID := uuid.New()
	firstKeyID := uuid.New()
	secondKeyID := uuid.New()

	payload, _ := json.Marshal(map[string]any{
		"extracted_data": []map[string]any{
			{"key_name": "advance_payment_percent", "value": 15},
			{"key_name": "contract_price", "value": 1000},
		},
	})
	returnedTask := makeDocumentTask(taskID, docID, "extract", "completed", payload)

	ms.On("UpdateWorkerTaskStatus", mock.Anything, mock.Anything).Return(returnedTask, nil)
	ms.On("GetDocumentOrganizationID", mock.Anything, docID).Return(orgID, nil)
	ms.On("ListExtractionKeysByOrganization", mock.Anything, orgID).
		Return([]repository.ExtractionKey{
			{ID: firstKeyID, OrganizationID: orgID, KeyName: "advance_payment_percent"},
			{ID: secondKeyID, OrganizationID: orgID, KeyName: "contract_price"},
		}, nil)
	ms.On("UpsertDocumentExtractedData", mock.Anything, mock.MatchedBy(func(p repository.UpsertDocumentExtractedDataParams) bool {
		return p.KeyID == firstKeyID
	})).Return(repository.DocumentExtractedDatum{}, errors.New("bad value")).Once()
	ms.On("UpsertDocumentExtractedData", mock.Anything, mock.MatchedBy(func(p repository.UpsertDocumentExtractedDataParams) bool {
		return p.KeyID == secondKeyID && string(p.ExtractedValue) == "1000"
	})).Return(repository.DocumentExtractedDatum{ID: uuid.New()}, nil).Once()

	svc := newTestWorkerService(ms, pc)
	_, err := svc.HandleStatusUpdate(ctx, taskID, WorkerStatusUpdate{
		Status:        "completed",
		ResultPayload: payload,
	})

	require.NoError(t, err)
	ms.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

func TestRawConfidence(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  float64
		valid bool
	}{
		{"valid zero", "0", 0, true},
		{"valid one", "1", 1, true},
		{"valid mid", "0.85", 0.85, true},
		{"above range", "1.1", 0, false},
		{"negative", "-0.1", 0, false},
		{"empty", "", 0, false},
		{"non-number", `"high"`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rawConfidence(json.RawMessage(tt.raw))
			assert.Equal(t, tt.valid, got.Valid)
			if tt.valid {
				assert.InDelta(t, tt.want, got.Float64, 1e-9)
			}
		})
	}
}
