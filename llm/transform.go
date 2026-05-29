package llm

import "time"

const (
	userImagePlaceholder = "(image omitted: model does not support images)"
	toolImagePlaceholder = "(tool image omitted: model does not support images)"
)

func replaceImages(content []Content, placeholder string) []Content {
	var out []Content
	prevPlaceholder := false
	for _, c := range content {
		if _, ok := c.(*Image); ok {
			if !prevPlaceholder {
				out = append(out, &Text{Text: placeholder})
			}
			prevPlaceholder = true
			continue
		}
		out = append(out, c)
		if t, ok := c.(*Text); ok {
			prevPlaceholder = t.Text == placeholder
		} else {
			prevPlaceholder = false
		}
	}
	return out
}

func downgradeImages(messages []Message, m *Model) []Message {
	if m.supportsImageInput() {
		return messages
	}
	out := make([]Message, len(messages))
	for i, msg := range messages {
		switch v := msg.(type) {
		case *UserMessage:
			out[i] = &UserMessage{Content: replaceImages(v.Content, userImagePlaceholder), Timestamp: v.Timestamp}
		case *ToolResultMessage:
			cp := *v
			cp.Content = replaceImages(v.Content, toolImagePlaceholder)
			out[i] = &cp
		default:
			out[i] = msg
		}
	}
	return out
}

func transformMessages(messages []Message, m *Model, normalizeID func(string) string) []Message {
	idMap := map[string]string{}
	imageAware := downgradeImages(messages, m)

	transformed := make([]Message, 0, len(imageAware))
	for _, msg := range imageAware {
		switch v := msg.(type) {
		case *UserMessage:
			transformed = append(transformed, v)
		case *ToolResultMessage:
			if newID, ok := idMap[v.ToolCallID]; ok && newID != v.ToolCallID {
				cp := *v
				cp.ToolCallID = newID
				transformed = append(transformed, &cp)
			} else {
				transformed = append(transformed, v)
			}
		case *AssistantMessage:
			sameModel := v.Provider == m.provider() && v.API == apiOpenAICompletions && v.Model == m.ID
			content := make([]Content, 0, len(v.Content))
			for _, block := range v.Content {
				switch b := block.(type) {
				case *Thinking:
					if b.Redacted {
						if sameModel {
							content = append(content, b)
						}
						continue
					}
					if sameModel && b.Signature != "" {
						content = append(content, b)
						continue
					}
					if b.Thinking == "" {
						continue
					}
					if sameModel {
						content = append(content, b)
					} else {
						content = append(content, &Text{Text: b.Thinking})
					}
				case *Text:
					content = append(content, b)
				case *ToolCall:
					tc := b
					if !sameModel && (b.ThoughtSignature != "" || normalizeID != nil) {
						cp := *b
						if !sameModel {
							cp.ThoughtSignature = ""
						}
						if normalizeID != nil {
							if newID := normalizeID(b.ID); newID != b.ID {
								idMap[b.ID] = newID
								cp.ID = newID
							}
						}
						tc = &cp
					}
					content = append(content, tc)
				default:
					content = append(content, block)
				}
			}
			cp := *v
			cp.Content = content
			transformed = append(transformed, &cp)
		default:
			transformed = append(transformed, msg)
		}
	}

	var result []Message
	var pending []*ToolCall
	existing := map[string]bool{}
	flush := func() {
		for _, tc := range pending {
			if !existing[tc.ID] {
				result = append(result, &ToolResultMessage{
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Content:    []Content{&Text{Text: "No result provided"}},
					IsError:    true,
					Timestamp:  time.Now(),
				})
			}
		}
		pending = nil
		existing = map[string]bool{}
	}

	for _, msg := range transformed {
		switch v := msg.(type) {
		case *AssistantMessage:
			flush()
			if v.StopReason == StopError || v.StopReason == StopAborted {
				continue
			}
			if tcs := toolCalls(v.Content); len(tcs) > 0 {
				pending = tcs
				existing = map[string]bool{}
			}
			result = append(result, v)
		case *ToolResultMessage:
			existing[v.ToolCallID] = true
			result = append(result, v)
		case *UserMessage:
			flush()
			result = append(result, v)
		default:
			result = append(result, msg)
		}
	}
	flush()
	return result
}
