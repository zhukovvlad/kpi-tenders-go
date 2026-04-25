package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/service"
)

const (
	testJWTAccessSecret  = "test-access-secret-must-be-32chars!"
	testJWTRefreshSecret = "test-refresh-secret-must-be-32char"
	testServiceToken     = "test-service-token-must-be-32chars!"
)

// newTestServerWithJWT creates a server with real JWT secrets but no DB
// connection — sufficient for middleware validation tests.
func newTestServerWithJWT() *Server {
	cfg := &config.Config{
		AppEnv:           "local",
		RedisURL:         "redis://localhost:6379/0",
		JWTAccessSecret:  testJWTAccessSecret,
		JWTRefreshSecret: testJWTRefreshSecret,
		ServiceToken:     testServiceToken,
	}
	return NewServer(cfg, slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})), nil)
}

// generateExpiredToken builds a syntactically valid but already-expired JWT.
func generateExpiredToken(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	claims := service.Claims{
		UserID: uuid.New(),
		OrgID:  uuid.New(),
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now.Add(-3 * time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	require.NoError(t, err)
	return tok
}

// ── AuthMiddleware ────────────────────────────────────────────────────────────

func TestAuthMiddleware_ValidToken_PassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	// Register a dedicated no-op route so the test only depends on middleware
	// behaviour and not on any business handler logic.
	s.Router().GET("/test/auth-ping", s.AuthMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	userID := uuid.New()
	orgID := uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/auth-ping", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_MissingToken_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_InvalidToken_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: "not.a.valid.jwt"})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ExpiredToken_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	expired := generateExpiredToken(t, testJWTAccessSecret)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: expired})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_WrongSigningKey_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	// Token signed with a different secret
	claims := service.Claims{
		UserID: uuid.New(), OrgID: uuid.New(), Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("completely-different-secret-here!"))
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── ServiceBearerAuth ─────────────────────────────────────────────────────────

// TestServiceBearerAuth_ValidToken_NotRejectedByMiddleware verifies that a
// valid SERVICE_TOKEN causes ServiceBearerAuth to let the request reach the
// handler. A dedicated no-op route is used so the test only depends on
// middleware behaviour and not on any business handler logic.
func TestServiceBearerAuth_ValidToken_NotRejectedByMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	// Register a no-op endpoint protected only by ServiceBearerAuth.
	s.Router().GET("/test/service-ping", s.ServiceBearerAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/service-ping", nil)
	req.Header.Set("Authorization", "Bearer "+testServiceToken)

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServiceBearerAuth_MissingHeader_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	taskID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/internal/worker/tasks/"+taskID.String()+"/status", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServiceBearerAuth_WrongToken_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	taskID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/internal/worker/tasks/"+taskID.String()+"/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestServiceBearerAuth_MalformedHeader_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithJWT()

	taskID := uuid.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/internal/worker/tasks/"+taskID.String()+"/status", nil)
	req.Header.Set("Authorization", "Token "+testServiceToken) // wrong scheme

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── AdminOnly ─────────────────────────────────────────────────────────────────

// newTestServerWithAdminRoute returns a server with a dedicated no-op route
// protected by AuthMiddleware + AdminOnly, so tests get a clean 200/403 signal.
func newTestServerWithAdminRoute() *Server {
	s := newTestServerWithJWT()
	s.Router().GET("/test/admin-ping", s.AuthMiddleware(), s.AdminOnly(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return s
}

// adminToken generates a valid access token with the given role.
func adminToken(t *testing.T, s *Server, role string) string {
	t.Helper()
	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), role)
	require.NoError(t, err)
	return access
}

func TestAdminOnly_AdminRole_PassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithAdminRoute()

	tok := adminToken(t, s, "admin")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/admin-ping", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAdminOnly_MemberRole_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithAdminRoute()

	tok := adminToken(t, s, "member")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/admin-ping", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminOnly_EmptyRole_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := newTestServerWithAdminRoute()

	tok := adminToken(t, s, "")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test/admin-ping", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: tok})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
