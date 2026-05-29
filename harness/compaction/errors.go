package compaction

import "fmt"

type CompactionErrorCode string

const (
	CompactionAborted    CompactionErrorCode = "aborted"
	CompactionFailed     CompactionErrorCode = "summarization_failed"
	CompactionInvalid    CompactionErrorCode = "invalid_session"
	CompactionUnknownErr CompactionErrorCode = "unknown"
)

type CompactionError struct {
	Code CompactionErrorCode
	Msg  string
	Err  error
}

func (e *CompactionError) Error() string { return fmt.Sprintf("%s (%s)", e.Msg, e.Code) }
func (e *CompactionError) Unwrap() error { return e.Err }

type BranchSummaryErrorCode string

const (
	BranchSummaryAborted BranchSummaryErrorCode = "aborted"
	BranchSummaryFailed  BranchSummaryErrorCode = "summarization_failed"
	BranchSummaryInvalid BranchSummaryErrorCode = "invalid_session"
)

type BranchSummaryError struct {
	Code BranchSummaryErrorCode
	Msg  string
	Err  error
}

func (e *BranchSummaryError) Error() string { return fmt.Sprintf("%s (%s)", e.Msg, e.Code) }
func (e *BranchSummaryError) Unwrap() error { return e.Err }
