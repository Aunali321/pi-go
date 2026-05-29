package llm

// StreamEvent is a sealed interface for assistant-stream events. A Start is
// emitted first, then block start/delta/end events, terminating with one Done
// or Error. Partial returns the message being assembled.
type StreamEvent interface {
	isStreamEvent()
	Partial() *AssistantMessage
}

type baseEvent struct{ partial *AssistantMessage }

func (baseEvent) isStreamEvent()               {}
func (e baseEvent) Partial() *AssistantMessage { return e.partial }

type StartEvent struct{ baseEvent }

type TextStartEvent struct {
	baseEvent
	ContentIndex int
}

type TextDeltaEvent struct {
	baseEvent
	ContentIndex int
	Delta        string
}

type TextEndEvent struct {
	baseEvent
	ContentIndex int
	Content      string
}

type ThinkingStartEvent struct {
	baseEvent
	ContentIndex int
}

type ThinkingDeltaEvent struct {
	baseEvent
	ContentIndex int
	Delta        string
}

type ThinkingEndEvent struct {
	baseEvent
	ContentIndex int
	Content      string
}

type ToolCallStartEvent struct {
	baseEvent
	ContentIndex int
}

type ToolCallDeltaEvent struct {
	baseEvent
	ContentIndex int
	Delta        string
}

type ToolCallEndEvent struct {
	baseEvent
	ContentIndex int
	ToolCall     *ToolCall
}

type DoneEvent struct {
	baseEvent
	Reason  StopReason
	Message *AssistantMessage
}

type ErrorEvent struct {
	baseEvent
	Reason StopReason
	Error  *AssistantMessage
}
