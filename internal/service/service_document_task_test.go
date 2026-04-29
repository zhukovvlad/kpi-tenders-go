package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/pythonworker"
	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

func TestDocumentTaskService_Create_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	params := repository.CreateDocumentTaskParams{
		DocumentID:     uuid.New(),
		ModuleName:     "convert",
		OrganizationID: uuid.New(),
	}
	expected := repository.DocumentTask{ID: uuid.New(), ModuleName: "convert"}

	mq.On("CreateDocumentTask", mock.Anything, params).Return(expected, nil)

	task, err := svc.Create(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, expected, task)
	mq.AssertExpectations(t)
}

func TestDocumentTaskService_Create_DocumentNotFound(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	mq.On("CreateDocumentTask", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, pgx.ErrNoRows)

	_, err := svc.Create(context.Background(), repository.CreateDocumentTaskParams{
		DocumentID:     uuid.New(),
		ModuleName:     "convert",
		OrganizationID: uuid.New(),
	})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeNotFound, appErr.Code)
	mq.AssertExpectations(t)
}

func TestDocumentTaskService_Create_DuplicateModule_ReturnsConflict(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	pgErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "uq_document_tasks_document_module",
	}
	mq.On("CreateDocumentTask", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, pgErr)

	_, err := svc.Create(context.Background(), repository.CreateDocumentTaskParams{
		DocumentID:     uuid.New(),
		ModuleName:     "convert",
		OrganizationID: uuid.New(),
	})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeConflict, appErr.Code)
	mq.AssertExpectations(t)
}

func TestDocumentTaskService_Create_DBError_ReturnsInternalError(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	// A different unique violation (wrong constraint name) must NOT become 409.
	otherPgErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "some_other_constraint",
	}
	mq.On("CreateDocumentTask", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, otherPgErr)

	_, err := svc.Create(context.Background(), repository.CreateDocumentTaskParams{
		DocumentID:     uuid.New(),
		ModuleName:     "convert",
		OrganizationID: uuid.New(),
	})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeInternalError, appErr.Code)
	mq.AssertExpectations(t)
}

func TestDocumentTaskService_Create_UnknownModule_ReturnsValidationFailed(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	_, err := svc.Create(context.Background(), repository.CreateDocumentTaskParams{
		DocumentID:     uuid.New(),
		ModuleName:     "totally_unknown",
		OrganizationID: uuid.New(),
	})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	// INSERT must NOT happen for unsupported modules.
	mq.AssertNotCalled(t, "CreateDocumentTask")
}

func TestDocumentTaskService_Create_AnonymizeViaPublicAPI_ReturnsValidationFailed(t *testing.T) {
	// 'anonymize' passes pythonworker.ValidateModule but must be blocked by the
	// public-API guard (moduleConvert check) in DocumentTaskService.Create.
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	_, err := svc.Create(context.Background(), repository.CreateDocumentTaskParams{
		DocumentID:     uuid.New(),
		ModuleName:     "anonymize",
		OrganizationID: uuid.New(),
	})

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeValidationFailed, appErr.Code)
	// INSERT must NOT be called.
	mq.AssertNotCalled(t, "CreateDocumentTask")
}

func TestDocumentTaskService_Create_TriggersPython_WithCorrectFields(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)
	svc := NewDocumentTaskService(mq, pc, newTestLogger())

	docID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()
	storagePath := "orgs/abc/file.pdf"

	params := repository.CreateDocumentTaskParams{
		DocumentID:     docID,
		ModuleName:     "convert",
		OrganizationID: orgID,
	}
	task := repository.DocumentTask{
		ID:               taskID,
		DocumentID:       docID,
		ModuleName:       "convert",
		InputStoragePath: storagePath,
	}

	mq.On("CreateDocumentTask", mock.Anything, params).Return(task, nil)
	pc.On("Process", mock.Anything, pythonworker.ProcessRequest{
		TaskID:      taskID.String(),
		DocumentID:  docID.String(),
		ModuleName:  "convert",
		StoragePath: storagePath,
	}).Return(nil)

	result, err := svc.Create(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, task, result)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

func TestDocumentTaskService_Create_PythonError_ReturnsTaskWithoutError(t *testing.T) {
	mq := new(storemock.MockQuerier)
	pc := new(mockPythonClient)
	svc := NewDocumentTaskService(mq, pc, newTestLogger())

	docID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()

	params := repository.CreateDocumentTaskParams{
		DocumentID:     docID,
		ModuleName:     "convert",
		OrganizationID: orgID,
	}
	task := repository.DocumentTask{
		ID:               taskID,
		DocumentID:       docID,
		ModuleName:       "convert",
		InputStoragePath: "orgs/abc/file.pdf",
	}

	mq.On("CreateDocumentTask", mock.Anything, params).Return(task, nil)
	pc.On("Process", mock.Anything, mock.Anything).Return(errors.New("python unavailable"))

	result, err := svc.Create(context.Background(), params)

	// Best-effort: Python error must NOT bubble up to the caller.
	require.NoError(t, err)
	assert.Equal(t, task, result)
	mq.AssertExpectations(t)
	pc.AssertExpectations(t)
}

func TestDocumentTaskService_ListByDocuments_Success(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	orgID := uuid.New()
	docID1 := uuid.New()
	docID2 := uuid.New()
	ids := []uuid.UUID{docID1, docID2}

	expected := []repository.DocumentTask{
		{ID: uuid.New(), DocumentID: docID1, ModuleName: "convert"},
		{ID: uuid.New(), DocumentID: docID2, ModuleName: "convert"},
	}

	mq.On("ListTasksByDocuments", mock.Anything, repository.ListTasksByDocumentsParams{
		DocumentIds:    ids,
		OrganizationID: orgID,
	}).Return(expected, nil)

	tasks, err := svc.ListByDocuments(context.Background(), ids, orgID)

	require.NoError(t, err)
	assert.Equal(t, expected, tasks)
	mq.AssertExpectations(t)
}

func TestDocumentTaskService_ListByDocuments_EmptySlice(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	orgID := uuid.New()

	mq.On("ListTasksByDocuments", mock.Anything, repository.ListTasksByDocumentsParams{
		DocumentIds:    []uuid.UUID{},
		OrganizationID: orgID,
	}).Return([]repository.DocumentTask{}, nil)

	tasks, err := svc.ListByDocuments(context.Background(), []uuid.UUID{}, orgID)

	require.NoError(t, err)
	assert.Empty(t, tasks)
	mq.AssertExpectations(t)
}

func TestDocumentTaskService_ListByDocuments_RepoError(t *testing.T) {
	mq := new(storemock.MockQuerier)
	svc := NewDocumentTaskService(mq, nil, newTestLogger())

	orgID := uuid.New()
	ids := []uuid.UUID{uuid.New()}

	mq.On("ListTasksByDocuments", mock.Anything, mock.Anything).Return([]repository.DocumentTask(nil), errors.New("db error"))

	_, err := svc.ListByDocuments(context.Background(), ids, orgID)

	require.Error(t, err)
	var appErr *errs.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errs.CodeInternalError, appErr.Code)
	mq.AssertExpectations(t)
}
