// Package errs defines structured application errors used across service and
// handler layers.  Service methods return *Error values; handlers translate
// them to HTTP responses without knowing any HTTP details themselves.
package errs

import "fmt"

// Code is a machine-readable error identifier sent to the frontend.
type Code string

const (
	CodeInternalError    Code = "internal_error"
	CodeNotFound         Code = "not_found"
	CodeConflict         Code = "conflict"
	CodeValidationFailed Code = "validation_failed"
	CodeUnauthorized     Code = "unauthorized"
	CodeForbidden        Code = "forbidden"
)

// Error is a structured application error.
//
//   - Code    — stable token for frontend switch/case logic.
//   - Message — human-readable text safe to expose to the caller.
//   - Err     — original cause kept for logging; intentionally omitted from
//     JSON serialisation via the `json:"-"` tag.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap lets errors.Is / errors.As traverse the cause chain.
func (e *Error) Unwrap() error {
	return e.Err
}

// New builds an Error. Pass err == nil when there is no underlying cause (e.g.
// a business-logic violation that needs no further context).
func New(code Code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}
