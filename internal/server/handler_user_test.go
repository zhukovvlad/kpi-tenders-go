package server

import (
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
	"go-kpi-tenders/pkg/errs"
)

// newServerWithMockUserService creates a JWT-capable server and replaces its
// userService with one backed by the supplied MockQuerier.
// The server's own logger (configured in newTestServerWithJWT) is reused so
// tests share a single log configuration.
func newServerWithMockUserService(mq *storemock.MockQuerier) *Server {
	s := newTestServerWithJWT()
	s.userService = service.NewUserService(mq, s.log)
	return s
}

// ── GET /api/v1/auth/me ───────────────────────────────────────────────────────

func TestGetMe_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetUserByIDAndOrg", mock.Anything, repository.GetUserByIDAndOrgParams{
		ID:             userID,
		OrganizationID: orgID,
	}).Return(repository.GetUserByIDAndOrgRow{
		ID:             userID,
		OrganizationID: orgID,
		Email:          "user@acme.com",
		FullName:       "John Doe",
		Role:           "member",
		IsActive:       true,
	}, nil)

	s := newServerWithMockUserService(mq)

	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, userID.String(), body["id"])
	assert.Equal(t, orgID.String(), body["organization_id"])
	assert.Equal(t, "user@acme.com", body["email"])
	assert.Equal(t, "John Doe", body["full_name"])
	assert.Equal(t, "member", body["role"])
	assert.Equal(t, true, body["is_active"])
	assert.NotContains(t, body, "password_hash")

	mq.AssertExpectations(t)
}

func TestGetMe_NoToken_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockUserService(new(storemock.MockQuerier))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/auth/me", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetMe_UserNotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetUserByIDAndOrg", mock.Anything, mock.Anything).
		Return(repository.GetUserByIDAndOrgRow{}, pgx.ErrNoRows)

	s := newServerWithMockUserService(mq)

	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "expected 'error' object in response body")
	assert.Equal(t, string(errs.CodeNotFound), errObj["code"])

	mq.AssertExpectations(t)
}

func TestGetMe_InactiveUser_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetUserByIDAndOrg", mock.Anything, mock.Anything).
		Return(repository.GetUserByIDAndOrgRow{ID: userID, IsActive: false}, nil)

	s := newServerWithMockUserService(mq)

	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "expected 'error' object in response body")
	assert.Equal(t, string(errs.CodeUnauthorized), errObj["code"])
	assert.Equal(t, "account is unavailable", errObj["message"])

	mq.AssertExpectations(t)
}

func TestGetMe_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetUserByIDAndOrg", mock.Anything, mock.Anything).
		Return(repository.GetUserByIDAndOrgRow{}, errors.New("connection reset"))

	s := newServerWithMockUserService(mq)

	access, _, err := s.authService.GenerateTokens(userID, orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "expected 'error' object in response body")
	assert.Equal(t, string(errs.CodeInternalError), errObj["code"])

	mq.AssertExpectations(t)
}
