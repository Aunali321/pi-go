package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/llm"
)

type editArgs struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// UnmarshalJSON accepts compatibility shims for raw tool-call arguments: an
// edits array serialized as a JSON string, and legacy top-level
// oldText/newText fields appended as a final edit.
func (a *editArgs) UnmarshalJSON(data []byte) error {
	var raw struct {
		Path    string          `json:"path"`
		Edits   json.RawMessage `json:"edits"`
		OldText *string         `json:"oldText"`
		NewText *string         `json:"newText"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.Path = raw.Path
	a.Edits = nil

	edits := raw.Edits
	if len(edits) > 0 && edits[0] == '"' {
		var s string
		if json.Unmarshal(edits, &s) == nil {
			var parsed []Edit
			if json.Unmarshal([]byte(s), &parsed) == nil {
				a.Edits = parsed
				edits = nil
			}
		}
	}
	if len(edits) > 0 {
		if err := json.Unmarshal(edits, &a.Edits); err != nil {
			return err
		}
	}
	if raw.OldText != nil && raw.NewText != nil {
		a.Edits = append(a.Edits, Edit{OldText: *raw.OldText, NewText: *raw.NewText})
	}
	return nil
}

// EditToolDetails describe the applied edit for logs and UI rendering.
type EditToolDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

func editAccessError(path string, err error) error {
	if fe, ok := err.(*env.FileError); ok {
		return fmt.Errorf("Could not edit file: %s. Error code: %s.", path, fe.Code)
	}
	return err
}

// NewEditTool creates the exact-text-replacement edit tool bound to an
// execution environment.
func NewEditTool(execEnv env.ExecutionEnv) agent.Tool {
	return agent.NewTool(agent.ToolDef[editArgs]{
		Name: "edit",
		Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. " +
			"If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. " +
			"Do not include large unchanged regions just to connect distant changes.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file to edit (relative or absolute)"},
				"edits": map[string]any{
					"type":        "array",
					"description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": map[string]any{"type": "string", "description": "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
							"newText": map[string]any{"type": "string", "description": "Replacement text for this targeted edit."},
						},
						"required": []string{"oldText", "newText"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
		Run: func(ctx context.Context, callID string, args editArgs, onUpdate agent.UpdateFunc) (agent.ToolResult, error) {
			if len(args.Edits) == 0 {
				return agent.ToolResult{}, fmt.Errorf("Edit tool input is invalid. edits must contain at least one replacement.")
			}
			absolutePath, err := ResolveToolPath(ctx, execEnv, args.Path)
			if err != nil {
				return agent.ToolResult{}, err
			}
			return withFileMutationQueue(ctx, execEnv, absolutePath, func() (agent.ToolResult, error) {
				if ctx.Err() != nil {
					return agent.ToolResult{}, fmt.Errorf("Operation aborted")
				}
				info, err := execEnv.FileInfo(ctx, absolutePath)
				if err != nil {
					return agent.ToolResult{}, editAccessError(args.Path, err)
				}
				if info.Kind != env.KindFile && info.Kind != env.KindSymlink {
					return agent.ToolResult{}, fmt.Errorf("Could not edit file: %s. Path is not a file.", args.Path)
				}

				fileContent, err := execEnv.ReadTextFile(ctx, absolutePath)
				if err != nil {
					return agent.ToolResult{}, editAccessError(args.Path, err)
				}
				if ctx.Err() != nil {
					return agent.ToolResult{}, fmt.Errorf("Operation aborted")
				}

				bom, content := stripBOM(fileContent)
				originalEnding := detectLineEnding(content)
				normalizedContent := normalizeToLF(content)
				baseContent, newContent, err := applyEditsToNormalizedContent(normalizedContent, args.Edits, args.Path)
				if err != nil {
					return agent.ToolResult{}, err
				}
				if ctx.Err() != nil {
					return agent.ToolResult{}, fmt.Errorf("Operation aborted")
				}

				finalContent := bom + restoreLineEndings(newContent, originalEnding)
				if err := execEnv.WriteFile(ctx, absolutePath, []byte(finalContent)); err != nil {
					return agent.ToolResult{}, editAccessError(args.Path, err)
				}
				if ctx.Err() != nil {
					return agent.ToolResult{}, fmt.Errorf("Operation aborted")
				}

				diff, firstChangedLine := generateDiffString(baseContent, newContent, 4)
				return agent.ToolResult{
					Content: []llm.Content{&llm.Text{Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(args.Edits), args.Path)}},
					Details: &EditToolDetails{
						Diff:             diff,
						Patch:            generateUnifiedPatch(args.Path, baseContent, newContent, 4),
						FirstChangedLine: firstChangedLine,
					},
				}, nil
			})
		},
	})
}
