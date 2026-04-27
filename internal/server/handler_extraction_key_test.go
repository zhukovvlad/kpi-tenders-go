package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
)

// newServerWithMockExtractionKeyService creates a JWT-capable server whose
// extractionKeyService is backed by the supplied MockQuerier.
func newServerWithMockExtractionKeyService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.extractionKeyService = service.NewExtractionKeyService(mq, s.log)
	return s
}

func TestResolveExtractionKey_SuccessCreated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()
	keyID := uuid.New()
	expected := repository.ExtractionKey{
		ID:             keyID,
		OrganizationID: orgID,
		KeyName:        "kakoy_protsent_avansa",
		SourceQuery:    "Какой процент аванса?",
		Description:    pgtype.Text{String: "Какой процент аванса?", Valid: true},
		DataType:       "number",
	}

	mq := new(storemock.MockQuerier)
	mq.On("GetExtractionKeyByOrgAndSourceQuery", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)
	mq.On("GetExtractionKeyByOrgAndKeyName", mock.Anything, mock.Anything).
		Return(repository.ExtractionKey{}, pgx.ErrNoRows)
	mq.On("CreateExtractionKey", mock.Anything, mock.Anything).Return(expected, nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/extraction-keys/resolve", strings.NewReader(`{"source_query":"Какой процент аванса?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, false, body["duplicate"])
	key := body["key"].(map[string]any)
	assert.Equal(t, keyID.String(), key["id"])
	assert.Equal(t, "kakoy_protsent_avansa", key["key_name"])
	mq.AssertExpectations(t)
}

func TestResolveExtractionKey_DuplicateReturnsOK(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()
	keyID := uuid.New()
	expected := repository.ExtractionKey{
		ID:             keyID,
		OrganizationID: orgID,
		KeyName:        "advance_payment_percent",
		SourceQuery:    "Какой процент аванса?",
		DataType:       "number",
	}

	mq := new(storemock.MockQuerier)
	mq.On("GetExtractionKeyByOrgAndSourceQuery", mock.Anything, mock.Anything).Return(expected, nil)

	s := newServerWithMockExtractionKeyService(t, mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/extraction-keys/resolve", strings.NewReader(`{"source_query":"Какой процент аванса?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["duplicate"])
	key := body["key"].(map[string]any)
	assert.Equal(t, keyID.String(), key["id"])
	assert.Equal(t, "advance_payment_percent", key["key_name"])
	mq.AssertExpectations(t)
}

func TestResolveExtractionKey_InvalidRequestReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()
	s := newServerWithMockExtractionKeyService(t, new(storemock.MockQuerier))
	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/extraction-keys/resolve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResolveExtractionKey_NoAuthReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockExtractionKeyService(t, new(storemock.MockQuerier))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/extraction-keys/resolve", strings.NewReader(`{"source_query":"Какой процент аванса?"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
