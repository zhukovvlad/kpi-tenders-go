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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
)

func newServerWithMockContractKindService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.contractKindService = service.NewContractKindService(mq, s.log)
	return s
}

func contractKindURL(id ...uuid.UUID) string {
	base := "/api/v1/contract-kinds"
	if len(id) > 0 {
		return base + "/" + id[0].String()
	}
	return base
}

// ── ListContractKinds ─────────────────────────────────────────────────────────

func TestListContractKinds_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contractKindURL(), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "ListContractKindsByOrg")
}

func TestListContractKinds_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	kinds := []repository.DocumentContractKind{{ID: uuid.New(), DisplayName: "Генподряд"}}
	mq.On("ListContractKindsByOrg", mock.Anything, mock.Anything).Return(kinds, nil)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contractKindURL(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// ── CreateContractKind ────────────────────────────────────────────────────────

func TestCreateContractKind_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	body, _ := json.Marshal(map[string]any{"display_name": "Генподряд"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, contractKindURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateContractKind_MissingDisplayName_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, contractKindURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "CreateContractKind")
}

func TestCreateContractKind_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	kind := repository.DocumentContractKind{ID: uuid.New(), DisplayName: "Генподряд", IsActive: true}
	mq.On("CreateContractKind", mock.Anything, mock.Anything).Return(kind, nil)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"display_name": "Генподряд"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, contractKindURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mq.AssertExpectations(t)
}

func TestCreateContractKind_Conflict_Returns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "uq_contract_kinds_org_name"}
	mq.On("CreateContractKind", mock.Anything, mock.Anything).
		Return(repository.DocumentContractKind{}, pgErr)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"display_name": "Генподряд"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, contractKindURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mq.AssertExpectations(t)
}

// ── GetContractKind ───────────────────────────────────────────────────────────

func TestGetContractKind_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contractKindURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetContractKind_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/contract-kinds/not-a-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetContractKind_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("GetContractKind", mock.Anything, mock.Anything).Return(repository.DocumentContractKind{}, pgx.ErrNoRows)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contractKindURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestGetContractKind_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	kindID := uuid.New()
	mq := new(storemock.MockQuerier)

	kind := repository.DocumentContractKind{ID: kindID, DisplayName: "Генподряд"}
	mq.On("GetContractKind", mock.Anything, mock.Anything).Return(kind, nil)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, contractKindURL(kindID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mq.AssertExpectations(t)
}

// ── DeleteContractKind ────────────────────────────────────────────────────────

func TestDeleteContractKind_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, contractKindURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDeleteContractKind_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("DeleteContractKind", mock.Anything, mock.Anything).Return(int64(0), nil)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, contractKindURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestDeleteContractKind_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("DeleteContractKind", mock.Anything, mock.Anything).Return(int64(1), nil)

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, contractKindURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mq.AssertExpectations(t)
}

// ── UpdateContractKind ────────────────────────────────────────────────────────

func TestUpdateContractKind_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	body, _ := json.Marshal(map[string]any{"display_name": "X", "sort_order": 0, "is_active": true})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, contractKindURL(uuid.New()), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUpdateContractKind_MissingSortOrder_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockContractKindService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	// sort_order omitted → 400
	body, _ := json.Marshal(map[string]any{"display_name": "X", "is_active": true})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, contractKindURL(uuid.New()), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "UpdateContractKind")
}

func TestUpdateContractKind_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("UpdateContractKind", mock.Anything, mock.Anything).Return(repository.DocumentContractKind{}, errors.New("db error"))

	s := newServerWithMockContractKindService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	sortOrder := int16(0)
	isActive := true
	body, _ := json.Marshal(map[string]any{"display_name": "X", "sort_order": sortOrder, "is_active": isActive})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, contractKindURL(uuid.New()), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}
