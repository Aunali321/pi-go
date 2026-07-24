package tools

import (
	"context"
	"fmt"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/llm"
)

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// NewWriteTool creates the file write tool bound to an execution environment.
func NewWriteTool(execEnv env.ExecutionEnv) agent.Tool {
	return agent.NewTool(agent.ToolDef[writeArgs]{
		Name:        "write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to the file to write (relative or absolute)"},
				"content": map[string]any{"type": "string", "description": "Content to write to the file"},
			},
			"required": []string{"path", "content"},
		},
		Run: func(ctx context.Context, callID string, args writeArgs, onUpdate agent.UpdateFunc) (agent.ToolResult, error) {
			absolutePath, err := ResolveToolPath(ctx, execEnv, args.Path)
			if err != nil {
				return agent.ToolResult{}, err
			}
			return withFileMutationQueue(ctx, execEnv, absolutePath, func() (agent.ToolResult, error) {
				if ctx.Err() != nil {
					return agent.ToolResult{}, fmt.Errorf("Operation aborted")
				}
				if err := execEnv.WriteFile(ctx, absolutePath, []byte(args.Content)); err != nil {
					return agent.ToolResult{}, err
				}
				if ctx.Err() != nil {
					return agent.ToolResult{}, fmt.Errorf("Operation aborted")
				}
				text := fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), args.Path)
				return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: text}}}, nil
			})
		},
	})
}
