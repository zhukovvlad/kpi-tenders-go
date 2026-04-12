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

func newTestServer() *Server {
	cfg := &config.Config{AppEnv: "local", AppPort: "8080"}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewServer(cfg, log, nil)
}

func TestHealthCheck(t *testing.T) {
	s := newTestServer()

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
	s := newTestServer()

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestProtectedRouteRequiresAuth(t *testing.T) {
	s := newTestServer()

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/documents", nil)
	w := httptest.NewRecorder()

	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
