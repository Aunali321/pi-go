package llm

import "testing"

func TestParseStreamingJSON(t *testing.T) {
	cases := map[string]map[string]any{
		`{"city":"Tokyo"}`: {"city": "Tokyo"},
		`{"a":1,"b":2`:     {"a": float64(1), "b": float64(2)},
		``:                 {},
		`{`:                {},
	}
	for in, want := range cases {
		got := parseStreamingJSON(in)
		if len(got) != len(want) {
			t.Errorf("parseStreamingJSON(%q) len=%d want=%d (%v)", in, len(got), len(want), got)
			continue
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("parseStreamingJSON(%q)[%q]=%v want %v", in, k, got[k], v)
			}
		}
	}
}

func TestBuildParamsAnthropicCacheAndReasoning(t *testing.T) {
	model := &Model{ID: "anthropic/claude-sonnet-4.5", Reasoning: true, Input: []InputModality{InputText}}
	ctx := &Context{
		SystemPrompt: "be terse",
		Messages:     []Message{TextUser("hello")},
	}
	opts := &StreamOptions{reasoningEffort: ThinkingMedium}
	compat := getCompat(model)

	if compat.cacheControlFormat != CacheControlAnthropic {
		t.Fatalf("expected anthropic cache control for anthropic/* on openrouter, got %q", compat.cacheControlFormat)
	}
	if compat.thinkingFormat != ThinkingFormatOpenRouter {
		t.Fatalf("expected openrouter thinking format, got %q", compat.thinkingFormat)
	}

	params, err := buildParams(model, ctx, opts, compat, CacheShort, nil)
	if err != nil {
		t.Fatal(err)
	}

	reasoning, ok := params["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("expected reasoning.effort=medium, got %v", params["reasoning"])
	}

	messages := params["messages"].([]map[string]any)
	sys := messages[0]
	parts, ok := sys["content"].([]map[string]any)
	if !ok || parts[len(parts)-1]["cache_control"] == nil {
		t.Fatalf("expected cache_control on system prompt, got %v", sys["content"])
	}

	last := messages[len(messages)-1]
	lastParts, ok := last["content"].([]map[string]any)
	if !ok || lastParts[len(lastParts)-1]["cache_control"] == nil {
		t.Fatalf("expected cache_control on last user message, got %v", last["content"])
	}
}

func TestConvertToolResultsAndCalls(t *testing.T) {
	model := &Model{ID: "anthropic/claude", Input: []InputModality{InputText}}
	assistant := &AssistantMessage{
		API:      apiOpenAICompletions,
		Provider: "openrouter",
		Model:    "anthropic/claude",
		Content: []Content{
			&Text{Text: "calling"},
			&ToolCall{ID: "c1", Name: "lookup", Arguments: map[string]any{"q": "x"}},
		},
		StopReason: StopToolUse,
	}
	ctx := &Context{
		Messages: []Message{
			TextUser("hi"),
			assistant,
			&ToolResultMessage{ToolCallID: "c1", ToolName: "lookup", Content: []Content{&Text{Text: "found"}}},
		},
	}
	msgs, err := convertMessages(model, ctx, getCompat(model), nil)
	if err != nil {
		t.Fatal(err)
	}

	var am map[string]any
	for _, m := range msgs {
		if m["role"] == "assistant" {
			am = m
		}
	}
	if am == nil {
		t.Fatal("missing assistant message")
	}
	calls, ok := am["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 || calls[0]["id"] != "c1" {
		t.Fatalf("expected one tool_call with id c1, got %v", am["tool_calls"])
	}

	tool := msgs[len(msgs)-1]
	if tool["role"] != "tool" || tool["tool_call_id"] != "c1" || tool["content"] != "found" {
		t.Fatalf("unexpected tool result message: %v", tool)
	}
}
