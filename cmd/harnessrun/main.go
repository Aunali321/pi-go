package main

import (
	"context"
	"encoding/json"
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
	ctx := context.Background()
	env := env.NewOSEnv("")
	repo := session.NewInMemorySessionRepo()
	session, err := repo.Create("")
	if err != nil {
		panic(err)
	}

	model := &llm.Model{
		ID: "anthropic/claude-3.5-haiku", Name: "Claude 3.5 Haiku",
		Provider: "openrouter", BaseURL: "http://127.0.0.1:8765/v1", Reasoning: false,
		Input:         []llm.InputModality{llm.InputText},
		Cost:          llm.Pricing{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1},
		ContextWindow: 200000, MaxTokens: 1024,
	}

	tool := agent.NewTool(agent.ToolDef[weatherArgs]{
		Name: "get_weather", Label: "Weather", Description: "Get the current weather for a city.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
		Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "12C overcast"}}, Details: map[string]any{"city": args.City}}, nil
		},
	})

	h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
		Env: env, Session: session, Tools: []agent.Tool{tool}, Model: model,
		SystemPrompt:  "You are a helpful assistant.",
		GetAuth:       func(m *llm.Model) (*harness.Auth, error) { return &harness.Auth{APIKey: "dummy"}, nil },
		StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort, Headers: map[string]string{"x-client": "go"}},
	})
	if err != nil {
		panic(err)
	}

	final, err := h.Prompt(ctx, "What's the weather in Paris?")
	if err != nil {
		panic(err)
	}
	sctx, _ := session.BuildContext()
	roles := make([]string, len(sctx.Messages))
	for i, m := range sctx.Messages {
		roles[i] = m.Role()
	}
	out, _ := json.MarshalIndent(map[string]any{"stopReason": final.StopReason, "errorMessage": final.ErrorMessage, "roles": roles}, "", "  ")
	os.Stdout.Write(out)
}
