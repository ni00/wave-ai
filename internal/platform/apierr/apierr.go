// Package apierr defines the public JSON error envelope used by API handlers:
// {"type":"error","error":{"type","message"},"request_id":"req_..."}.
package apierr

import (
	"fmt"
	"net/http"

	"wave-ai.local/wave/internal/platform/xid"
)

// ErrorType enumerates the public error.type values from the API reference.
type ErrorType string

const (
	InvalidRequest  ErrorType = "invalid_request_error"
	Authentication  ErrorType = "authentication_error"
	Permission      ErrorType = "permission_error"
	NotFound        ErrorType = "not_found_error"
	RequestTooLarge ErrorType = "request_too_large"
	RateLimit       ErrorType = "rate_limit_error"
	API             ErrorType = "api_error"
	Timeout         ErrorType = "timeout_error"
	Overloaded      ErrorType = "overloaded_error"
)

// Error is a domain error that the HTTP layer maps onto the public envelope.
type Error struct {
	Status  int
	Type    ErrorType
	Message string
	// Internal, when set, is logged server-side but never sent to clients.
	Internal error
}

func (e *Error) Error() string {
	if e.Internal != nil {
		return fmt.Sprintf("%s: %s (internal: %v)", e.Type, e.Message, e.Internal)
	}
	return string(e.Type) + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Internal }

func New(status int, typ ErrorType, format string, args ...any) *Error {
	return &Error{Status: status, Type: typ, Message: fmt.Sprintf(format, args...)}
}

func Invalid(format string, args ...any) *Error {
	return New(http.StatusBadRequest, InvalidRequest, format, args...)
}

func Unauthorized(format string, args ...any) *Error {
	return New(http.StatusUnauthorized, Authentication, format, args...)
}

func Forbidden(format string, args ...any) *Error {
	return New(http.StatusForbidden, Permission, format, args...)
}

func NotFoundErr(format string, args ...any) *Error {
	return New(http.StatusNotFound, NotFound, format, args...)
}

func Conflict(format string, args ...any) *Error {
	return New(http.StatusConflict, InvalidRequest, format, args...)
}

func TooLarge(format string, args ...any) *Error {
	return New(http.StatusRequestEntityTooLarge, RequestTooLarge, format, args...)
}

func InternalErr(err error, format string, args ...any) *Error {
	e := New(http.StatusInternalServerError, API, format, args...)
	e.Internal = err
	return e
}

// Envelope is the wire form for JSON API errors. HTTP file-transfer errors and
// bodyless health/conditional responses are documented separately.
type Envelope struct {
	Type      string     `json:"type" validate:"required" enums:"error" example:"error"`
	Error     EnvelopeEr `json:"error" validate:"required"`
	RequestID string     `json:"request_id" validate:"required" example:"req_example"`
}

type EnvelopeEr struct {
	Type    string         `json:"type" validate:"required" enums:"invalid_request_error,authentication_error,permission_error,not_found_error,request_too_large,rate_limit_error,api_error,timeout_error,overloaded_error" example:"invalid_request_error"`
	Message string         `json:"message" validate:"required" example:"input is required"`
	Details map[string]any `json:"details,omitempty"`
}

// NewRequestID mints the req_-prefixed correlation identifier.
func NewRequestID() string { return xid.New("req") }
