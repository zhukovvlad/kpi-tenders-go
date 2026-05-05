package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// newServerWithMockComparisonSessionService wires a server whose
// comparisonSessionService is backed by the supplied MockQuerier.
func newServerWithMockComparisonSessionService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.comparisonSessionService = service.NewComparisonSessionService(mq, s.log)
	return s
}

func comparisonSessionURL(id ...uuid.UUID) string {
	base := "/api/v1/comparison-sessions"
	if len(id) > 0 {
		return base + "/" + id[0].String()
	}
	return base
}

func fakeSession(orgID uuid.UUID) repository.ComparisonSession {
	return repository.ComparisonSession{
		ID:             uuid.New(),
		OrganizationID: orgID,
		CreatedAt:      time.Now(),
	}
}

// ── ListComparisonSessions ────────────────────────────────────────────────────

// 1. Missing auth → 401.
func TestListComparisonSessions_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		comparisonSessionURL(), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "ListComparisonSessionsByOrg")
}

// 2. Success — returns session list.
func TestListComparisonSessions_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	sessions := []repository.ComparisonSession{fakeSession(orgID)}
	mq.On("ListComparisonSessionsByOrg", mock.Anything, orgID).Return(sessions, nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		comparisonSessionURL(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// ── GetComparisonSession ──────────────────────────────────────────────────────

// 3. No auth → 401.
func TestGetComparisonSession_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		comparisonSessionURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "GetComparisonSession")
}

// 4. Malformed UUID → 400.
func TestGetComparisonSession_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/comparison-sessions/not-a-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "GetComparisonSession")
}

// 5. Session not found → 404.
func TestGetComparisonSession_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("GetComparisonSession", mock.Anything, repository.GetComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(repository.ComparisonSession{}, pgx.ErrNoRows)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		comparisonSessionURL(sessionID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// 6. Success — returns session and tenant-scoped documents.
func TestGetComparisonSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	mq := new(storemock.MockQuerier)

	sess := repository.ComparisonSession{
		ID:             sessionID,
		OrganizationID: orgID,
		CreatedAt:      time.Now(),
	}
	mq.On("GetComparisonSession", mock.Anything, repository.GetComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(sess, nil)
	mq.On("ListComparisonSessionDocuments", mock.Anything,
		repository.ListComparisonSessionDocumentsParams{
			SessionID:      sessionID,
			OrganizationID: orgID,
		}).Return([]repository.ComparisonSessionDocument{}, nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		comparisonSessionURL(sessionID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body, "session")
	assert.Contains(t, body, "documents")
	mq.AssertExpectations(t)
}

// ── CreateComparisonSession ───────────────────────────────────────────────────

// 7. No auth → 401.
func TestCreateComparisonSession_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		comparisonSessionURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "CreateComparisonSession")
}

// 8. Invalid contract_kind_id format → 400.
func TestCreateComparisonSession_InvalidContractKindID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "member")
	require.NoError(t, err)

	bodyBytes, _ := json.Marshal(map[string]any{"contract_kind_id": "not-a-uuid"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		comparisonSessionURL(), bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "CreateComparisonSession")
}

// 9. Success (no contract_kind) → 201.
func TestCreateComparisonSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	userID := uuid.New()
	mq := new(storemock.MockQuerier)

	sess := fakeSession(orgID)
	mq.On("CreateComparisonSession", mock.Anything, mock.MatchedBy(func(p repository.CreateComparisonSessionParams) bool {
		return p.OrganizationID == orgID
	})).Return(sess, nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	name := "Test Session"
	bodyBytes, _ := json.Marshal(map[string]any{"name": name})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		comparisonSessionURL(), bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mq.AssertExpectations(t)
}

// ── DeleteComparisonSession ───────────────────────────────────────────────────

// 10. No auth → 401.
func TestDeleteComparisonSession_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		comparisonSessionURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "DeleteComparisonSession")
}

// 11. Session not found → 404.
func TestDeleteComparisonSession_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("DeleteComparisonSession", mock.Anything, repository.DeleteComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(int64(0), nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		comparisonSessionURL(sessionID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// 12. Success → 204.
func TestDeleteComparisonSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("DeleteComparisonSession", mock.Anything, repository.DeleteComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(int64(1), nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		comparisonSessionURL(sessionID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mq.AssertExpectations(t)
}

// ── AddDocumentToSession ──────────────────────────────────────────────────────

// 13. No auth → 401.
func TestAddDocumentToSession_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	url := comparisonSessionURL(uuid.New()) + "/documents"
	body, _ := json.Marshal(map[string]any{"document_id": uuid.New().String()})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// 14. Session not found for tenant → 404.
func TestAddDocumentToSession_SessionNotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("GetComparisonSession", mock.Anything, repository.GetComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(repository.ComparisonSession{}, pgx.ErrNoRows)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	url := comparisonSessionURL(sessionID) + "/documents"
	body, _ := json.Marshal(map[string]any{"document_id": uuid.New().String()})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// 15. Success → 201 with document entry.
func TestAddDocumentToSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	documentID := uuid.New()
	mq := new(storemock.MockQuerier)

	sess := repository.ComparisonSession{ID: sessionID, OrganizationID: orgID, CreatedAt: time.Now()}
	mq.On("GetComparisonSession", mock.Anything, repository.GetComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(sess, nil)

	doc := repository.ComparisonSessionDocument{
		SessionID:      sessionID,
		DocumentID:     documentID,
		OrganizationID: orgID,
		Position:       0,
	}
	mq.On("AddDocumentToComparisonSession", mock.Anything,
		repository.AddDocumentToComparisonSessionParams{
			SessionID:      sessionID,
			DocumentID:     documentID,
			OrganizationID: orgID,
			Position:       0,
		}).Return(doc, nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	url := comparisonSessionURL(sessionID) + "/documents"
	body, _ := json.Marshal(map[string]any{"document_id": documentID.String()})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mq.AssertExpectations(t)
}

// ── RemoveDocumentFromSession ─────────────────────────────────────────────────

// 16. No auth → 401.
func TestRemoveDocumentFromSession_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockComparisonSessionService(t, mq)

	url := comparisonSessionURL(uuid.New()) + "/documents/" + uuid.New().String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// 17. Document not in session → 404.
func TestRemoveDocumentFromSession_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	documentID := uuid.New()
	mq := new(storemock.MockQuerier)

	sess := repository.ComparisonSession{ID: sessionID, OrganizationID: orgID, CreatedAt: time.Now()}
	mq.On("GetComparisonSession", mock.Anything, repository.GetComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(sess, nil)

	mq.On("RemoveDocumentFromComparisonSession", mock.Anything,
		repository.RemoveDocumentFromComparisonSessionParams{
			SessionID:      sessionID,
			DocumentID:     documentID,
			OrganizationID: orgID,
		}).Return(int64(0), nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	url := comparisonSessionURL(sessionID) + "/documents/" + documentID.String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// 18. Success → 204.
func TestRemoveDocumentFromSession_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	sessionID := uuid.New()
	documentID := uuid.New()
	mq := new(storemock.MockQuerier)

	sess := repository.ComparisonSession{ID: sessionID, OrganizationID: orgID, CreatedAt: time.Now()}
	mq.On("GetComparisonSession", mock.Anything, repository.GetComparisonSessionParams{
		ID:             sessionID,
		OrganizationID: orgID,
	}).Return(sess, nil)

	mq.On("RemoveDocumentFromComparisonSession", mock.Anything,
		repository.RemoveDocumentFromComparisonSessionParams{
			SessionID:      sessionID,
			DocumentID:     documentID,
			OrganizationID: orgID,
		}).Return(int64(1), nil)

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	url := comparisonSessionURL(sessionID) + "/documents/" + documentID.String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mq.AssertExpectations(t)
}

// ── DB error path ─────────────────────────────────────────────────────────────

// 19. ListComparisonSessions DB error → 500.
func TestListComparisonSessions_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("ListComparisonSessionsByOrg", mock.Anything, orgID).
		Return(nil, errors.New("db error"))

	s := newServerWithMockComparisonSessionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		comparisonSessionURL(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}
