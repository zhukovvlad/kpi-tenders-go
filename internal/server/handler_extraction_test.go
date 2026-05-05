package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

// newServerWithMockExtractionService wires a server whose extractionService is
// backed by the supplied MockQuerier with a nil python client (Progress runs
// but does not publish; the request is still persisted).
func newServerWithMockExtractionService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.extractionService = service.NewExtractionService(mq, nil, s.log)
	return s
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func initiateExtractionURL(docID uuid.UUID) string {
	return "/api/v1/documents/" + docID.String() + "/extract"
}

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

// 2. Malformed UUID in :id → 400.
func TestInitiateExtraction_InvalidDocID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"value?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/documents/not-a-uuid/extract", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 3. Missing questions field → 400 from binding.
func TestInitiateExtraction_MissingQuestions_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(uuid.New()), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 4. Empty questions slice → 400 from gin's `min=1` binding.
func TestInitiateExtraction_EmptyQuestions_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(uuid.New()), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// 5. Document not found → 404.
func TestInitiateExtraction_DocumentNotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID: docID, OrganizationID: orgID,
	}).Return(repository.Document{}, pgx.ErrNoRows)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"price?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// 6. Happy path → 201 with extraction_request_id; default anonymize=true.
// The DB sequence: GetDocument → CreateExtractionRequest → progress (best-effort).
// Progress can fail (e.g. ListDocumentsByParent error) but Initiate still
// returns the request, so the handler still emits 201.
func TestInitiateExtraction_Success_Returns201(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	orgID := uuid.New()
	requestID := uuid.New()
	storagePath := "tenders/doc.pdf"

	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: storagePath}
	createdReq := repository.ExtractionRequest{
		ID:             requestID,
		DocumentID:     docID,
		OrganizationID: orgID,
		Anonymize:      true,
		Status:         "pending",
	}

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID: docID, OrganizationID: orgID,
	}).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.MatchedBy(func(p repository.CreateExtractionRequestParams) bool {
		return p.DocumentID == docID && p.OrganizationID == orgID && p.Anonymize
	})).Return(createdReq, nil)
	// Progress runs but errors out (fail fast); 201 is still returned.
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, errors.New("transient"))

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body := jsonBody(t, map[string]any{"questions": []string{"What is the price?"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, requestID.String(), resp["extraction_request_id"])
	assert.Equal(t, "pending", resp["status"])
	mq.AssertExpectations(t)
}

// 7. CreateExtractionRequest fails → 500.
func TestInitiateExtraction_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	orgID := uuid.New()
	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: "x"}

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.Anything).
		Return(repository.ExtractionRequest{}, errors.New("db down"))

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
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

// 8. Explicit anonymize=false propagates to CreateExtractionRequest.
func TestInitiateExtraction_AnonymizeFalse_Propagates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	orgID := uuid.New()
	doc := repository.Document{ID: docID, OrganizationID: orgID, StoragePath: "x"}

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, mock.Anything).Return(doc, nil)
	mq.On("CreateExtractionRequest", mock.Anything, mock.MatchedBy(func(p repository.CreateExtractionRequestParams) bool {
		return !p.Anonymize
	})).Return(repository.ExtractionRequest{
		ID: uuid.New(), DocumentID: docID, OrganizationID: orgID,
		Anonymize: false, Status: "pending",
	}, nil)
	mq.On("GetDocumentByID", mock.Anything, docID).Return(doc, errors.New("stop progress"))

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	flag := false
	body := jsonBody(t, map[string]any{"questions": []string{"q"}, "anonymize": flag})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		initiateExtractionURL(docID), body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mq.AssertExpectations(t)
}
