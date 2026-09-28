package apperr

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Code string

const (
	CodeInvalid      Code = "invalid"
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeUnauthorized Code = "unauthorized"
	CodeForbidden    Code = "forbidden"
	CodeRateLimit    Code = "rate_limit"
	CodeInternal     Code = "internal"
)

type Error struct {
	Code Code
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func New(code Code, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

func Invalid(msg string) *Error      { return New(CodeInvalid, msg) }
func NotFound(msg string) *Error     { return New(CodeNotFound, msg) }
func Conflict(msg string) *Error     { return New(CodeConflict, msg) }
func Unauthorized(msg string) *Error { return New(CodeUnauthorized, msg) }
func Forbidden(msg string) *Error    { return New(CodeForbidden, msg) }
func RateLimited(msg string) *Error  { return New(CodeRateLimit, msg) }

func Wrap(code Code, err error, msg string) *Error {
	return &Error{Code: code, Msg: msg, Err: err}
}

func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

func GRPCStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok && !isApp(err) {
		return err
	}
	e, ok := As(err)
	if !ok {
		return status.Error(codes.Internal, "internal error")
	}
	return status.Error(toCode(e.Code), e.Msg)
}

func isApp(err error) bool {
	_, ok := As(err)
	return ok
}

func toCode(c Code) codes.Code {
	switch c {
	case CodeInvalid:
		return codes.InvalidArgument
	case CodeNotFound:
		return codes.NotFound
	case CodeConflict:
		return codes.AlreadyExists
	case CodeUnauthorized:
		return codes.Unauthenticated
	case CodeForbidden:
		return codes.PermissionDenied
	case CodeRateLimit:
		return codes.ResourceExhausted
	default:
		return codes.Internal
	}
}
