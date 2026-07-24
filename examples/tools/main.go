// Multi-tool example: two tools, parallel execution, and an afterToolCall hook
// that observes every tool result. Requires OPENROUTER_API_KEY.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

type weatherArgs struct {
	City string `json:"city"`
}

type addArgs struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

func main() {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "set OPENROUTER_API_KEY to run this example")
		os.Exit(1)
	}
	ctx := context.Background()

	weather := agent.NewTool(agent.ToolDef[weatherArgs]{
		Name: "get_weather", Description: "Get the current weather for a city.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
		Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "21°C in " + args.City}}}, nil
		},
	})
	add := agent.NewTool(agent.ToolDef[addArgs]{
		Name: "add", Description: "Add two numbers.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}}, "required": []string{"a", "b"}},
		Run: func(ctx context.Context, id string, args addArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: fmt.Sprintf("%g", args.A+args.B)}}}, nil
		},
	})

	repo := session.NewInMemorySessionRepo()
	sess, _ := repo.Create("")

	h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
		Session: sess, Tools: []agent.Tool{weather, add},
		SystemPrompt:  "Use the provided tools. Call each tool that the question requires.",
		Model:         &llm.Model{ID: "anthropic/claude-3.5-haiku", Input: []llm.InputModality{llm.InputText}, MaxTokens: 1024, ContextWindow: 200000},
		StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort},
	})
	if err != nil {
		panic(err)
	}

	// Observe (and could rewrite) every tool result.
	h.OnToolResult(func(e harness.ToolResultEvent) *harness.ToolResultPatch {
		fmt.Printf("[tool result] %s(%v) -> error=%v\n", e.ToolName, e.Input, e.IsError)
		return nil // return a *ToolResultPatch to modify the result
	})

	final, err := h.Prompt(ctx, "What is the weather in Rome, and what is 19 plus 23?")
	if err != nil {
		panic(err)
	}
	for _, c := range final.Content {
		if t, ok := c.(*llm.Text); ok {
			fmt.Println("\n[answer]", t.Text)
		}
	}
}
