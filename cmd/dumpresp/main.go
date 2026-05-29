package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/aunali321/pi-go/llm"
)

func main() {
	fixture := os.Args[1]
	model := &llm.Model{
		ID: "test-model", Name: "test-model", Provider: "openrouter",
		BaseURL: "http://127.0.0.1:8766/v1", Reasoning: true, Input: []llm.InputModality{llm.InputText},
		Cost: llm.Pricing{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}, ContextWindow: 200000, MaxTokens: 1024,
		Headers: map[string]string{"x-fixture": fixture},
	}
	reqCtx := &llm.Context{SystemPrompt: "s", Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "hi"}}}}}
	result := llm.StreamSimple(context.Background(), model, reqCtx, &llm.StreamOptions{APIKey: "dummy"}).Result()

	raw, _ := json.Marshal(result)
	var m map[string]any
	json.Unmarshal(raw, &m)
	delete(m, "timestamp")
	out, _ := json.MarshalIndent(m, "", "  ")
	os.Stdout.Write(out)
}
