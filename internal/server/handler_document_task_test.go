package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
)

// newServerWithMockDocumentTaskService wires a server whose documentTaskService
// is backed by the supplied MockQuerier with a nil python client.
func newServerWithMockDocumentTaskService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.documentTaskService = service.NewDocumentTaskService(mq, nil, s.log)
	return s
}

// listTasksURL returns GET /api/v1/tasks with the given raw query string.
func listTasksURL(query string) string {
	if query == "" {
		return "/api/v1/tasks"
	}
	return "/api/v1/tasks?" + query
}

// ── GET /api/v1/tasks ─────────────────────────────────────────────────────────

// 1. No auth → 401.
func TestListDocumentTasks_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		listTasksURL("document_id="+uuid.New().String()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "ListTasksByDocument")
}

// 2. No query params → 400.
func TestListDocumentTasks_NoParams_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(""), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 3. Both document_id and document_ids → 400.
func TestListDocumentTasks_BothParams_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	query := fmt.Sprintf("document_id=%s&document_ids=%s", uuid.New(), uuid.New())
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(query), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 4. Invalid document_id (single) → 400.
func TestListDocumentTasks_InvalidSingleID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		listTasksURL("document_id=not-a-uuid"), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 5. Single document_id happy path → 200.
func TestListDocumentTasks_SingleID_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	docID := uuid.New()
	expected := []repository.DocumentTask{{ID: uuid.New(), DocumentID: docID, ModuleName: "convert"}}

	mq.On("ListTasksByDocument", mock.Anything, repository.ListTasksByDocumentParams{
		DocumentID:     docID,
		OrganizationID: orgID,
	}).Return(expected, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		listTasksURL("document_id="+docID.String()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// 6. Single document_id, service error → 500.
func TestListDocumentTasks_SingleID_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	mq.On("ListTasksByDocument", mock.Anything, mock.Anything).
		Return([]repository.DocumentTask(nil), errors.New("db error"))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		listTasksURL("document_id="+uuid.New().String()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}

// 7. Batch document_ids happy path → 200.
func TestListDocumentTasks_BatchIDs_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	id1, id2 := uuid.New(), uuid.New()
	expected := []repository.DocumentTask{
		{ID: uuid.New(), DocumentID: id1, ModuleName: "convert"},
		{ID: uuid.New(), DocumentID: id2, ModuleName: "convert"},
	}

	mq.On("ListTasksByDocuments", mock.Anything, repository.ListTasksByDocumentsParams{
		DocumentIds:    []uuid.UUID{id1, id2},
		OrganizationID: orgID,
	}).Return(expected, nil)

	query := "document_ids=" + id1.String() + "," + id2.String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(query), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// 8. Batch with invalid UUID in document_ids → 400.
func TestListDocumentTasks_BatchIDs_InvalidUUID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	query := "document_ids=" + uuid.New().String() + ",not-a-uuid"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(query), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "ListTasksByDocuments")
}

// 9. Batch with >100 IDs → 400.
func TestListDocumentTasks_BatchIDs_TooMany_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	parts := make([]string, 101)
	for i := range parts {
		parts[i] = uuid.New().String()
	}
	query := "document_ids=" + strings.Join(parts, ",")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(query), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "ListTasksByDocuments")
}

// 10. Batch document_ids, service error → 500.
func TestListDocumentTasks_BatchIDs_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	mq.On("ListTasksByDocuments", mock.Anything, mock.Anything).
		Return([]repository.DocumentTask(nil), errors.New("db error"))

	query := "document_ids=" + uuid.New().String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(query), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}

// 11. document_id present + document_ids present but empty → 400 (mutual-exclusivity regression).
func TestListDocumentTasks_DocumentIDAndEmptyDocumentIDs_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockDocumentTaskService(t, mq)

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	// ?document_id=<uuid>&document_ids= — both keys present; document_ids is empty.
	query := "document_id=" + uuid.New().String() + "&document_ids="
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, listTasksURL(query), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "ListTasksByDocument")
	mq.AssertNotCalled(t, "ListTasksByDocuments")
}
