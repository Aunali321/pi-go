package session

import (
	"fmt"

	"github.com/aunali321/pi-go/harness/env"
)

type SessionErrorCode string

const (
	SessionNotFound          SessionErrorCode = "not_found"
	SessionInvalid           SessionErrorCode = "invalid_session"
	SessionInvalidEntry      SessionErrorCode = "invalid_entry"
	SessionInvalidForkTarget SessionErrorCode = "invalid_fork_target"
	SessionStorageErr        SessionErrorCode = "storage"
	SessionUnknown           SessionErrorCode = "unknown"
)

type SessionError struct {
	Code SessionErrorCode
	Msg  string
	Err  error
}

func (e *SessionError) Error() string { return fmt.Sprintf("%s (%s)", e.Msg, e.Code) }
func (e *SessionError) Unwrap() error { return e.Err }

func newSessionError(code SessionErrorCode, msg string, cause error) *SessionError {
	return &SessionError{Code: code, Msg: msg, Err: cause}
}

// fileSystemResultOrThrow maps an env.FileError to a SessionError, preserving not_found.
func fileSystemResultOrThrow[T any](value T, err error, msg string) (T, error) {
	if err != nil {
		code := SessionStorageErr
		if fe, ok := err.(*env.FileError); ok && fe.Code == env.FileNotFound {
			code = SessionNotFound
		}
		return value, newSessionError(code, fmt.Sprintf("%s: %s", msg, err.Error()), err)
	}
	return value, nil
}

func toError(v any) error {
	switch e := v.(type) {
	case nil:
		return nil
	case error:
		return e
	case string:
		return fmt.Errorf("%s", e)
	default:
		return fmt.Errorf("%v", v)
	}
}
