package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// jsonStringInner escapes s the way JS JSON.stringify does (no HTML
// escaping), without the surrounding quotes.
func jsonStringInner(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func jsonString(s string) string { return `"` + jsonStringInner(s) + `"` }

// jsonMarshalJS marshals compactly without Go's default HTML escaping,
// matching JS JSON.stringify output for the characters <, > and &.
func jsonMarshalJS(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func msToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func timeToMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (t *Text) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": "text", "text": t.Text}
	if t.Signature != "" {
		m["textSignature"] = t.Signature
	}
	return json.Marshal(m)
}

func (t *Thinking) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": "thinking", "thinking": t.Thinking}
	if t.Signature != "" {
		m["thinkingSignature"] = t.Signature
	}
	if t.Redacted {
		m["redacted"] = true
	}
	return json.Marshal(m)
}

func (i *Image) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"type": "image", "data": i.Data, "mimeType": i.MimeType})
}

func (tc *ToolCall) MarshalJSON() ([]byte, error) {
	args := tc.Arguments
	if args == nil {
		args = map[string]any{}
	}
	m := map[string]any{"type": "toolCall", "id": tc.ID, "name": tc.Name, "arguments": args}
	if tc.ThoughtSignature != "" {
		m["thoughtSignature"] = tc.ThoughtSignature
	}
	return json.Marshal(m)
}

// MarshalContentSlice serializes a content slice; nil becomes [].
func marshalContent(content []Content) ([]byte, error) {
	if content == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(content)
}

func unmarshalContentSlice(data json.RawMessage) ([]Content, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	out := make([]Content, 0, len(raws))
	for _, raw := range raws {
		c, err := unmarshalContent(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func unmarshalContent(data json.RawMessage) (Content, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}
	switch head.Type {
	case "text":
		var v struct {
			Text      string `json:"text"`
			Signature string `json:"textSignature"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &Text{Text: v.Text, Signature: v.Signature}, nil
	case "thinking":
		var v struct {
			Thinking  string `json:"thinking"`
			Signature string `json:"thinkingSignature"`
			Redacted  bool   `json:"redacted"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &Thinking{Thinking: v.Thinking, Signature: v.Signature, Redacted: v.Redacted}, nil
	case "image":
		var v struct {
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return &Image{Data: v.Data, MimeType: v.MimeType}, nil
	case "toolCall":
		var v struct {
			ID               string         `json:"id"`
			Name             string         `json:"name"`
			Arguments        map[string]any `json:"arguments"`
			ThoughtSignature string         `json:"thoughtSignature"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		if v.Arguments == nil {
			v.Arguments = map[string]any{}
		}
		return &ToolCall{ID: v.ID, Name: v.Name, Arguments: v.Arguments, ThoughtSignature: v.ThoughtSignature}, nil
	default:
		return nil, fmt.Errorf("unknown content type %q", head.Type)
	}
}

func (m *UserMessage) MarshalJSON() ([]byte, error) {
	content, err := marshalContent(m.Content)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"role":      "user",
		"content":   json.RawMessage(content),
		"timestamp": timeToMs(m.Timestamp),
	})
}

func (m *UserMessage) UnmarshalJSON(data []byte) error {
	var v struct {
		Content   json.RawMessage `json:"content"`
		Timestamp int64           `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.Timestamp = msToTime(v.Timestamp)
	return unmarshalMessageContent(v.Content, &m.Content)
}

func (m *AssistantMessage) MarshalJSON() ([]byte, error) {
	content, err := marshalContent(m.Content)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"role":       "assistant",
		"content":    json.RawMessage(content),
		"api":        m.API,
		"provider":   m.Provider,
		"model":      m.Model,
		"usage":      m.Usage,
		"stopReason": m.StopReason,
		"timestamp":  timeToMs(m.Timestamp),
	}
	if m.ResponseModel != "" {
		out["responseModel"] = m.ResponseModel
	}
	if m.ResponseID != "" {
		out["responseId"] = m.ResponseID
	}
	if m.ErrorMessage != "" {
		out["errorMessage"] = m.ErrorMessage
	}
	return json.Marshal(out)
}

func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	var v struct {
		Content       json.RawMessage `json:"content"`
		API           string          `json:"api"`
		Provider      string          `json:"provider"`
		Model         string          `json:"model"`
		ResponseModel string          `json:"responseModel"`
		ResponseID    string          `json:"responseId"`
		Usage         Usage           `json:"usage"`
		StopReason    StopReason      `json:"stopReason"`
		ErrorMessage  string          `json:"errorMessage"`
		Timestamp     int64           `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.API, m.Provider, m.Model = v.API, v.Provider, v.Model
	m.ResponseModel, m.ResponseID = v.ResponseModel, v.ResponseID
	m.Usage, m.StopReason, m.ErrorMessage = v.Usage, v.StopReason, v.ErrorMessage
	m.Timestamp = msToTime(v.Timestamp)
	if len(v.Content) == 0 {
		return nil
	}
	c, err := unmarshalContentSlice(v.Content)
	if err != nil {
		return err
	}
	m.Content = c
	return nil
}

func (m *ToolResultMessage) MarshalJSON() ([]byte, error) {
	content, err := marshalContent(m.Content)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"role":       "toolResult",
		"toolCallId": m.ToolCallID,
		"toolName":   m.ToolName,
		"content":    json.RawMessage(content),
		"isError":    m.IsError,
		"timestamp":  timeToMs(m.Timestamp),
	}
	if m.Details != nil {
		out["details"] = m.Details
	}
	if m.Usage != nil {
		out["usage"] = m.Usage
	}
	if m.AddedToolNames != nil {
		out["addedToolNames"] = m.AddedToolNames
	}
	return json.Marshal(out)
}

func (m *ToolResultMessage) UnmarshalJSON(data []byte) error {
	var v struct {
		ToolCallID     string          `json:"toolCallId"`
		ToolName       string          `json:"toolName"`
		Content        json.RawMessage `json:"content"`
		Details        json.RawMessage `json:"details"`
		Usage          *Usage          `json:"usage"`
		AddedToolNames []string        `json:"addedToolNames"`
		IsError        bool            `json:"isError"`
		Timestamp      int64           `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.ToolCallID, m.ToolName, m.IsError = v.ToolCallID, v.ToolName, v.IsError
	m.Usage, m.AddedToolNames = v.Usage, v.AddedToolNames
	m.Timestamp = msToTime(v.Timestamp)
	if len(v.Details) > 0 {
		var d any
		if err := json.Unmarshal(v.Details, &d); err != nil {
			return err
		}
		m.Details = d
	}
	if len(v.Content) == 0 {
		return nil
	}
	c, err := unmarshalContentSlice(v.Content)
	if err != nil {
		return err
	}
	m.Content = c
	return nil
}

// unmarshalMessageContent decodes a content field that may be a plain string or
// an array of content blocks.
func unmarshalMessageContent(data json.RawMessage, dst *[]Content) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*dst = []Content{&Text{Text: s}}
		return nil
	}
	c, err := unmarshalContentSlice(data)
	if err != nil {
		return err
	}
	*dst = c
	return nil
}

// DecodeContentField decodes a content field that is either a plain string or
// an array of content blocks.
func DecodeContentField(data json.RawMessage) ([]Content, error) {
	var out []Content
	if err := unmarshalMessageContent(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DecodeMessage decodes an LLM message from JSON by its role.
func DecodeMessage(role string, data json.RawMessage) (Message, error) {
	switch role {
	case "user":
		var m UserMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		return &m, nil
	case "assistant":
		var m AssistantMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		return &m, nil
	case "toolResult":
		var m ToolResultMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		return &m, nil
	default:
		return nil, fmt.Errorf("unknown message role %q", role)
	}
}
