package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

// newServerWithMockExtractionService wires a server whose extractionService is
// backed by the supplied MockQuerier with a nil python client (best-effort
// trigger is skipped, task is still persisted).
func newServerWithMockExtractionService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.extractionService = service.NewExtractionService(mq, nil, s.log)
	return s
}

// jsonBody serialises v to a *bytes.Buffer suitable for use as an HTTP request body.
func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

// initiateExtractionURL returns the URL for POST /api/v1/documents/:id/extract.
func initiateExtractionURL(docID uuid.UUID) string {
	return "/api/v1/documents/" + docID.String() + "/extract"
}

// ── POST /api/v1/documents/:id/extract ───────────────────────────────────────

// 1. Missing auth cookie → 401.
func TestInitiateExtraction_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	body := jsonBody(t, map[string]any{"questions": []string{"value?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(uuid.New()), body)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "GetDocument")
}

// 2. Malformed UUID in :id path param → 400.
func TestInitiateExtraction_InvalidDocID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	userID := uuid.New()
	orgID := uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"value?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/documents/not-a-uuid/extract", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "GetDocument")
}

// 3. Missing questions field (binding:required,min=1 fails) → 400.
func TestInitiateExtraction_MissingQuestions_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	userID := uuid.New()
	orgID := uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	// Empty body — questions field absent.
	body := jsonBody(t, map[string]any{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(uuid.New()), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "GetDocument")
}

// 4. Empty questions slice (service validates len > 0) → 400.
func TestInitiateExtraction_EmptyQuestions_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	userID := uuid.New()
	orgID := uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	// Gin's `min=1` binding on a slice rejects empty arrays at the binding layer.
	body := jsonBody(t, map[string]any{"questions": []string{}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(uuid.New()), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "GetDocument")
}

// 5. Document not found → 404.
func TestInitiateExtraction_DocumentNotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(repository.Document{}, pgx.ErrNoRows)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"What is the price?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// 6. resolve_keys task already exists → 409 Conflict.
func TestInitiateExtraction_AlreadyExists_Returns409(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()
	storagePath := "tenders/doc.pdf"

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	// ON CONFLICT DO NOTHING → pgx.ErrNoRows signals the task already exists.
	mq.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).Return(repository.DocumentTask{}, pgx.ErrNoRows)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"price?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mq.AssertExpectations(t)
}

// 7. Happy path → 201 with {"task_id": "<uuid>"}.
func TestInitiateExtraction_Success_Returns201WithTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()
	taskID := uuid.New()
	storagePath := "tenders/doc.pdf"

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).
		Return([]repository.ExtractionKey{}, nil)
	mq.On("CreateDocumentTaskInternal", mock.Anything, mock.MatchedBy(func(p repository.CreateDocumentTaskInternalParams) bool {
		return p.DocumentID == docID && p.ModuleName == "resolve_keys" && p.InputStoragePath == storagePath
	})).Return(repository.DocumentTask{ID: taskID, DocumentID: docID, ModuleName: "resolve_keys"}, nil)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"What is the contract value?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, taskID.String(), resp["task_id"])

	mq.AssertExpectations(t)
}

// 8. Internal DB error → 500.
func TestInitiateExtraction_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()
	storagePath := "tenders/doc.pdf"

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}, nil)
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return([]repository.ExtractionKey{}, nil)
	mq.On("CreateDocumentTaskInternal", mock.Anything, mock.Anything).
		Return(repository.DocumentTask{}, errs.New(errs.CodeInternalError, "db error", nil))

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"price?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}
