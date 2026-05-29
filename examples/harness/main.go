// Harness example: run a turn through AgentHarness with on-disk JSONL session
// persistence, then reopen the session and print the reconstructed transcript.
// Requires OPENROUTER_API_KEY.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

type weatherArgs struct {
	City string `json:"city"`
}

func main() {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "set OPENROUTER_API_KEY to run this example")
		os.Exit(1)
	}
	ctx := context.Background()

	fsenv := env.NewOSEnv("")
	sessionsDir, _ := os.MkdirTemp("", "pi-sessions-")
	repo := session.NewJsonlSessionRepo(fsenv, sessionsDir)
	sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: fsenv.Cwd()})
	if err != nil {
		panic(err)
	}
	meta := sess.GetMetadata().(session.JsonlSessionMetadata)

	weather := agent.NewTool(agent.ToolDef[weatherArgs]{
		Name: "get_weather", Description: "Get the current weather for a city.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
		Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "12°C, overcast in " + args.City}}}, nil
		},
	})

	h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
		Env: fsenv, Session: sess, Tools: []agent.Tool{weather},
		SystemPrompt:  "Answer weather questions using the get_weather tool.",
		Model:         &llm.Model{ID: "anthropic/claude-3.5-haiku", Input: []llm.InputModality{llm.InputText}, MaxTokens: 1024, ContextWindow: 200000},
		GetAuth:       func(m *llm.Model) (*harness.Auth, error) { return &harness.Auth{APIKey: key}, nil },
		StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort},
	})
	if err != nil {
		panic(err)
	}

	if _, err := h.Prompt(ctx, "What's the weather in Paris?"); err != nil {
		panic(err)
	}
	fmt.Println("session written to:", meta.Path)

	// Reopen from disk and replay the persisted transcript.
	reopened, err := repo.Open(ctx, meta)
	if err != nil {
		panic(err)
	}
	sctx, err := reopened.BuildContext()
	if err != nil {
		panic(err)
	}
	fmt.Println("\nreconstructed transcript:")
	for _, m := range sctx.Messages {
		fmt.Printf("  %-11s %s\n", m.Role(), firstText(m))
	}
}

func firstText(m agent.AgentMessage) string {
	var content []llm.Content
	switch v := m.(type) {
	case *llm.UserMessage:
		content = v.Content
	case *llm.AssistantMessage:
		content = v.Content
	case *llm.ToolResultMessage:
		content = v.Content
	}
	for _, c := range content {
		if t, ok := c.(*llm.Text); ok && t.Text != "" {
			return t.Text
		}
	}
	return ""
}
