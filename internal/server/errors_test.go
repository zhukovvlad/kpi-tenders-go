package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/pkg/errs"
)

// respondWithErrorCase describes a single respondWithError scenario.
type respondWithErrorCase struct {
	name        string
	err         error
	wantStatus  int
	wantCode    errs.Code
	wantMessage string
}

func TestRespondWithError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []respondWithErrorCase{
		// ── Known *errs.Error codes ────────────────────────────────────────────
		{
			name:        "not_found maps to 404",
			err:         errs.New(errs.CodeNotFound, "document not found", nil),
			wantStatus:  http.StatusNotFound,
			wantCode:    errs.CodeNotFound,
			wantMessage: "document not found",
		},
		{
			name:        "conflict maps to 409",
			err:         errs.New(errs.CodeConflict, "already exists", nil),
			wantStatus:  http.StatusConflict,
			wantCode:    errs.CodeConflict,
			wantMessage: "already exists",
		},
		{
			name:        "validation_failed maps to 400",
			err:         errs.New(errs.CodeValidationFailed, "invalid input", nil),
			wantStatus:  http.StatusBadRequest,
			wantCode:    errs.CodeValidationFailed,
			wantMessage: "invalid input",
		},
		{
			name:        "unauthorized maps to 401",
			err:         errs.New(errs.CodeUnauthorized, "invalid token", nil),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    errs.CodeUnauthorized,
			wantMessage: "invalid token",
		},
		{
			name:        "forbidden maps to 403",
			err:         errs.New(errs.CodeForbidden, "access denied", nil),
			wantStatus:  http.StatusForbidden,
			wantCode:    errs.CodeForbidden,
			wantMessage: "access denied",
		},
		{
			name:        "internal_error maps to 500",
			err:         errs.New(errs.CodeInternalError, "internal server error", errors.New("db timeout")),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    errs.CodeInternalError,
			wantMessage: "internal server error",
		},
		// ── Fallback paths ────────────────────────────────────────────────────
		{
			name:        "unknown code normalised to internal_error",
			err:         errs.New(errs.Code("bogus_code"), "some message", nil),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    errs.CodeInternalError,
			wantMessage: "internal server error",
		},
		{
			name:        "non-errs.Error normalised to internal_error",
			err:         errors.New("raw repository error"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    errs.CodeInternalError,
			wantMessage: "internal server error",
		},
		// ── Envelope shape ────────────────────────────────────────────────────
		{
			name:        "wrapped *errs.Error is unwrapped via errors.As",
			err:         fmt.Errorf("service layer: %w", errs.New(errs.CodeNotFound, "org not found", nil)),
			wantStatus:  http.StatusNotFound,
			wantCode:    errs.CodeNotFound,
			wantMessage: "org not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			s.respondWithError(c, tc.err)

			assert.Equal(t, tc.wantStatus, w.Code)

			var body errorResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
			assert.Equal(t, tc.wantCode, body.Error.Code)
			assert.Equal(t, tc.wantMessage, body.Error.Message)
		})
	}
}
