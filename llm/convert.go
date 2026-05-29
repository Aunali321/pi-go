package llm

import (
	"encoding/json"
	"strings"
)

const apiOpenAICompletions = "openai-completions"

func normalizeToolCallID(m *Model, id string) string {
	if strings.Contains(id, "|") {
		callID, _, _ := strings.Cut(id, "|")
		var b strings.Builder
		for _, r := range callID {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		s := b.String()
		if len(s) > 40 {
			s = s[:40]
		}
		return s
	}
	if m.provider() == "openai" && len(id) > 40 {
		return id[:40]
	}
	return id
}

func imageDataURL(img *Image) string {
	return "data:" + img.MimeType + ";base64," + img.Data
}

func convertMessages(m *Model, ctx *Context, compat resolvedCompat) []map[string]any {
	transformed := transformMessages(ctx.Messages, m, func(id string) string {
		return normalizeToolCallID(m, id)
	})

	var params []map[string]any

	if ctx.SystemPrompt != "" {
		role := "system"
		if m.Reasoning && compat.supportsDeveloperRole {
			role = "developer"
		}
		params = append(params, map[string]any{"role": role, "content": sanitizeSurrogates(ctx.SystemPrompt)})
	}

	lastRole := ""

	for i := 0; i < len(transformed); i++ {
		msg := transformed[i]

		if compat.requiresAssistantAfterToolResult && lastRole == "toolResult" {
			if _, ok := msg.(*UserMessage); ok {
				params = append(params, map[string]any{"role": "assistant", "content": "I have processed the tool results."})
			}
		}

		switch v := msg.(type) {
		case *UserMessage:
			// pi keeps array content as an array of parts and only emits a plain
			// string when the source content was itself a string. Agent/harness
			// user messages are always arrays, so we always emit the parts form.
			var parts []map[string]any
			for _, c := range v.Content {
				switch b := c.(type) {
				case *Text:
					parts = append(parts, map[string]any{"type": "text", "text": sanitizeSurrogates(b.Text)})
				case *Image:
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageDataURL(b)}})
				}
			}
			if len(parts) == 0 {
				continue
			}
			params = append(params, map[string]any{"role": "user", "content": parts})

		case *AssistantMessage:
			am := map[string]any{"role": "assistant"}
			if compat.requiresAssistantAfterToolResult {
				am["content"] = ""
			} else {
				am["content"] = nil
			}

			var textParts []string
			for _, t := range textBlocks(v.Content) {
				if strings.TrimSpace(t.Text) != "" {
					textParts = append(textParts, sanitizeSurrogates(t.Text))
				}
			}
			assistantText := strings.Join(textParts, "")

			var thinking []*Thinking
			for _, c := range v.Content {
				if th, ok := c.(*Thinking); ok && strings.TrimSpace(th.Thinking) != "" {
					thinking = append(thinking, th)
				}
			}

			if len(thinking) > 0 {
				if compat.requiresThinkingAsText {
					var sb []string
					for _, th := range thinking {
						sb = append(sb, sanitizeSurrogates(th.Thinking))
					}
					content := []map[string]any{{"type": "text", "text": strings.Join(sb, "\n\n")}}
					for _, tp := range textParts {
						content = append(content, map[string]any{"type": "text", "text": tp})
					}
					am["content"] = content
				} else {
					if assistantText != "" {
						am["content"] = assistantText
					}
					if sig := thinking[0].Signature; sig != "" {
						var raw []string
						for _, th := range thinking {
							raw = append(raw, th.Thinking)
						}
						am[sig] = strings.Join(raw, "\n")
					}
				}
			} else if assistantText != "" {
				am["content"] = assistantText
			}

			tcs := toolCalls(v.Content)
			if len(tcs) > 0 {
				calls := make([]map[string]any, len(tcs))
				var details []any
				for i, tc := range tcs {
					args, _ := json.Marshal(tc.Arguments)
					calls[i] = map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(args),
						},
					}
					if tc.ThoughtSignature != "" {
						var d any
						if json.Unmarshal([]byte(tc.ThoughtSignature), &d) == nil {
							details = append(details, d)
						}
					}
				}
				am["tool_calls"] = calls
				if len(details) > 0 {
					am["reasoning_details"] = details
				}
			}

			if compat.requiresReasoningContentOnAssistant && m.Reasoning {
				if _, ok := am["reasoning_content"]; !ok {
					am["reasoning_content"] = ""
				}
			}

			content := am["content"]
			hasContent := false
			switch c := content.(type) {
			case string:
				hasContent = c != ""
			case []map[string]any:
				hasContent = len(c) > 0
			}
			_, hasTools := am["tool_calls"]
			if !hasContent && !hasTools {
				continue
			}
			params = append(params, am)

		case *ToolResultMessage:
			var imageBlocks []map[string]any
			j := i
			for ; j < len(transformed); j++ {
				tr, ok := transformed[j].(*ToolResultMessage)
				if !ok {
					break
				}
				var texts []string
				hasImages := false
				for _, c := range tr.Content {
					switch b := c.(type) {
					case *Text:
						texts = append(texts, b.Text)
					case *Image:
						hasImages = true
					}
				}
				textResult := strings.Join(texts, "\n")
				content := textResult
				if content == "" {
					content = "(see attached image)"
				}
				toolMsg := map[string]any{
					"role":         "tool",
					"content":      sanitizeSurrogates(content),
					"tool_call_id": tr.ToolCallID,
				}
				if compat.requiresToolResultName && tr.ToolName != "" {
					toolMsg["name"] = tr.ToolName
				}
				params = append(params, toolMsg)

				if hasImages && m.supportsImageInput() {
					for _, c := range tr.Content {
						if img, ok := c.(*Image); ok {
							imageBlocks = append(imageBlocks, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageDataURL(img)}})
						}
					}
				}
			}
			i = j - 1

			if len(imageBlocks) > 0 {
				if compat.requiresAssistantAfterToolResult {
					params = append(params, map[string]any{"role": "assistant", "content": "I have processed the tool results."})
				}
				content := []map[string]any{{"type": "text", "text": "Attached image(s) from tool result:"}}
				content = append(content, imageBlocks...)
				params = append(params, map[string]any{"role": "user", "content": content})
				lastRole = "user"
			} else {
				lastRole = "toolResult"
			}
			continue
		}

		lastRole = msg.Role()
	}

	return params
}

func convertTools(tools []Tool, compat resolvedCompat) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		fn := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		}
		tool := map[string]any{"type": "function", "function": fn}
		if compat.supportsStrictMode {
			fn["strict"] = false
		}
		out[i] = tool
	}
	return out
}

func hasToolHistory(messages []Message) bool {
	for _, msg := range messages {
		switch v := msg.(type) {
		case *ToolResultMessage:
			return true
		case *AssistantMessage:
			if len(toolCalls(v.Content)) > 0 {
				return true
			}
		}
	}
	return false
}
