package apperr

import "errors"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string                    { return e.Code + ": " + e.Message }
func New(status int, code, message string) *Error { return &Error{code, message, status} }
func Validation(message string) *Error            { return New(400, "VALIDATION_ERROR", message) }
func NotFound() *Error                            { return New(404, "NOT_FOUND", "Resource not found") }
func Forbidden() *Error                           { return New(403, "FORBIDDEN", "You do not have access to this operation") }
func Conflict(code, message string) *Error        { return New(409, code, message) }
func Public(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(500, "INTERNAL_ERROR", "An internal error occurred")
}
