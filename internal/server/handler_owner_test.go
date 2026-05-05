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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
)

// newServerWithMockOwnerUserService creates a JWT-capable server with userService
// backed by the supplied MockQuerier.
func newServerWithMockOwnerUserService(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.userService = service.NewUserService(mq, s.log)
	return s
}

// ── GET /api/v1/organizations/:id/users ──────────────────────────────────────

func TestOwnerListOrganizationUsers_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT(t)

	orgID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/organizations/"+orgID.String()+"/users", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOwnerListOrganizationUsers_NonOwner_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT(t)

	// admin token — not owner
	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "admin")
	require.NoError(t, err)

	orgID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/organizations/"+orgID.String()+"/users", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestOwnerListOrganizationUsers_InvalidOrgID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockOwnerUserService(t, mq)

	tok := ownerToken(t, s)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/organizations/not-a-uuid/users", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerListOrganizationUsers_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	orgID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	mq := new(storemock.MockQuerier)
	mq.On("ListUsersByOrganization", mock.Anything, pgtype.UUID{Bytes: orgID, Valid: true}).
		Return([]repository.ListUsersByOrganizationRow{
			{
				ID:             userID,
				OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
				Email:          "user@acme.com",
				FullName:       "Jane Doe",
				Role:           "admin",
				IsActive:       true,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		}, nil)

	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/organizations/"+orgID.String()+"/users", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, userID.String(), body[0]["id"])
	mq.AssertExpectations(t)
}

func TestOwnerListOrganizationUsers_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	mq.On("ListUsersByOrganization", mock.Anything, pgtype.UUID{Bytes: orgID, Valid: true}).
		Return(nil, errors.New("db unavailable"))

	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/organizations/"+orgID.String()+"/users", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}

// ── PATCH /api/v1/organizations/:id/users/:user_id ───────────────────────────

func TestOwnerUpdateOrganizationUser_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT(t)

	orgID := uuid.New()
	userID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOwnerUpdateOrganizationUser_NonOwner_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT(t)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "admin")
	require.NoError(t, err)

	orgID := uuid.New()
	userID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestOwnerUpdateOrganizationUser_InvalidOrgID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	userID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/bad-uuid/users/"+userID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerUpdateOrganizationUser_InvalidUserID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	orgID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/"+orgID.String()+"/users/bad-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerUpdateOrganizationUser_NothingToUpdate_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	orgID := uuid.New()
	userID := uuid.New()
	body, _ := json.Marshal(map[string]any{}) // no role, no is_active
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerUpdateOrganizationUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	orgID := uuid.New()
	userID := uuid.New()
	now := time.Now()
	role := "member"

	mq := new(storemock.MockQuerier)
	mq.On("UpdateUser", mock.Anything, repository.UpdateUserParams{
		ID:             userID,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		Role:           pgtype.Text{String: role, Valid: true},
		IsActive:       pgtype.Bool{},
	}).Return(repository.UpdateUserRow{
		ID:             userID,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		Email:          "user@acme.com",
		FullName:       "Jane Doe",
		Role:           role,
		IsActive:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil)

	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	body, _ := json.Marshal(map[string]any{"role": role})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, userID.String(), resp["id"])
	assert.Equal(t, role, resp["role"])
	mq.AssertExpectations(t)
}

func TestOwnerUpdateOrganizationUser_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	orgID := uuid.New()
	userID := uuid.New()
	active := false

	mq := new(storemock.MockQuerier)
	mq.On("UpdateUser", mock.Anything, repository.UpdateUserParams{
		ID:             userID,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		Role:           pgtype.Text{},
		IsActive:       pgtype.Bool{Bool: active, Valid: true},
	}).Return(repository.UpdateUserRow{}, pgx.ErrNoRows)

	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	body, _ := json.Marshal(map[string]any{"is_active": active})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(),
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

// ── DELETE /api/v1/organizations/:id/users/:user_id ──────────────────────────

func TestOwnerDeactivateOrganizationUser_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT(t)

	orgID := uuid.New()
	userID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOwnerDeactivateOrganizationUser_NonOwner_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT(t)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "admin")
	require.NoError(t, err)

	orgID := uuid.New()
	userID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestOwnerDeactivateOrganizationUser_InvalidOrgID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	userID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/organizations/bad-uuid/users/"+userID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerDeactivateOrganizationUser_InvalidUserID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	orgID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/organizations/"+orgID.String()+"/users/bad-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerDeactivateOrganizationUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	orgID := uuid.New()
	userID := uuid.New()
	now := time.Now()
	active := false

	mq := new(storemock.MockQuerier)
	mq.On("UpdateUser", mock.Anything, repository.UpdateUserParams{
		ID:             userID,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		Role:           pgtype.Text{},
		IsActive:       pgtype.Bool{Bool: active, Valid: true},
	}).Return(repository.UpdateUserRow{
		ID:             userID,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		Email:          "user@acme.com",
		FullName:       "Jane Doe",
		Role:           "member",
		IsActive:       active,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil)

	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mq.AssertExpectations(t)
}

func TestOwnerDeactivateOrganizationUser_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	orgID := uuid.New()
	userID := uuid.New()
	active := false

	mq := new(storemock.MockQuerier)
	mq.On("UpdateUser", mock.Anything, repository.UpdateUserParams{
		ID:             userID,
		OrganizationID: pgtype.UUID{Bytes: orgID, Valid: true},
		Role:           pgtype.Text{},
		IsActive:       pgtype.Bool{Bool: active, Valid: true},
	}).Return(repository.UpdateUserRow{}, pgx.ErrNoRows)

	s := newServerWithMockOwnerUserService(t, mq)
	tok := ownerToken(t, s)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/organizations/"+orgID.String()+"/users/"+userID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}
