package harness

import "fmt"

type AgentHarnessErrorCode string

const (
	HarnessBusy          AgentHarnessErrorCode = "busy"
	HarnessInvalidState  AgentHarnessErrorCode = "invalid_state"
	HarnessInvalidArg    AgentHarnessErrorCode = "invalid_argument"
	HarnessSession       AgentHarnessErrorCode = "session"
	HarnessHook          AgentHarnessErrorCode = "hook"
	HarnessAuth          AgentHarnessErrorCode = "auth"
	HarnessCompaction    AgentHarnessErrorCode = "compaction"
	HarnessBranchSummary AgentHarnessErrorCode = "branch_summary"
	HarnessUnknown       AgentHarnessErrorCode = "unknown"
)

type AgentHarnessError struct {
	Code AgentHarnessErrorCode
	Msg  string
	Err  error
}

func (e *AgentHarnessError) Error() string { return fmt.Sprintf("%s (%s)", e.Msg, e.Code) }
func (e *AgentHarnessError) Unwrap() error { return e.Err }
