package llm

func compatCacheControl(compat resolvedCompat, retention CacheRetention) map[string]any {
	if compat.cacheControlFormat != CacheControlAnthropic || retention == CacheNone {
		return nil
	}
	cc := map[string]any{"type": "ephemeral"}
	if retention == CacheLong && compat.supportsLongCacheRetention {
		cc["ttl"] = "1h"
	}
	return cc
}

func applyAnthropicCacheControl(messages, tools []map[string]any, cc map[string]any) {
	cacheSystemPrompt(messages, cc)
	cacheLastTool(tools, cc)
	cacheLastConversationMessage(messages, cc)
}

func cacheSystemPrompt(messages []map[string]any, cc map[string]any) {
	for _, msg := range messages {
		if role, _ := msg["role"].(string); role == "system" || role == "developer" {
			cacheTextContent(msg, cc)
			return
		}
	}
}

func cacheLastTool(tools []map[string]any, cc map[string]any) {
	if len(tools) == 0 {
		return
	}
	tools[len(tools)-1]["cache_control"] = cc
}

func cacheLastConversationMessage(messages []map[string]any, cc map[string]any) {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role == "user" || role == "assistant" || role == "tool" {
			if cacheTextContent(messages[i], cc) {
				return
			}
		}
	}
}

func cacheTextContent(msg, cc map[string]any) bool {
	switch content := msg["content"].(type) {
	case string:
		if content == "" {
			return false
		}
		msg["content"] = []map[string]any{{"type": "text", "text": content, "cache_control": cc}}
		return true
	case []map[string]any:
		for i := len(content) - 1; i >= 0; i-- {
			if t, _ := content[i]["type"].(string); t == "text" {
				content[i]["cache_control"] = cc
				return true
			}
		}
	}
	return false
}
