package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"go-kpi-tenders/internal/config"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{AppEnv: "local", AppPort: "8080", RedisURL: "redis://localhost:6379/0"}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	s, err := NewServer(cfg, log, nil)
	if err != nil {
		panic(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHealthCheck(t *testing.T) {
	s := newTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &body)
	assert.NoError(t, err)
	assert.Equal(t, "ok", body["status"])
}

func TestHealthCheckV1(t *testing.T) {
	s := newTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProtectedRouteRequiresAuth(t *testing.T) {
	s := newTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/documents", nil)
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
