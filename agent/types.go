package agent

import (
	"context"

	"github.com/aunali321/pi-go/llm"
)

// AgentMessage is any message the agent transcript can hold: the three LLM
// message types plus application-defined custom messages. Implementations must
// expose a role via Role(); custom messages are reduced to LLM messages by a
// ConvertToLLM function before each request.
type AgentMessage interface {
	Role() string
}

// Context is the agent's working state for a run.
type Context struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []Tool
}

// StreamFunc produces an assistant response stream. Defaults to llm.StreamSimple.
type StreamFunc func(ctx context.Context, model *llm.Model, reqCtx *llm.Context, opts *llm.StreamOptions) *llm.Stream

// Config controls a single agent run.
type Config struct {
	Model     *llm.Model
	Reasoning llm.ThinkingLevel
	Options   llm.StreamOptions

	Stream StreamFunc

	ConvertToLLM     func([]AgentMessage) []llm.Message
	TransformContext func(ctx context.Context, msgs []AgentMessage) []AgentMessage
	APIKey           func(provider string) string

	ToolExecution ExecutionMode

	BeforeToolCall      func(ctx context.Context, c BeforeToolCall) BeforeToolResult
	AfterToolCall       func(ctx context.Context, c AfterToolCall) *AfterToolResult
	ShouldStopAfterTurn func(c TurnInfo) bool
	PrepareNextTurn     func(c TurnInfo) *TurnUpdate
	GetSteeringMessages func() []AgentMessage
	GetFollowUpMessages func() []AgentMessage
}

type BeforeToolResult struct {
	Block  bool
	Reason string
}

// AfterToolResult overrides parts of an executed tool result. Nil fields keep
// the original value. No deep merge is performed.
type AfterToolResult struct {
	Content   []llm.Content
	Details   any
	IsError   *bool
	Usage     *llm.Usage
	Terminate *bool
}

type BeforeToolCall struct {
	Assistant *llm.AssistantMessage
	ToolCall  *llm.ToolCall
	Args      map[string]any
	Context   *Context
}

type AfterToolCall struct {
	Assistant *llm.AssistantMessage
	ToolCall  *llm.ToolCall
	Args      map[string]any
	Result    ToolResult
	IsError   bool
	Context   *Context
}

type TurnInfo struct {
	Message     *llm.AssistantMessage
	ToolResults []*llm.ToolResultMessage
	Context     *Context
	NewMessages []AgentMessage
}

// TurnUpdate replaces runtime state before the next request.
type TurnUpdate struct {
	Context   *Context
	Model     *llm.Model
	Reasoning *llm.ThinkingLevel
}

// EventSink receives agent events.
type EventSink func(Event)

// Event is one of the agent lifecycle events.
type Event interface{ isAgentEvent() }

type AgentStart struct{}
type AgentEnd struct{ Messages []AgentMessage }
type TurnStart struct{}
type TurnEnd struct {
	Message     AgentMessage
	ToolResults []*llm.ToolResultMessage
}
type MessageStart struct{ Message AgentMessage }
type MessageUpdate struct {
	Message AgentMessage
	Event   llm.StreamEvent
}
type MessageEnd struct{ Message AgentMessage }
type ToolExecutionStart struct {
	ToolCallID string
	ToolName   string
	Args       map[string]any
}
type ToolExecutionUpdate struct {
	ToolCallID    string
	ToolName      string
	Args          map[string]any
	PartialResult ToolResult
}
type ToolExecutionEnd struct {
	ToolCallID string
	ToolName   string
	Result     ToolResult
	IsError    bool
}

func (AgentStart) isAgentEvent()          {}
func (AgentEnd) isAgentEvent()            {}
func (TurnStart) isAgentEvent()           {}
func (TurnEnd) isAgentEvent()             {}
func (MessageStart) isAgentEvent()        {}
func (MessageUpdate) isAgentEvent()       {}
func (MessageEnd) isAgentEvent()          {}
func (ToolExecutionStart) isAgentEvent()  {}
func (ToolExecutionUpdate) isAgentEvent() {}
func (ToolExecutionEnd) isAgentEvent()    {}

func defaultConvertToLLM(msgs []AgentMessage) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if lm, ok := m.(llm.Message); ok {
			out = append(out, lm)
		}
	}
	return out
}
