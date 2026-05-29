// Vision example: send a local image to a vision-capable model and print its
// description. Usage: vision <image.png>  (requires OPENROUTER_API_KEY).
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/llm"
)

func main() {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" || len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: OPENROUTER_API_KEY=... vision <image.png>")
		os.Exit(1)
	}

	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	image := &llm.Image{Data: base64.StdEncoding.EncodeToString(raw), MimeType: "image/png"}

	modelID := os.Getenv("PI_TEST_MODEL")
	if modelID == "" {
		modelID = "openai/gpt-4o-mini" // a vision-capable model
	}
	model := &llm.Model{
		ID: modelID, Input: []llm.InputModality{llm.InputText, llm.InputImage},
		ContextWindow: 128000, MaxTokens: 1024,
	}

	ctx := &agent.Context{SystemPrompt: "Describe images concisely."}
	cfg := &agent.Config{Model: model, Options: llm.StreamOptions{APIKey: key}}

	emit := func(e agent.Event) {
		if u, ok := e.(agent.MessageUpdate); ok {
			if d, ok := u.Event.(llm.TextDeltaEvent); ok {
				fmt.Print(d.Delta)
			}
		}
	}

	prompt := &llm.UserMessage{Content: []llm.Content{
		&llm.Text{Text: "Describe this image in one sentence."},
		image,
	}}
	agent.Run(context.Background(), []agent.AgentMessage{prompt}, ctx, cfg, emit)
	fmt.Println()
}
