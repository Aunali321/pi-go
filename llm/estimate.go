package llm

import "time"

// ContextUsageEstimate summarizes estimated token usage for a message list.
type ContextUsageEstimate struct {
	// Tokens is the estimated total context tokens.
	Tokens int
	// UsageTokens is the count reported by the most recent applicable
	// assistant usage block.
	UsageTokens int
	// TrailingTokens is the estimate for messages after that usage block.
	TrailingTokens int
	// LastUsageIndex is the index of the message that provided usage, or -1.
	LastUsageIndex int
}

const (
	charsPerToken       = 4
	estimatedImageChars = 4800
)

// CalculateContextTokens returns the context size a usage block describes.
func CalculateContextTokens(u Usage) int {
	if u.TotalTokens != 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

// EstimateTextTokens estimates tokens for a plain string.
func EstimateTextTokens(text string) int {
	return ceilDiv(len(text), charsPerToken)
}

func estimateTextAndImageChars(content []Content) int {
	chars := 0
	for _, c := range content {
		switch b := c.(type) {
		case *Text:
			chars += len(b.Text)
		case *Image:
			chars += estimatedImageChars
		}
	}
	return chars
}

// EstimateMessageTokens estimates the token count for one message.
func EstimateMessageTokens(msg Message) int {
	switch m := msg.(type) {
	case *UserMessage:
		return ceilDiv(estimateTextAndImageChars(m.Content), charsPerToken)
	case *ToolResultMessage:
		return ceilDiv(estimateTextAndImageChars(m.Content), charsPerToken)
	case *AssistantMessage:
		chars := 0
		for _, c := range m.Content {
			switch b := c.(type) {
			case *Text:
				chars += len(b.Text)
			case *Thinking:
				chars += len(b.Thinking)
			case *ToolCall:
				chars += len(b.Name) + len(safeJSONLen(b.Arguments))
			}
		}
		return ceilDiv(chars, charsPerToken)
	}
	return 0
}

func safeJSONLen(v any) string {
	data, err := jsonMarshalJS(v)
	if err != nil {
		return "[unserializable]"
	}
	return string(data)
}

func lastAssistantUsage(messages []Message) (Usage, int) {
	var latestPrefix time.Time
	var usage Usage
	index := -1

	for i, msg := range messages {
		if a, ok := msg.(*AssistantMessage); ok {
			// A newer prefix message inserted after this response (for
			// example, a compaction summary) means its usage cannot describe
			// the current prefix.
			appliesToPrefix := !a.Timestamp.Before(latestPrefix)
			if appliesToPrefix && a.StopReason != StopAborted && a.StopReason != StopError &&
				CalculateContextTokens(a.Usage) > 0 {
				usage, index = a.Usage, i
			}
		}
		if ts := messageTimestamp(msg); ts.After(latestPrefix) {
			latestPrefix = ts
		}
	}
	return usage, index
}

func messageTimestamp(msg Message) time.Time {
	switch m := msg.(type) {
	case *UserMessage:
		return m.Timestamp
	case *AssistantMessage:
		return m.Timestamp
	case *ToolResultMessage:
		return m.Timestamp
	}
	return time.Time{}
}

// EstimateMessagesTokens estimates total context tokens for a message list,
// anchored on the most recent applicable assistant usage block when present.
func EstimateMessagesTokens(messages []Message) ContextUsageEstimate {
	usage, index := lastAssistantUsage(messages)
	if index >= 0 {
		usageTokens := CalculateContextTokens(usage)
		trailing := 0
		for i := index + 1; i < len(messages); i++ {
			trailing += EstimateMessageTokens(messages[i])
		}
		return ContextUsageEstimate{
			Tokens:         usageTokens + trailing,
			UsageTokens:    usageTokens,
			TrailingTokens: trailing,
			LastUsageIndex: index,
		}
	}

	tokens := 0
	for _, msg := range messages {
		tokens += EstimateMessageTokens(msg)
	}
	return ContextUsageEstimate{Tokens: tokens, TrailingTokens: tokens, LastUsageIndex: -1}
}

func estimateToolsTokens(tools []Tool) int {
	if len(tools) == 0 {
		return 0
	}
	return EstimateTextTokens(safeJSONLen(tools))
}

// EstimateContextTokens estimates total context tokens for a full request
// context, including the system prompt and tool definitions when no usage
// anchor exists, and deferred tool definitions loaded after the anchor.
func EstimateContextTokens(ctx *Context) ContextUsageEstimate {
	est := EstimateMessagesTokens(ctx.Messages)
	if est.LastUsageIndex >= 0 {
		addedNames := map[string]bool{}
		for _, msg := range ctx.Messages[est.LastUsageIndex+1:] {
			if tr, ok := msg.(*ToolResultMessage); ok {
				for _, name := range tr.AddedToolNames {
					addedNames[name] = true
				}
			}
		}
		var added []Tool
		for _, t := range ctx.Tools {
			if addedNames[t.Name] {
				added = append(added, t)
			}
		}
		addedTokens := estimateToolsTokens(added)
		est.Tokens += addedTokens
		est.TrailingTokens += addedTokens
		return est
	}

	prefixTokens := estimateToolsTokens(ctx.Tools)
	if ctx.SystemPrompt != "" {
		prefixTokens += EstimateTextTokens(ctx.SystemPrompt)
	}
	est.Tokens += prefixTokens
	est.TrailingTokens += prefixTokens
	return est
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }
