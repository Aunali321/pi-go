package message

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/llm"
)

const (
	CompactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	CompactionSummarySuffix = "\n</summary>"
	BranchSummaryPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	BranchSummarySuffix     = "</summary>"
)

type BashExecutionMessage struct {
	Command            string
	Output             string
	ExitCode           *int
	Cancelled          bool
	Truncated          bool
	FullOutputPath     string
	Timestamp          time.Time
	ExcludeFromContext bool
}

func (*BashExecutionMessage) Role() string { return "bashExecution" }

func (m *BashExecutionMessage) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"role":      "bashExecution",
		"command":   m.Command,
		"output":    m.Output,
		"cancelled": m.Cancelled,
		"truncated": m.Truncated,
		"timestamp": m.Timestamp.UnixMilli(),
	}
	if m.ExitCode != nil {
		out["exitCode"] = *m.ExitCode
	}
	if m.FullOutputPath != "" {
		out["fullOutputPath"] = m.FullOutputPath
	}
	if m.ExcludeFromContext {
		out["excludeFromContext"] = true
	}
	return json.Marshal(out)
}

type CustomMessage struct {
	CustomType string
	Content    []llm.Content
	Display    bool
	Details    any
	Timestamp  time.Time
}

func (*CustomMessage) Role() string { return "custom" }

func (m *CustomMessage) MarshalJSON() ([]byte, error) {
	content := m.Content
	if content == nil {
		content = []llm.Content{}
	}
	out := map[string]any{
		"role":       "custom",
		"customType": m.CustomType,
		"content":    content,
		"display":    m.Display,
		"timestamp":  m.Timestamp.UnixMilli(),
	}
	if m.Details != nil {
		out["details"] = m.Details
	}
	return json.Marshal(out)
}

type BranchSummaryMessage struct {
	Summary   string
	FromID    string
	Timestamp time.Time
}

func (*BranchSummaryMessage) Role() string { return "branchSummary" }

func (m *BranchSummaryMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"role":      "branchSummary",
		"summary":   m.Summary,
		"fromId":    m.FromID,
		"timestamp": m.Timestamp.UnixMilli(),
	})
}

type CompactionSummaryMessage struct {
	Summary      string
	TokensBefore int
	Timestamp    time.Time
}

func (*CompactionSummaryMessage) Role() string { return "compactionSummary" }

func (m *CompactionSummaryMessage) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"role":         "compactionSummary",
		"summary":      m.Summary,
		"tokensBefore": m.TokensBefore,
		"timestamp":    m.Timestamp.UnixMilli(),
	})
}

func bashExecutionToText(m *BashExecutionMessage) string {
	text := fmt.Sprintf("Ran `%s`\n", m.Command)
	if m.Output != "" {
		text += "```\n" + m.Output + "\n```"
	} else {
		text += "(no output)"
	}
	if m.Cancelled {
		text += "\n\n(command cancelled)"
	} else if m.ExitCode != nil && *m.ExitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *m.ExitCode)
	}
	if m.Truncated && m.FullOutputPath != "" {
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", m.FullOutputPath)
	}
	return text
}

func CreateBranchSummaryMessage(summary, fromID string, ts time.Time) *BranchSummaryMessage {
	return &BranchSummaryMessage{Summary: summary, FromID: fromID, Timestamp: ts}
}

func CreateCompactionSummaryMessage(summary string, tokensBefore int, ts time.Time) *CompactionSummaryMessage {
	return &CompactionSummaryMessage{Summary: summary, TokensBefore: tokensBefore, Timestamp: ts}
}

func CreateCustomMessage(customType string, content []llm.Content, display bool, details any, ts time.Time) *CustomMessage {
	return &CustomMessage{CustomType: customType, Content: content, Display: display, Details: details, Timestamp: ts}
}

// ConvertToLLM reduces agent messages (including harness custom messages) to LLM
// messages for a provider request.
func ConvertToLLM(messages []agent.AgentMessage) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		switch v := m.(type) {
		case *BashExecutionMessage:
			if v.ExcludeFromContext {
				continue
			}
			out = append(out, &llm.UserMessage{
				Content:   []llm.Content{&llm.Text{Text: bashExecutionToText(v)}},
				Timestamp: v.Timestamp,
			})
		case *CustomMessage:
			out = append(out, &llm.UserMessage{Content: v.Content, Timestamp: v.Timestamp})
		case *BranchSummaryMessage:
			out = append(out, &llm.UserMessage{
				Content:   []llm.Content{&llm.Text{Text: BranchSummaryPrefix + v.Summary + BranchSummarySuffix}},
				Timestamp: v.Timestamp,
			})
		case *CompactionSummaryMessage:
			out = append(out, &llm.UserMessage{
				Content:   []llm.Content{&llm.Text{Text: CompactionSummaryPrefix + v.Summary + CompactionSummarySuffix}},
				Timestamp: v.Timestamp,
			})
		case llm.Message:
			out = append(out, v)
		}
	}
	return out
}

func DecodeAgentMessage(data json.RawMessage) (agent.AgentMessage, error) {
	var head struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	switch head.Role {
	case "user", "assistant", "toolResult":
		return llm.DecodeMessage(head.Role, data)
	case "bashExecution":
		var v struct {
			Command            string `json:"command"`
			Output             string `json:"output"`
			ExitCode           *int   `json:"exitCode"`
			Cancelled          bool   `json:"cancelled"`
			Truncated          bool   `json:"truncated"`
			FullOutputPath     string `json:"fullOutputPath"`
			Timestamp          int64  `json:"timestamp"`
			ExcludeFromContext bool   `json:"excludeFromContext"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &BashExecutionMessage{v.Command, v.Output, v.ExitCode, v.Cancelled, v.Truncated, v.FullOutputPath, time.UnixMilli(v.Timestamp), v.ExcludeFromContext}, nil
	case "custom":
		var v struct {
			CustomType string          `json:"customType"`
			Content    json.RawMessage `json:"content"`
			Display    bool            `json:"display"`
			Details    json.RawMessage `json:"details"`
			Timestamp  int64           `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		content, err := llm.DecodeContentField(v.Content)
		if err != nil {
			return nil, err
		}
		return &CustomMessage{v.CustomType, content, v.Display, rawAny(v.Details), time.UnixMilli(v.Timestamp)}, nil
	case "branchSummary":
		var v struct {
			Summary   string `json:"summary"`
			FromID    string `json:"fromId"`
			Timestamp int64  `json:"timestamp"`
		}
		json.Unmarshal(data, &v)
		return &BranchSummaryMessage{v.Summary, v.FromID, time.UnixMilli(v.Timestamp)}, nil
	case "compactionSummary":
		var v struct {
			Summary      string `json:"summary"`
			TokensBefore int    `json:"tokensBefore"`
			Timestamp    int64  `json:"timestamp"`
		}
		json.Unmarshal(data, &v)
		return &CompactionSummaryMessage{v.Summary, v.TokensBefore, time.UnixMilli(v.Timestamp)}, nil
	default:
		return nil, fmt.Errorf("unknown message role %q", head.Role)
	}
}

func DecodeCustomMessageContent(data json.RawMessage) ([]llm.Content, error) {
	return llm.DecodeContentField(data)
}
