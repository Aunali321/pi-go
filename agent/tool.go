package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aunali321/pi-go/llm"
)

type ExecutionMode string

const (
	ModeSequential ExecutionMode = "sequential"
	ModeParallel   ExecutionMode = "parallel"
)

// ToolResult is the outcome of a tool execution. Content is returned to the
// model; Details is opaque structured data for logs or UI.
type ToolResult struct {
	Content []llm.Content
	Details any
	// Usage from the tool execution itself, if available. Not used for main
	// LLM context accounting.
	Usage *llm.Usage
	// AddedToolNames lists tools introduced by this result and available from
	// this transcript point onward.
	AddedToolNames []string
	Terminate      bool
}

// UpdateFunc streams partial results during a long-running tool execution.
type UpdateFunc func(ToolResult)

// Tool is a callable the agent can invoke. Build instances with NewTool.
type Tool interface {
	Name() string
	Description() string
	Label() string
	Schema() map[string]any
	Mode() ExecutionMode
	Execute(ctx context.Context, callID string, args json.RawMessage, onUpdate UpdateFunc) (ToolResult, error)
}

// ToolDef defines a tool with strongly-typed arguments A. Raw JSON arguments
// from the model are decoded into A before Run is called; a decode failure
// surfaces to the model as an error tool result.
type ToolDef[A any] struct {
	Name        string
	Description string
	Label       string
	Schema      map[string]any
	Mode        ExecutionMode
	Run         func(ctx context.Context, callID string, args A, onUpdate UpdateFunc) (ToolResult, error)
}

func NewTool[A any](def ToolDef[A]) Tool {
	if def.Label == "" {
		def.Label = def.Name
	}
	return &typedTool[A]{def}
}

type typedTool[A any] struct{ def ToolDef[A] }

func (t *typedTool[A]) Name() string           { return t.def.Name }
func (t *typedTool[A]) Description() string    { return t.def.Description }
func (t *typedTool[A]) Label() string          { return t.def.Label }
func (t *typedTool[A]) Schema() map[string]any { return t.def.Schema }
func (t *typedTool[A]) Mode() ExecutionMode    { return t.def.Mode }

func (t *typedTool[A]) Execute(ctx context.Context, callID string, raw json.RawMessage, onUpdate UpdateFunc) (ToolResult, error) {
	var args A
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return ToolResult{}, fmt.Errorf("invalid arguments for tool %q: %w", t.def.Name, err)
		}
	}
	return t.def.Run(ctx, callID, args, onUpdate)
}

func findTool(tools []Tool, name string) Tool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// ConstrainedSampler is optionally implemented by tools that request
// provider-side constrained sampling for their schema.
type ConstrainedSampler interface {
	ConstrainedSampling() *llm.ConstrainedSampling
}

func toLLMTools(tools []Tool) []llm.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]llm.Tool, len(tools))
	for i, t := range tools {
		out[i] = llm.Tool{Name: t.Name(), Description: t.Description(), Parameters: t.Schema()}
		if cs, ok := t.(ConstrainedSampler); ok {
			out[i].ConstrainedSampling = cs.ConstrainedSampling()
		}
	}
	return out
}

func textResult(msg string) ToolResult {
	return ToolResult{Content: []llm.Content{&llm.Text{Text: msg}}, Details: map[string]any{}}
}
