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
	// Usage from the tool execution itself, if available. Not part of main
	// LLM context accounting.
	Usage *Usage
	// AddedToolNames lists names from Context.Tools that became available
	// after this result. Providers with native deferred tool loading use it
	// as the load point; others ignore it and use Context.Tools normally.
	AddedToolNames []string
	IsError        bool
	Timestamp      time.Time
}

func (*ToolResultMessage) Role() string { return "toolResult" }

type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	// CacheWrite1h is the subset of CacheWrite written with 1h retention.
	// Only Anthropic reports this split.
	CacheWrite1h int `json:"cacheWrite1h,omitempty"`
	// Reasoning counts reasoning/thinking tokens when the provider reports
	// them; a subset of Output. Nil when the provider exposes no breakdown.
	Reasoning   *int `json:"reasoning,omitempty"`
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
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	// ConstrainedSampling optionally requests provider-side constrained
	// sampling for this tool. Nil disables it.
	ConstrainedSampling *ConstrainedSampling `json:"constrainedSampling,omitempty"`
}

type GrammarFormat string

const (
	GrammarOpenAILark  GrammarFormat = "openai_lark"
	GrammarOpenAIRegex GrammarFormat = "openai_regex"
)

// ConstrainedSampling configures provider-side constrained sampling for a
// tool. Type "json_schema" roughly maps to strict tool schemas; Type
// "grammar" supplies provider-specific encodings of the intended language.
type ConstrainedSampling struct {
	Type string `json:"type"` // "json_schema" or "grammar"
	// Strict applies to type "json_schema": "prefer" or "require".
	Strict string `json:"strict,omitempty"`
	// Variants applies to type "grammar".
	Variants map[GrammarFormat]string `json:"variants,omitempty"`
}

type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
}
