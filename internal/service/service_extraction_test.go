package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestExtractionService(mq *storemock.MockQuerier, pc workerPythonClient) *ExtractionService {
	return NewExtractionService(mq, pc, newTestLogger())
}

func makeDocument(id, orgID uuid.UUID, parentID *uuid.UUID, kind string, path string) repository.Document {
	doc := repository.Document{
		ID:             id,
		OrganizationID: orgID,
		StoragePath:    path,
		FileName:       "x",
	}
	if parentID != nil {
		doc.ParentID = pgtype.UUID{Bytes: *parentID, Valid: true}
	}
	if kind != "" {
		doc.ArtifactKind = pgtype.Text{String: kind, Valid: true}
	}
	return doc
}

// ── Initiate validation ───────────────────────────────────────────────────────

func TestInitiate_EmptyQuestions_ReturnsValidationError(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, uuid.New(), uuid.New(), []string{}, true)
	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertNotCalled(t, "GetDocument")
}

func TestInitiate_DocumentNotFound_Returns404(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()

	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID: docID, OrganizationID: orgID,
	}).Return(repository.Document{}, pgx.ErrNoRows)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"q"}, true)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
}

// ── Initiate progress paths ──────────────────────────────────────────────────

// No artifacts at all → ensures convert task is enqueued.
func TestInitiate_NoArtifacts_EnqueuesConvert(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()
	convertTaskID := uuid.New()
	storagePath := "tenders/docs/test.docx"

	doc := makeDocument(docID, orgID, nil, "", storagePath)
	convertTask := repository.DocumentTask{
		ID: convertTaskID, DocumentID: docID, ModuleName: moduleConvert,
		InputStoragePath: storagePath,
	}

	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.MatchedBy(func(p repository.CreateExtractionRequestParams) bool {
		return p.DocumentID == docID && p.OrganizationID == orgID && p.Anonymize
	})).Return(repository.ExtractionRequest{
		ID: uuid.New(), DocumentID: docID, OrganizationID: orgID,
		Questions: json.RawMessage(`["q1"]`), Anonymize: true, Status: requestStatusPending,
	}, nil)

	// Progress: GetDocumentByID → list artifacts (empty) → ensureConvertTask.
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	mq.On("ListDocumentsByParent", mock.Anything, mock.Anything).
		Return([]repository.Document{}, nil)
	mq.On("CreateDocumentTaskSingleton", mock.Anything, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       docID,
		ModuleName:       moduleConvert,
		InputStoragePath: storagePath,
	}).Return(convertTask, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(req pythonworker.ProcessRequest) bool {
		return req.ModuleName == moduleConvert && req.TaskID == convertTaskID.String()
	})).Return(nil)

	svc := newTestExtractionService(mq, pc)
	req, err := svc.Initiate(ctx, docID, orgID, []string{"q1"}, true)
	require.NoError(t, err)
	assert.Equal(t, requestStatusPending, req.Status)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// MD already exists, anonymize=false → resolve_keys fired immediately.
func TestInitiate_MDExists_AnonymizeFalse_FiresResolveKeys(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	mdID := uuid.New()
	orgID := uuid.New()
	requestID := uuid.New()
	resolveTaskID := uuid.New()
	storagePath := "docs/orig.docx"
	mdPath := "docs/orig.md"

	doc := makeDocument(docID, orgID, nil, "", storagePath)
	mdArtifact := makeDocument(mdID, orgID, &docID, artifactKindConvertMD, mdPath)
	createdReq := repository.ExtractionRequest{
		ID: requestID, DocumentID: docID, OrganizationID: orgID,
		Questions: json.RawMessage(`["q1"]`), Anonymize: false, Status: requestStatusPending,
	}
	resolveTask := repository.DocumentTask{
		ID: resolveTaskID, DocumentID: docID, ModuleName: moduleResolveKeys,
		ExtractionRequestID: pgtype.UUID{Bytes: requestID, Valid: true},
	}

	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.Anything).Return(createdReq, nil)
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	mq.On("ListDocumentsByParent", mock.Anything, mock.Anything).
		Return([]repository.Document{mdArtifact}, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	mq.On("CreateDocumentTaskForRequest", mock.Anything, mock.MatchedBy(func(p repository.CreateDocumentTaskForRequestParams) bool {
		return p.DocumentID == docID &&
			p.ModuleName == moduleResolveKeys &&
			p.InputStoragePath == mdPath &&
			p.ExtractionRequestID.Valid &&
			uuid.UUID(p.ExtractionRequestID.Bytes) == requestID
	})).Return(resolveTask, nil)
	mq.On("SetExtractionRequestStatus", mock.Anything, mock.MatchedBy(func(p repository.SetExtractionRequestStatusParams) bool {
		return p.ID == requestID && p.Status == requestStatusRunning
	})).Return(repository.ExtractionRequest{ID: requestID, Status: requestStatusRunning}, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(r pythonworker.ProcessRequest) bool {
		return r.ModuleName == moduleResolveKeys && r.StoragePath == mdPath && r.TaskID == resolveTaskID.String()
	})).Return(nil)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"q1"}, false)
	require.NoError(t, err)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// MD exists, anonymize=true, but no anonymize artifact yet → ensureAnonymizeTask.
func TestInitiate_MDExists_AnonymizeTrue_NoAnon_FiresAnonymize(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	mdID := uuid.New()
	orgID := uuid.New()
	mdPath := "docs/orig.md"
	anonTaskID := uuid.New()

	doc := makeDocument(docID, orgID, nil, "", "docs/orig.docx")
	mdArtifact := makeDocument(mdID, orgID, &docID, artifactKindConvertMD, mdPath)
	createdReq := repository.ExtractionRequest{
		ID: uuid.New(), DocumentID: docID, OrganizationID: orgID,
		Questions: json.RawMessage(`["q1"]`), Anonymize: true, Status: requestStatusPending,
	}
	anonTask := repository.DocumentTask{ID: anonTaskID, DocumentID: docID, ModuleName: moduleAnonymize, InputStoragePath: mdPath}

	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.Anything).Return(createdReq, nil)
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	mq.On("ListDocumentsByParent", mock.Anything, mock.Anything).
		Return([]repository.Document{mdArtifact}, nil)
	mq.On("CreateDocumentTaskSingleton", mock.Anything, repository.CreateDocumentTaskSingletonParams{
		DocumentID:       docID,
		ModuleName:       moduleAnonymize,
		InputStoragePath: mdPath,
	}).Return(anonTask, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(r pythonworker.ProcessRequest) bool {
		return r.ModuleName == moduleAnonymize && r.StoragePath == mdPath
	})).Return(nil)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"q1"}, true)
	require.NoError(t, err)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// Anonymize artifact already present → resolve_keys fired against its path.
func TestInitiate_AnonExists_AnonymizeTrue_FiresResolveKeysOnAnon(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	mdID := uuid.New()
	anonID := uuid.New()
	orgID := uuid.New()
	requestID := uuid.New()
	resolveTaskID := uuid.New()
	anonPath := "docs/orig.anon.md"

	doc := makeDocument(docID, orgID, nil, "", "docs/orig.docx")
	mdArtifact := makeDocument(mdID, orgID, &docID, artifactKindConvertMD, "docs/orig.md")
	anonArtifact := makeDocument(anonID, orgID, &docID, artifactKindAnonymizeDoc, anonPath)
	createdReq := repository.ExtractionRequest{
		ID: requestID, DocumentID: docID, OrganizationID: orgID,
		Questions: json.RawMessage(`["q1"]`), Anonymize: true, Status: requestStatusPending,
	}
	resolveTask := repository.DocumentTask{
		ID: resolveTaskID, DocumentID: docID, ModuleName: moduleResolveKeys,
	}

	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.Anything).Return(createdReq, nil)
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	mq.On("ListDocumentsByParent", mock.Anything, mock.Anything).
		Return([]repository.Document{mdArtifact, anonArtifact}, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	mq.On("CreateDocumentTaskForRequest", mock.Anything, mock.MatchedBy(func(p repository.CreateDocumentTaskForRequestParams) bool {
		return p.InputStoragePath == anonPath
	})).Return(resolveTask, nil)
	mq.On("SetExtractionRequestStatus", mock.Anything, mock.Anything).
		Return(repository.ExtractionRequest{}, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(r pythonworker.ProcessRequest) bool {
		return r.ModuleName == moduleResolveKeys && r.StoragePath == anonPath
	})).Return(nil)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"q1"}, true)
	require.NoError(t, err)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// Initiate succeeds even when Progress fails downstream — best-effort.
func TestInitiate_ProgressError_RequestStillReturned(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()
	doc := makeDocument(docID, orgID, nil, "", "docs/x.docx")

	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.Anything).Return(repository.ExtractionRequest{
		ID: uuid.New(), DocumentID: docID, OrganizationID: orgID,
		Anonymize: true, Status: requestStatusPending,
	}, nil)
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	mq.On("ListDocumentsByParent", mock.Anything, mock.Anything).
		Return(nil, errors.New("db down"))

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"q1"}, true)
	require.NoError(t, err) // progress failure is logged, not returned
}

// ── OnResolveKeysCompleted ───────────────────────────────────────────────────

func TestOnResolveKeysCompleted_FullFlow(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	mdID := uuid.New()
	orgID := uuid.New()
	requestID := uuid.New()
	taskID := uuid.New()
	extractTaskID := uuid.New()
	mdPath := "docs/orig.md"

	resultPayload, _ := json.Marshal(map[string]any{
		"new_keys": []map[string]string{
			{"key_name": "price", "source_query": "What is the price?", "data_type": "string"},
		},
		"resolved_schema": []map[string]string{
			{"key_name": "price", "data_type": "string"},
		},
	})
	resolveTask := repository.DocumentTask{
		ID: taskID, DocumentID: docID, ModuleName: moduleResolveKeys,
		ResultPayload:       resultPayload,
		ExtractionRequestID: pgtype.UUID{Bytes: requestID, Valid: true},
	}
	req := repository.ExtractionRequest{
		ID: requestID, DocumentID: docID, OrganizationID: orgID,
		Anonymize: false, Status: requestStatusRunning,
	}
	doc := makeDocument(docID, orgID, nil, "", "docs/orig.docx")
	mdArtifact := makeDocument(mdID, orgID, &docID, artifactKindConvertMD, mdPath)
	extractTask := repository.DocumentTask{
		ID: extractTaskID, DocumentID: docID, ModuleName: moduleExtract,
	}

	mq.On("GetExtractionRequestByID", mock.Anything, requestID).Return(req, nil)
	mq.On("UpsertExtractionKey", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, nil)
	mq.On("SetExtractionRequestResolvedSchema", mock.Anything, mock.MatchedBy(func(p repository.SetExtractionRequestResolvedSchemaParams) bool {
		return p.ID == requestID && len(p.ResolvedSchema) > 0
	})).Return(req, nil)
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, nil)
	mq.On("ListDocumentsByParent", mock.Anything, mock.Anything).
		Return([]repository.Document{mdArtifact}, nil)
	mq.On("CreateDocumentTaskForRequest", mock.Anything, mock.MatchedBy(func(p repository.CreateDocumentTaskForRequestParams) bool {
		return p.ModuleName == moduleExtract && p.InputStoragePath == mdPath
	})).Return(extractTask, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(r pythonworker.ProcessRequest) bool {
		return r.ModuleName == moduleExtract && r.StoragePath == mdPath
	})).Return(nil)

	svc := newTestExtractionService(mq, pc)
	require.NoError(t, svc.OnResolveKeysCompleted(ctx, resolveTask))
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

func TestOnResolveKeysCompleted_NoExtractionRequestID_Errors(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	task := repository.DocumentTask{
		ID: uuid.New(), ModuleName: moduleResolveKeys,
		ResultPayload: json.RawMessage(`{}`),
	}
	svc := newTestExtractionService(mq, pc)
	err := svc.OnResolveKeysCompleted(ctx, task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no extraction_request_id")
}

// ── OnExtractCompleted ────────────────────────────────────────────────────────

func TestOnExtractCompleted_PersistsAndMarksCompleted(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()
	requestID := uuid.New()
	keyID := uuid.New()
	taskID := uuid.New()

	resultPayload := json.RawMessage(`{"price":"100","missing":null}`)
	extractTask := repository.DocumentTask{
		ID: taskID, DocumentID: docID, ModuleName: moduleExtract,
		ResultPayload:       resultPayload,
		ExtractionRequestID: pgtype.UUID{Bytes: requestID, Valid: true},
	}
	req := repository.ExtractionRequest{ID: requestID, DocumentID: docID, OrganizationID: orgID}
	keyRow := repository.ExtractionKey{ID: keyID, KeyName: "price"}

	mq.On("GetExtractionRequestByID", mock.Anything, requestID).Return(req, nil)
	mq.On("GetExtractionKeysByNames", mock.Anything, mock.MatchedBy(func(p repository.GetExtractionKeysByNamesParams) bool {
		return p.OrganizationID == orgID
	})).Return([]repository.ExtractionKey{keyRow}, nil)
	mq.On("BatchUpsertExtractedData", mock.Anything, mock.MatchedBy(func(p repository.BatchUpsertExtractedDataParams) bool {
		return p.OrganizationID == orgID &&
			p.DocumentID == docID &&
			len(p.KeyIds) == 1 && p.KeyIds[0] == keyID &&
			len(p.ExtractedValues) == 1 && p.ExtractedValues[0] == "100"
	})).Return(nil)
	mq.On("SetExtractionRequestStatus", mock.Anything, mock.MatchedBy(func(p repository.SetExtractionRequestStatusParams) bool {
		return p.ID == requestID && p.Status == requestStatusCompleted
	})).Return(req, nil)

	svc := newTestExtractionService(mq, pc)
	require.NoError(t, svc.OnExtractCompleted(ctx, extractTask))
	mq.AssertExpectations(t)
}

// ── Progress on terminal status ──────────────────────────────────────────────

func TestProgress_CompletedRequest_NoOp(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	req := repository.ExtractionRequest{ID: uuid.New(), Status: requestStatusCompleted}
	svc := newTestExtractionService(mq, pc)
	require.NoError(t, svc.Progress(ctx, req))
	// No mock calls expected.
	mq.AssertNotCalled(t, "GetDocumentByID")
}

// ── GetRequest ────────────────────────────────────────────────────────────────

func TestGetRequest_NotFound(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	id := uuid.New()
	orgID := uuid.New()
	mq.On("GetExtractionRequest", mock.Anything, repository.GetExtractionRequestParams{
		ID: id, OrganizationID: orgID,
	}).Return(repository.ExtractionRequest{}, pgx.ErrNoRows)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.GetRequest(ctx, id, orgID)
	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
}
