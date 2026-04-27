package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// ── tests ─────────────────────────────────────────────────────────────────────

// 1. Happy path: questions provided, doc found, task created, Python triggered.
func TestExtractionService_Initiate_Success(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()
	storagePath := "tenders/docs/original.docx"
	questions := []string{"What is the contract value?", "What is the deadline?"}

	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}
	keys := []repository.ExtractionKey{
		{ID: uuid.New(), KeyName: "existing_key", SourceQuery: "Existing?", DataType: "string"},
	}
	task := repository.DocumentTask{ID: taskID, DocumentID: docID, ModuleName: "resolve_keys", Status: "pending"}

	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(doc, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return(keys, nil)
	mq.On("CreateDocumentTaskInternal", mock.Anything, repository.CreateDocumentTaskInternalParams{
		DocumentID:       docID,
		ModuleName:       moduleResolveKeys,
		InputStoragePath: storagePath,
	}).Return(task, nil)
	pc.On("Process", mock.Anything, mock.MatchedBy(func(req pythonworker.ProcessRequest) bool {
		return req.TaskID == taskID.String() &&
			req.DocumentID == docID.String() &&
			req.ModuleName == moduleResolveKeys &&
			req.StoragePath == storagePath &&
			req.Kwargs != nil
	})).Return(nil)

	svc := newTestExtractionService(mq, pc)
	result, err := svc.Initiate(ctx, docID, orgID, questions)

	require.NoError(t, err)
	assert.Equal(t, taskID, result.ID)
	assert.Equal(t, moduleResolveKeys, result.ModuleName)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

// 2. Empty questions slice → validation error, no DB calls.
func TestExtractionService_Initiate_EmptyQuestions(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, uuid.New(), uuid.New(), []string{})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	mq.AssertNotCalled(t, "GetDocument")
	pc.AssertNotCalled(t, "Process")
}

// 3. Document not found → 404.
func TestExtractionService_Initiate_DocumentNotFound(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()

	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(repository.Document{}, pgx.ErrNoRows)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"some question"})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 4. resolve_keys task already exists → 409 Conflict.
func TestExtractionService_Initiate_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()
	storagePath := "tenders/docs/doc.pdf"

	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}

	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(doc, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	// ON CONFLICT DO NOTHING → pgx returns ErrNoRows when nothing is inserted.
	mq.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, pgx.ErrNoRows)

	svc := newTestExtractionService(mq, pc)
	_, err := svc.Initiate(ctx, docID, orgID, []string{"What is the value?"})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	mq.AssertExpectations(t)
	pc.AssertNotCalled(t, "Process")
}

// 5. Nil python client → task returned without triggering Process.
func TestExtractionService_Initiate_NilPythonClient(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)

	docID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()
	storagePath := "tenders/docs/doc.pdf"

	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}
	task := repository.DocumentTask{ID: taskID, DocumentID: docID, ModuleName: moduleResolveKeys}

	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(doc, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	mq.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).Return(task, nil)

	// No python client — pass nil explicitly.
	svc := NewExtractionService(mq, nil, newTestLogger())
	result, err := svc.Initiate(ctx, docID, orgID, []string{"question"})

	require.NoError(t, err)
	assert.Equal(t, taskID, result.ID)
	mq.AssertExpectations(t)
}

// 6. Python client errors → task still returned (best-effort trigger).
func TestExtractionService_Initiate_PythonClientError_BestEffort(t *testing.T) {
	ctx := context.Background()
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)

	docID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()
	storagePath := "tenders/docs/doc.pdf"

	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}
	task := repository.DocumentTask{ID: taskID, DocumentID: docID, ModuleName: moduleResolveKeys}

	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	mq.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).Return(task, nil)
	pc.On("Process", mock.Anything, mock.Anything).Return(errors.New("python worker down"))

	svc := newTestExtractionService(mq, pc)
	result, err := svc.Initiate(ctx, docID, orgID, []string{"question"})

	// Error is logged but not propagated — task was already persisted.
	require.NoError(t, err)
	assert.Equal(t, taskID, result.ID)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}
