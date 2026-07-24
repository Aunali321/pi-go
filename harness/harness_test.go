package harness

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

type weatherArgs struct {
	City string `json:"city"`
}

// TestHarnessEndToEndOpenRouter drives a full AgentHarness turn against
// OpenRouter: the model must call a tool, answer, and the session must persist
// the whole exchange to JSONL. Set OPENROUTER_API_KEY to run.
func TestHarnessEndToEndOpenRouter(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	fsenv := env.NewOSEnv(t.TempDir())
	repo := session.NewJsonlSessionRepo(fsenv, t.TempDir())
	sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: fsenv.Cwd()})
	if err != nil {
		t.Fatal(err)
	}
	meta := sess.GetMetadata().(session.JsonlSessionMetadata)

	modelID := os.Getenv("PI_TEST_MODEL")
	if modelID == "" {
		modelID = "anthropic/claude-3.5-haiku"
	}
	model := &llm.Model{
		ID:            modelID,
		Input:         []llm.InputModality{llm.InputText},
		ContextWindow: 128000,
		MaxTokens:     1024,
		Cost:          llm.Pricing{Input: 0.15, Output: 0.6},
	}

	var (
		mu         sync.Mutex
		toolCity   string
		toolCalled bool
	)
	weather := agent.NewTool(agent.ToolDef[weatherArgs]{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
		Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
			mu.Lock()
			toolCalled = true
			toolCity = args.City
			mu.Unlock()
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "12°C, overcast"}}}, nil
		},
	})

	h, err := NewAgentHarness(AgentHarnessOptions{
		Session:      sess,
		Tools:        []agent.Tool{weather},
		SystemPrompt: "You answer weather questions using the get_weather tool.",
		Model:        model,
		Stream: func(ctx context.Context, m *llm.Model, reqCtx *llm.Context, opts *llm.StreamOptions) *llm.Stream {
			o := *opts
			o.APIKey = key
			return llm.StreamSimple(ctx, m, reqCtx, &o)
		},
		StreamOptions: HarnessStreamOptions{CacheRetention: llm.CacheShort},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	sawToolCall := false
	h.Subscribe(func(event any, _ context.Context) {
		switch e := event.(type) {
		case agent.MessageUpdate:
			if d, ok := e.Event.(llm.TextDeltaEvent); ok {
				text.WriteString(d.Delta)
			}
		case agent.ToolExecutionStart:
			sawToolCall = true
		}
	})

	final, err := h.Prompt(ctx, "What is the weather in Paris?")
	if err != nil {
		t.Fatalf("prompt failed: %v", err)
	}
	if final.StopReason != llm.StopEnd {
		t.Fatalf("expected normal stop, got %q (%s)", final.StopReason, final.ErrorMessage)
	}
	if !toolCalled || !sawToolCall {
		t.Fatal("model did not call the weather tool")
	}
	if !strings.Contains(strings.ToLower(toolCity), "paris") {
		t.Fatalf("tool got unexpected city %q", toolCity)
	}
	if final.Usage.TotalTokens == 0 {
		t.Error("expected non-zero usage")
	}

	// Reopen the persisted session and confirm the full exchange survived.
	reopened, err := repo.Open(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	sctx, err := reopened.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, m := range sctx.Messages {
		roles[m.Role()]++
	}
	if roles["user"] < 1 || roles["assistant"] < 2 || roles["toolResult"] < 1 {
		t.Fatalf("session did not persist full exchange: %+v", roles)
	}
	t.Logf("persisted roles: %+v; answer: %q", roles, strings.TrimSpace(text.String()))
}
