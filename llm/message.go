package llm

import "time"

type StopReason string

const (
	StopEnd     StopReason = "stop"
	StopLength  StopReason = "length"
	StopToolUse StopReason = "toolUse"
	StopError   StopReason = "error"
	StopAborted StopReason = "aborted"
)

// Message is one of *UserMessage, *AssistantMessage or *ToolResultMessage.
type Message interface {
	Role() string
}

type UserMessage struct {
	Content   []Content
	Timestamp time.Time
}

func (*UserMessage) Role() string { return "user" }

func TextUser(text string) *UserMessage {
	return &UserMessage{Content: []Content{&Text{Text: text}}, Timestamp: time.Now()}
}

type AssistantMessage struct {
	Content []Content

	API           string
	Provider      string
	Model         string
	ResponseModel string
	ResponseID    string

	Usage        Usage
	StopReason   StopReason
	ErrorMessage string
	Timestamp    time.Time
}

func (*AssistantMessage) Role() string { return "assistant" }

type ToolResultMessage struct {
	ToolCallID string
	ToolName   string
	Content    []Content
	Details    any
	IsError    bool
	Timestamp  time.Time
}

func (*ToolResultMessage) Role() string { return "toolResult" }

type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
}

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
}
