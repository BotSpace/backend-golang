package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// AppError is the domain-level error type. Handlers convert it to HTTP responses.
type AppError struct {
	Code    string // stable machine-readable code, e.g. "user.not_found"
	Message string // human-readable message safe to expose
	Status  int    // HTTP status code
	Err     error  // wrapped internal error (not exposed)
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Err }

// As-friendly helper
func AsAppError(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

func NotFound(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusNotFound}
}

func BadRequest(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusBadRequest}
}

func Unauthorized(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusUnauthorized}
}

func Forbidden(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusForbidden}
}

func Conflict(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusConflict}
}

func TooManyRequests(code, msg string) *AppError {
	return &AppError{Code: code, Message: msg, Status: http.StatusTooManyRequests}
}

func Internal(err error) *AppError {
	return &AppError{
		Code:    "internal.error",
		Message: "internal server error",
		Status:  http.StatusInternalServerError,
		Err:     err,
	}
}

// Wrap attaches an underlying error to an AppError, returning a copy so a
// shared/package-level AppError value is never mutated.
func Wrap(ae *AppError, err error) *AppError {
	if ae == nil {
		return Internal(err)
	}
	cp := *ae
	cp.Err = err
	return &cp
}
