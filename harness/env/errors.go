package env

import "fmt"

type FileErrorCode string

const (
	FileAborted          FileErrorCode = "aborted"
	FileNotFound         FileErrorCode = "not_found"
	FilePermissionDenied FileErrorCode = "permission_denied"
	FileNotDirectory     FileErrorCode = "not_directory"
	FileIsDirectory      FileErrorCode = "is_directory"
	FileInvalid          FileErrorCode = "invalid"
	FileNotSupported     FileErrorCode = "not_supported"
	FileUnknown          FileErrorCode = "unknown"
)

type FileError struct {
	Code FileErrorCode
	Msg  string
	Path string
	Err  error
}

func (e *FileError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s (%s): %s", e.Msg, e.Code, e.Path)
	}
	return fmt.Sprintf("%s (%s)", e.Msg, e.Code)
}

func (e *FileError) Unwrap() error { return e.Err }

type ExecutionErrorCode string

const (
	ExecAborted          ExecutionErrorCode = "aborted"
	ExecTimeout          ExecutionErrorCode = "timeout"
	ExecShellUnavailable ExecutionErrorCode = "shell_unavailable"
	ExecSpawnError       ExecutionErrorCode = "spawn_error"
	ExecCallbackError    ExecutionErrorCode = "callback_error"
	ExecUnknown          ExecutionErrorCode = "unknown"
)

type ExecutionError struct {
	Code ExecutionErrorCode
	Msg  string
	Err  error
}

func (e *ExecutionError) Error() string { return fmt.Sprintf("%s (%s)", e.Msg, e.Code) }
func (e *ExecutionError) Unwrap() error { return e.Err }
