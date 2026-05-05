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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"

	"github.com/jackc/pgx/v5/pgconn"
)

func newServerWithMockInvitationService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.invitationService = service.NewInvitationService(mq, s.log)
	return s
}

func invitationURL(id ...uuid.UUID) string {
	base := "/api/v1/invitations"
	if len(id) > 0 {
		return base + "/" + id[0].String()
	}
	return base
}

func fakeInvitation(orgID uuid.UUID) repository.UserInvitation {
	return repository.UserInvitation{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Email:          "test@example.com",
		Role:           "member",
		ExpiresAt:      time.Now().Add(72 * time.Hour),
		CreatedAt:      time.Now(),
	}
}

// ── CreateInvitation ──────────────────────────────────────────────────────────

// 1. No auth → 401.
func TestCreateInvitation_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockInvitationService(t, mq)

	body, _ := json.Marshal(map[string]any{"email": "a@b.com"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "CreateUserInvitation")
}

// 2. Non-admin role → 403 (AdminOnly middleware).
func TestCreateInvitation_NonAdmin_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockInvitationService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"email": "a@b.com"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mq.AssertNotCalled(t, "CreateUserInvitation")
}

// 3. Invalid email → 400 from binding.
func TestCreateInvitation_InvalidEmail_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockInvitationService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"email": "not-an-email"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "CreateUserInvitation")
}

// 4. Invalid role → 400 from handler validation.
func TestCreateInvitation_InvalidRole_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockInvitationService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"email": "a@b.com", "role": "superuser"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "CreateUserInvitation")
}

// 5. Duplicate invitation → 409.
func TestCreateInvitation_Conflict_Returns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "uq_user_invitations_org_email_active"}
	mq.On("CreateUserInvitation", mock.Anything, mock.Anything).
		Return(repository.UserInvitation{}, pgErr)

	s := newServerWithMockInvitationService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"email": "a@b.com"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mq.AssertExpectations(t)
}

// 6. Success in local env → response includes "token" field.
func TestCreateInvitation_LocalEnv_IncludesToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	inv := fakeInvitation(orgID)
	mq.On("CreateUserInvitation", mock.Anything, mock.Anything).Return(inv, nil)

	s := newServerWithMockInvitationService(t, mq)
	// cfg.AppEnv is already "local" (set by newTestServerWithJWT)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"email": "a@b.com"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "token", "local env must include raw token in response")
	assert.Contains(t, resp, "invitation")
	mq.AssertExpectations(t)
}

// 7. Success in non-local env → response must NOT include "token" field.
func TestCreateInvitation_NonLocalEnv_OmitsToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	inv := fakeInvitation(orgID)
	mq.On("CreateUserInvitation", mock.Anything, mock.Anything).Return(inv, nil)

	s := newServerWithMockInvitationService(t, mq)
	s.cfg.AppEnv = "production"
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "admin")
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"email": "a@b.com"})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, invitationURL(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotContains(t, resp, "token", "non-local env must NOT expose raw token")
	assert.Contains(t, resp, "invitation")
	mq.AssertExpectations(t)
}
