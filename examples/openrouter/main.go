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
	model := &llm.Model{
		ID:            "anthropic/claude-sonnet-4.5",
		Name:          "Claude Sonnet 4.5",
		Reasoning:     true,
		Input:         []llm.InputModality{llm.InputText, llm.InputImage},
		ContextWindow: 200000,
		MaxTokens:     8192,
		Cost:          llm.Pricing{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}

	weather := agent.NewTool(agent.ToolDef[weatherArgs]{
		Name:        "get_weather",
		Description: "Get the current weather for a city.",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
		Run: func(ctx context.Context, callID string, args weatherArgs, onUpdate agent.UpdateFunc) (agent.ToolResult, error) {
			return agent.ToolResult{
				Content: []llm.Content{&llm.Text{Text: fmt.Sprintf("It is 22°C and sunny in %s.", args.City)}},
				Details: map[string]any{"city": args.City, "tempC": 22},
			}, nil
		},
	})

	agentCtx := &agent.Context{
		SystemPrompt: "You are a concise assistant. Use tools when relevant.",
		Tools:        []agent.Tool{weather},
	}

	cfg := &agent.Config{
		Model:     model,
		Reasoning: llm.ThinkingOff,
		Options:   llm.StreamOptions{CacheRetention: llm.CacheShort},
	}

	emit := func(e agent.Event) {
		switch ev := e.(type) {
		case agent.MessageUpdate:
			if d, ok := ev.Event.(llm.TextDeltaEvent); ok {
				fmt.Print(d.Delta)
			}
		case agent.ToolExecutionStart:
			fmt.Printf("\n[tool] %s(%v)\n", ev.ToolName, ev.Args)
		case agent.ToolExecutionEnd:
			fmt.Printf("[tool done] error=%v\n", ev.IsError)
		case agent.AgentEnd:
			fmt.Println("\n[done]")
		}
	}

	if os.Getenv("OPENROUTER_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "set OPENROUTER_API_KEY to run this example")
		os.Exit(1)
	}

	agent.Run(context.Background(),
		[]agent.AgentMessage{llm.TextUser("What's the weather in Tokyo?")},
		agentCtx, cfg, emit)
}
