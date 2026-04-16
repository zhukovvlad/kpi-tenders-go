package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/pkg/errs"
)

// codeToStatus maps application error codes to their canonical HTTP status.
var codeToStatus = map[errs.Code]int{
	errs.CodeNotFound:         http.StatusNotFound,
	errs.CodeConflict:         http.StatusConflict,
	errs.CodeValidationFailed: http.StatusBadRequest,
	errs.CodeUnauthorized:     http.StatusUnauthorized,
	errs.CodeForbidden:        http.StatusForbidden,
	errs.CodeInternalError:    http.StatusInternalServerError,
	errs.CodeNotImplemented:   http.StatusNotImplemented,
}

// errorResponse is the envelope sent in every error response.
type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    errs.Code `json:"code"`
	Message string    `json:"message"`
}

// respondWithError writes a structured JSON error response and logs the event.
//
//   - If err wraps an *errs.Error the code and message come from it and the
//     HTTP status is chosen from codeToStatus (fallback: 500).
//     Non-internal errors are logged at DEBUG level; internal errors at ERROR.
//   - Any other error type produces a 500 with code "internal_error" and is
//     logged at ERROR with only the concrete Go type (%T); the error text is
//     intentionally omitted to avoid PII leakage from raw repository errors.
func (s *Server) respondWithError(c *gin.Context, err error) {
	var appErr *errs.Error
	if errors.As(err, &appErr) {
		status, ok := codeToStatus[appErr.Code]
		if !ok {
			// Unknown code — never expose non-canonical codes to the client (#3).
			// Log only code + safe message to avoid PII from the wrapped cause.
			s.log.Error("request error: unknown code",
				slog.String("code", string(appErr.Code)),
				slog.String("message", appErr.Message),
			)
			c.JSON(http.StatusInternalServerError, errorResponse{
				Error: errorBody{Code: errs.CodeInternalError, Message: "internal server error"},
			})
			return
		}

		// Only log the underlying cause for internal errors to avoid leaking
		// PII that may be present in DB error details (e.g. unique-violation
		// messages contain the conflicting value) (#4).
		if appErr.Code == errs.CodeInternalError {
			attrs := []any{
				slog.String("code", string(appErr.Code)),
				slog.String("message", appErr.Message),
			}
			if appErr.Err != nil {
				attrs = append(attrs, slog.String("cause", appErr.Err.Error()))
			}
			s.log.Error("request error", attrs...)
		} else {
			s.log.Debug("request error",
				slog.String("code", string(appErr.Code)),
				slog.String("message", appErr.Message),
			)
		}

		c.JSON(status, errorResponse{Error: errorBody{Code: appErr.Code, Message: appErr.Message}})
		return
	}

	// Unrecognised error — never expose internal details to the caller.
	// Log only the type to avoid leaking PII that raw repository errors may carry.
	s.log.Error("request error: unhandled", slog.String("type", fmt.Sprintf("%T", err)))
	c.JSON(http.StatusInternalServerError, errorResponse{
		Error: errorBody{Code: errs.CodeInternalError, Message: "internal server error"},
	})
}
