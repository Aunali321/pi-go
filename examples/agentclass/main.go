// Stateful Agent example: the Agent owns the transcript, emits events, and
// exposes Prompt/State. Requires OPENROUTER_API_KEY.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aunali321/pi-go/agent"
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

	weather := agent.NewTool(agent.ToolDef[weatherArgs]{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
		Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "18°C, light rain in " + args.City}}}, nil
		},
	})

	a := agent.NewAgent(agent.AgentOptions{
		SystemPrompt: "You are concise. Use the weather tool when asked about weather.",
		Model: &llm.Model{
			ID: "anthropic/claude-3.5-haiku", Input: []llm.InputModality{llm.InputText},
			ContextWindow: 200000, MaxTokens: 1024, Cost: llm.Pricing{Input: 0.8, Output: 4},
		},
		Tools:   []agent.Tool{weather},
		Options: llm.StreamOptions{APIKey: key, CacheRetention: llm.CacheShort},
	})

	a.Subscribe(func(e agent.Event, _ context.Context) {
		switch ev := e.(type) {
		case agent.MessageUpdate:
			if d, ok := ev.Event.(llm.TextDeltaEvent); ok {
				fmt.Print(d.Delta)
			}
		case agent.ToolExecutionStart:
			fmt.Printf("\n[tool] %s(%v)\n", ev.ToolName, ev.Args)
		}
	})

	if err := a.Prompt(context.Background(), "What's the weather in Berlin?"); err != nil {
		fmt.Fprintln(os.Stderr, "prompt failed:", err)
		os.Exit(1)
	}
	fmt.Printf("\n\ntranscript holds %d messages\n", len(a.State().Messages))
}
