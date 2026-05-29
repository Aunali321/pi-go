package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aunali321/pi-go/llm"
)

type weatherArgs struct {
	City string `json:"city"`
}

// TestAgentLoopOpenRouter drives a full agent loop against OpenRouter: the model
// must call the weather tool and then answer using the tool result. Set
// OPENROUTER_API_KEY to run it.
func TestAgentLoopOpenRouter(t *testing.T) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}

	model := &llm.Model{
		ID:            "anthropic/claude-3.5-haiku",
		Name:          "Claude 3.5 Haiku",
		Input:         []llm.InputModality{llm.InputText},
		ContextWindow: 200000,
		MaxTokens:     1024,
		Cost:          llm.Pricing{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1},
	}

	var (
		toolCity   string
		toolCalled bool
	)
	weather := NewTool(ToolDef[weatherArgs]{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"city": map[string]any{"type": "string"}},
			"required":             []string{"city"},
			"additionalProperties": false,
		},
		Run: func(ctx context.Context, callID string, args weatherArgs, onUpdate UpdateFunc) (ToolResult, error) {
			toolCalled = true
			toolCity = args.City
			return ToolResult{
				Content: []llm.Content{&llm.Text{Text: "18°C, light rain"}},
				Details: map[string]any{"city": args.City},
			}, nil
		},
	})

	agentCtx := &Context{
		SystemPrompt: "You answer using the get_weather tool when asked about weather.",
		Tools:        []Tool{weather},
	}
	cfg := &Config{
		Model:   model,
		Options: llm.StreamOptions{CacheRetention: llm.CacheShort, MaxTokens: 1024},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var finalText strings.Builder
	emit := func(e Event) {
		if u, ok := e.(MessageUpdate); ok {
			if d, ok := u.Event.(llm.TextDeltaEvent); ok {
				finalText.WriteString(d.Delta)
			}
		}
	}

	msgs := Run(ctx, []AgentMessage{llm.TextUser("What is the weather in Paris?")}, agentCtx, cfg, emit)

	if !toolCalled {
		t.Fatal("model did not call the weather tool")
	}
	if !strings.Contains(strings.ToLower(toolCity), "paris") {
		t.Fatalf("tool received unexpected city: %q", toolCity)
	}

	last, ok := msgs[len(msgs)-1].(*llm.AssistantMessage)
	if !ok {
		t.Fatalf("expected final message to be assistant, got %T", msgs[len(msgs)-1])
	}
	if last.StopReason != llm.StopEnd {
		t.Fatalf("expected normal stop, got %q (%s)", last.StopReason, last.ErrorMessage)
	}
	if last.Usage.TotalTokens == 0 {
		t.Error("expected non-zero token usage from the provider")
	}
	if strings.TrimSpace(finalText.String()) == "" {
		t.Error("expected a non-empty final answer")
	}
}
