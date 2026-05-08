package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
)

func newServerWithMockExtractionKeyService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.extractionKeyService = service.NewExtractionKeyService(mq, s.log)
	return s
}

func extractionKeyURL(id ...uuid.UUID) string {
	base := "/api/v1/extraction-keys"
	if len(id) > 0 {
		return base + "/" + id[0].String()
	}
	return base
}

func sampleKey(_ uuid.UUID) repository.ExtractionKey {
	return repository.ExtractionKey{
		ID:          uuid.New(),
		KeyName:     "total_price",
		SourceQuery: "What is the total contract price?",
		DataType:    "number",
		IsActive:    true,
		CreatedAt:   time.Now(),
	}
}

// ── ListExtractionKeys ────────────────────────────────────────────────────────

func TestListExtractionKeys_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, extractionKeyURL(), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "ListExtractionKeysByOrg")
}

func TestListExtractionKeys_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	keys := []repository.ExtractionKey{sampleKey(orgID)}
	mq.On("ListExtractionKeysByOrg", mock.Anything, orgID).Return(keys, nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, extractionKeyURL(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// ── CreateExtractionKey ───────────────────────────────────────────────────────

func TestCreateExtractionKey_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	body, _ := json.Marshal(map[string]any{
		"key_name": "price", "source_query": "price?", "data_type": "number",
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		extractionKeyURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateExtractionKey_MissingFields_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	// missing source_query and data_type
	body, _ := json.Marshal(map[string]any{"key_name": "price"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		extractionKeyURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "CreateExtractionKey")
}

func TestCreateExtractionKey_InvalidDataType_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{
		"key_name": "price", "source_query": "price?", "data_type": "float",
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		extractionKeyURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "CreateExtractionKey")
}

func TestCreateExtractionKey_Conflict_Returns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "uq_extraction_keys_org_name"}
	mq.On("CreateExtractionKey", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgErr)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{
		"key_name": "price", "source_query": "price?", "data_type": "number",
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		extractionKeyURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mq.AssertExpectations(t)
}

func TestCreateExtractionKey_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	key := sampleKey(orgID)
	mq.On("CreateExtractionKey", mock.Anything, mock.Anything).Return(key, nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{
		"key_name": "total_price", "source_query": "What is the total contract price?", "data_type": "number",
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		extractionKeyURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mq.AssertExpectations(t)
}

// ── GetExtractionKey ──────────────────────────────────────────────────────────

func TestGetExtractionKey_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionKeyURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetExtractionKey_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/extraction-keys/not-a-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetExtractionKey_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("GetExtractionKey", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionKeyURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestGetExtractionKey_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	keyID := uuid.New()
	mq := new(storemock.MockQuerier)

	key := sampleKey(orgID)
	key.ID = keyID
	mq.On("GetExtractionKey", mock.Anything, mock.Anything).Return(key, nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionKeyURL(keyID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// ── UpdateExtractionKey ───────────────────────────────────────────────────────

func TestUpdateExtractionKey_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	body, _ := json.Marshal(map[string]any{"data_type": "string"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		extractionKeyURL(uuid.New()), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUpdateExtractionKey_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	// 0 rows updated → service returns not_found
	mq.On("UpdateExtractionKey", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"data_type": "string"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		extractionKeyURL(uuid.New()), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestUpdateExtractionKey_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	keyID := uuid.New()
	mq := new(storemock.MockQuerier)

	updated := sampleKey(orgID)
	updated.ID = keyID
	updated.DataType = "string"
	mq.On("UpdateExtractionKey", mock.Anything, mock.Anything).Return(updated, nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"data_type": "string"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		extractionKeyURL(keyID), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// ── DeleteExtractionKey ───────────────────────────────────────────────────────

func TestDeleteExtractionKey_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionKeyService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		extractionKeyURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDeleteExtractionKey_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("DeleteExtractionKey", mock.Anything, mock.Anything).Return(int64(0), nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		extractionKeyURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestDeleteExtractionKey_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("DeleteExtractionKey", mock.Anything, mock.Anything).Return(int64(1), nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		extractionKeyURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mq.AssertExpectations(t)
}
