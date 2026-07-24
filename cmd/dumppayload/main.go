package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/aunali321/pi-go/llm"
)

func mkModel(id string, reasoning bool) *llm.Model {
	return &llm.Model{
		ID: id, Name: id, Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1",
		Reasoning: reasoning, Input: []llm.InputModality{llm.InputText, llm.InputImage},
		Cost: llm.Pricing{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}, ContextWindow: 200000, MaxTokens: 1024,
	}
}

var weatherTool = llm.Tool{
	Name: "get_weather", Description: "Get the current weather for a city.",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
}

func ts(ms int64) time.Time { return time.UnixMilli(ms) }

type scenario struct {
	model *llm.Model
	ctx   *llm.Context
	opts  *llm.StreamOptions
}

func scenarios() map[string]scenario {
	asst := func(content ...llm.Content) *llm.AssistantMessage {
		return &llm.AssistantMessage{Content: content, API: "openai-completions", Provider: "openrouter", Model: "anthropic/claude-3.5-haiku", StopReason: llm.StopToolUse, Timestamp: ts(2)}
	}
	return map[string]scenario{
		"base": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheShort, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "What's the weather in Paris?"}}, Timestamp: ts(1700000000000)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.Text{Text: "Let me check."}, &llm.ToolCall{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "Paris"}}}, API: "openai-completions", Provider: "openrouter", Model: "anthropic/claude-3.5-haiku", StopReason: llm.StopToolUse, Timestamp: ts(1700000001000)},
					&llm.ToolResultMessage{ToolCallID: "call_1", ToolName: "get_weather", Content: []llm.Content{&llm.Text{Text: "12C overcast"}}, Timestamp: ts(1700000002000)},
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "Thanks. Anything else notable?"}}, Timestamp: ts(1700000003000)},
				},
				Tools: []llm.Tool{weatherTool},
			},
		},
		"reasoning": {
			model: mkModel("openai/gpt-4o-mini", true),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheShort, MaxTokens: 2048, Reasoning: llm.ThinkingHigh},
			ctx:   &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}, Timestamp: ts(1)}}},
		},
		"longcache": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheLong, MaxTokens: 1024},
			ctx:   &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}, Timestamp: ts(1)}}, Tools: []llm.Tool{weatherTool}},
		},
		"images": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "look"}, &llm.Image{Data: "AAAA", MimeType: "image/png"}}, Timestamp: ts(1)},
					asst(&llm.ToolCall{ID: "c1", Name: "get_weather", Arguments: map[string]any{}}),
					&llm.ToolResultMessage{ToolCallID: "c1", ToolName: "get_weather", Content: []llm.Content{&llm.Text{Text: "see image"}, &llm.Image{Data: "BBBB", MimeType: "image/jpeg"}}, Timestamp: ts(3)},
				},
				Tools: []llm.Tool{weatherTool},
			},
		},
		"thinking": {
			model: mkModel("anthropic/claude-3.5-haiku", true),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024, Reasoning: llm.ThinkingMedium},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "solve"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.Thinking{Thinking: "let me think", Signature: "reasoning_content"}, &llm.Text{Text: "answer"}}, API: "openai-completions", Provider: "openrouter", Model: "anthropic/claude-3.5-haiku", StopReason: llm.StopEnd, Timestamp: ts(2)},
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "more"}}, Timestamp: ts(3)},
				},
			},
		},
		"multitoolresult": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "do two"}}, Timestamp: ts(1)},
					asst(&llm.ToolCall{ID: "a", Name: "get_weather", Arguments: map[string]any{"city": "A"}}, &llm.ToolCall{ID: "b", Name: "get_weather", Arguments: map[string]any{"city": "B"}}),
					&llm.ToolResultMessage{ToolCallID: "a", ToolName: "get_weather", Content: []llm.Content{&llm.Text{Text: "AR"}}, Timestamp: ts(3)},
					&llm.ToolResultMessage{ToolCallID: "b", ToolName: "get_weather", Content: []llm.Content{&llm.Text{Text: "BR"}}, Timestamp: ts(4)},
				},
				Tools: []llm.Tool{weatherTool},
			},
		},
		"deepseek": {
			model: &llm.Model{ID: "deepseek-chat", Name: "deepseek", Provider: "deepseek", BaseURL: "https://api.deepseek.com", Reasoning: true, Input: []llm.InputModality{llm.InputText}, Cost: llm.Pricing{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}, ContextWindow: 200000, MaxTokens: 1024},
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024, Reasoning: llm.ThinkingHigh},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.Text{Text: "prev"}}, API: "openai-completions", Provider: "deepseek", Model: "deepseek-chat", StopReason: llm.StopEnd, Timestamp: ts(2)},
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "more"}}, Timestamp: ts(3)},
				},
			},
		},
		"imageonly": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx:   &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Image{Data: "AAAA", MimeType: "image/png"}}, Timestamp: ts(1)}}},
		},
		"toolhistory": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "x"}}, Timestamp: ts(1)},
					asst(&llm.ToolCall{ID: "h", Name: "t", Arguments: map[string]any{}}),
					&llm.ToolResultMessage{ToolCallID: "h", ToolName: "t", Content: []llm.Content{&llm.Text{Text: "r"}}, Timestamp: ts(3)},
				},
			},
		},
		"reasoningoff": {
			model: mkModel("openai/gpt-4o-mini", true),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheShort, MaxTokens: 1024},
			ctx:   &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}, Timestamp: ts(1)}}},
		},
		"crossmodel": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "q"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.Thinking{Thinking: "secret", Signature: "sig"}, &llm.Thinking{Thinking: "", Signature: "x", Redacted: true}, &llm.Text{Text: "answer"}, &llm.ToolCall{ID: "tc1", Name: "t", Arguments: map[string]any{}}}, API: "openai-completions", Provider: "openai", Model: "gpt-4", StopReason: llm.StopToolUse, Timestamp: ts(2)},
					&llm.ToolResultMessage{ToolCallID: "tc1", ToolName: "t", Content: []llm.Content{&llm.Text{Text: "r"}}, Timestamp: ts(3)},
				},
				Tools: []llm.Tool{weatherTool},
			},
		},
		"erroredmsg": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "a"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.Text{Text: "partial"}}, API: "openai-completions", Provider: "openrouter", Model: "anthropic/claude-3.5-haiku", StopReason: llm.StopError, ErrorMessage: "boom", Timestamp: ts(2)},
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "b"}}, Timestamp: ts(3)},
				},
			},
		},
		"idnormalize": {
			model: &llm.Model{ID: "gpt-4o", Name: "gpt-4o", Provider: "openai", BaseURL: "https://api.openai.com/v1", Reasoning: false, Input: []llm.InputModality{llm.InputText}, Cost: llm.Pricing{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}, ContextWindow: 200000, MaxTokens: 1024},
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "q"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.ToolCall{ID: "call_abcdefghijabcdefghijabcdefghijabcdefghij", Name: "t", Arguments: map[string]any{}}}, API: "openai-completions", Provider: "anthropic", Model: "claude", StopReason: llm.StopToolUse, Timestamp: ts(2)},
					&llm.ToolResultMessage{ToolCallID: "call_abcdefghijabcdefghijabcdefghijabcdefghij", ToolName: "t", Content: []llm.Content{&llm.Text{Text: "r"}}, Timestamp: ts(3)},
				},
			},
		},
		"orphan": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "go"}}, Timestamp: ts(1)},
					asst(&llm.ToolCall{ID: "orphan1", Name: "get_weather", Arguments: map[string]any{"city": "X"}}),
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "next"}}, Timestamp: ts(3)},
				},
				Tools: []llm.Tool{weatherTool},
			},
		},
		"notooloutput": {
			model: mkModel("anthropic/claude-3.5-haiku", false),
			opts:  &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "go"}}, Timestamp: ts(1)},
					asst(&llm.ToolCall{ID: "c1", Name: "get_weather", Arguments: map[string]any{"city": "X"}}),
					&llm.ToolResultMessage{ToolCallID: "c1", ToolName: "get_weather", Timestamp: ts(3)},
				},
				Tools: []llm.Tool{weatherTool},
			},
		},
		"kimideferred": {
			model: func() *llm.Model {
				m := mkModel("moonshot/kimi-k2", false)
				m.Compat = &llm.Compat{DeferredToolsMode: llm.DeferredToolsKimi}
				return m
			}(),
			opts: &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "go"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.ToolCall{ID: "c1", Name: "lookup", Arguments: map[string]any{}}}, API: "openai-completions", Provider: "openrouter", Model: "moonshot/kimi-k2", StopReason: llm.StopToolUse, Timestamp: ts(2)},
					&llm.ToolResultMessage{ToolCallID: "c1", ToolName: "lookup", Content: []llm.Content{&llm.Text{Text: "found"}}, AddedToolNames: []string{"get_weather"}, Timestamp: ts(3)},
				},
				Tools: []llm.Tool{weatherTool, {Name: "lookup", Description: "Look something up.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}},
			},
		},
		"grammartool": {
			model: func() *llm.Model {
				m := mkModel("openai/gpt-5.2", false)
				t := true
				m.Compat = &llm.Compat{SupportsOpenAIGrammarTools: &t}
				return m
			}(),
			opts: &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024},
			ctx: &llm.Context{
				SystemPrompt: "sys",
				Messages: []llm.Message{
					&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "go"}}, Timestamp: ts(1)},
					&llm.AssistantMessage{Content: []llm.Content{&llm.ToolCall{ID: "g1", Name: "run_sql", Arguments: map[string]any{"query": "select 1"}}}, API: "openai-completions", Provider: "openrouter", Model: "openai/gpt-5.2", StopReason: llm.StopToolUse, Timestamp: ts(2)},
					&llm.ToolResultMessage{ToolCallID: "g1", ToolName: "run_sql", Content: []llm.Content{&llm.Text{Text: "1"}}, Timestamp: ts(3)},
				},
				Tools: []llm.Tool{
					{Name: "run_sql", Description: "Run SQL.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}, ConstrainedSampling: &llm.ConstrainedSampling{Type: "grammar", Variants: map[llm.GrammarFormat]string{llm.GrammarOpenAILark: "start: /.+/"}}},
					{Name: "strict_tool", Description: "Strict.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []string{"a"}}, ConstrainedSampling: &llm.ConstrainedSampling{Type: "json_schema", Strict: "prefer"}},
				},
			},
		},
		"zaithinking": {
			model: func() *llm.Model {
				m := mkModel("z-ai/glm-5", true)
				m.Compat = &llm.Compat{ThinkingFormat: llm.ThinkingFormatZAI}
				return m
			}(),
			opts: &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024, Reasoning: llm.ThinkingMedium},
			ctx:  &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}, Timestamp: ts(1)}}},
		},
		"chattemplate": {
			model: func() *llm.Model {
				m := mkModel("qwen/qwen4", true)
				m.Compat = &llm.Compat{
					ThinkingFormat: llm.ThinkingFormatChatTemplate,
					ChatTemplateKwargs: map[string]llm.ChatTemplateKwarg{
						"enable_thinking": {Var: "thinking.enabled"},
						"thinking_effort": {Var: "thinking.effort", OmitWhenOff: true},
						"fixed":           {Value: "on"},
					},
				}
				return m
			}(),
			opts: &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024, Reasoning: llm.ThinkingLow},
			ctx:  &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}, Timestamp: ts(1)}}},
		},
		"clampmax": {
			model: func() *llm.Model {
				m := mkModel("anthropic/claude-3.5-haiku", false)
				m.ContextWindow = 5000
				return m
			}(),
			opts: &llm.StreamOptions{APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 4096},
			ctx:  &llm.Context{SystemPrompt: "sys", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hello there"}}, Timestamp: ts(1)}}},
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
	}
	s, ok := scenarios()[os.Args[1]]
	if !ok {
		os.Exit(1)
	}
	s.opts.OnPayload = func(payload map[string]any) map[string]any {
		out, _ := json.MarshalIndent(payload, "", "  ")
		os.Stdout.Write(out)
		os.Exit(0)
		return nil
	}
	llm.StreamSimple(context.Background(), s.model, s.ctx, s.opts).Result()
}
