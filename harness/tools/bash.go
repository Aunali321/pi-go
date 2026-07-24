package tools

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/llm"
)

const bashUpdateThrottle = 100 * time.Millisecond

// BashToolDetails are attached to bash tool results when output is truncated.
type BashToolDetails struct {
	Truncation     *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string            `json:"fullOutputPath,omitempty"`
}

// BashExecution is the mutable command a BashPrepare hook may adjust before
// it runs.
type BashExecution struct {
	Command    string
	Cwd        string
	Env        map[string]string
	InheritEnv bool
}

// BashPrepare adjusts a command before execution.
type BashPrepare func(ctx context.Context, execution *BashExecution) error

// BashToolOptions configures NewBashTool.
type BashToolOptions struct {
	CommandPrefix string
	Prepare       BashPrepare
}

type bashArgs struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout"`
}

func validateBashTimeout(timeout *float64) (float64, error) {
	if timeout == nil {
		return 0, nil
	}
	if math.IsInf(*timeout, 0) || math.IsNaN(*timeout) || *timeout <= 0 {
		return 0, fmt.Errorf("Invalid timeout: must be a finite number of seconds")
	}
	return *timeout, nil
}

// formatJSNumber renders a float the way JS string interpolation does
// (integers without a decimal point).
func formatJSNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// NewBashTool creates the bash execution tool bound to an execution
// environment.
func NewBashTool(execEnv env.ExecutionEnv, options *BashToolOptions) agent.Tool {
	if options == nil {
		options = &BashToolOptions{}
	}
	return agent.NewTool(agent.ToolDef[bashArgs]{
		Name: "bash",
		Description: fmt.Sprintf(
			"Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
			DefaultMaxLines, DefaultMaxBytes/1024),
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to execute"},
				"timeout": map[string]any{"type": "number", "description": "Timeout in seconds (optional, no default timeout)"},
			},
			"required": []string{"command"},
		},
		Run: func(ctx context.Context, callID string, args bashArgs, onUpdate agent.UpdateFunc) (agent.ToolResult, error) {
			timeout, err := validateBashTimeout(args.Timeout)
			if err != nil {
				return agent.ToolResult{}, err
			}

			execution := &BashExecution{
				Command:    args.Command,
				Cwd:        execEnv.Cwd(),
				Env:        map[string]string{},
				InheritEnv: true,
			}
			if options.CommandPrefix != "" {
				execution.Command = options.CommandPrefix + "\n" + args.Command
			}
			if options.Prepare != nil {
				if err := options.Prepare(ctx, execution); err != nil {
					return agent.ToolResult{}, err
				}
			}

			updates := newThrottledUpdates(onUpdate)
			defer updates.stop()
			if onUpdate != nil {
				onUpdate(agent.ToolResult{})
			}

			capture, err := ExecuteShellWithCapture(ctx, execEnv, execution.Command, &ShellCaptureOptions{
				Cwd:                   execution.Cwd,
				Env:                   execution.Env,
				InheritEnv:            &execution.InheritEnv,
				Timeout:               timeout,
				ReturnExecutionErrors: true,
				OnChunk: func(chunk string, getProgress func() ShellCaptureProgress) {
					updates.schedule(getProgress)
				},
			})
			if err != nil {
				return agent.ToolResult{}, err
			}
			updates.flush(func() ShellCaptureProgress { return capture.ShellCaptureProgress })

			outputText := capture.Output
			var details any
			if capture.Truncation.Truncated {
				t := capture.Truncation
				details = &BashToolDetails{Truncation: &t, FullOutputPath: capture.FullOutputPath}
				startLine := t.TotalLines - t.OutputLines + 1
				endLine := t.TotalLines
				switch {
				case t.LastLinePartial:
					outputText += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
						FormatSize(t.OutputBytes), endLine, FormatSize(capture.LastLineBytes), capture.FullOutputPath)
				case t.TruncatedBy == "lines":
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
						startLine, endLine, t.TotalLines, capture.FullOutputPath)
				default:
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
						startLine, endLine, t.TotalLines, FormatSize(DefaultMaxBytes), capture.FullOutputPath)
				}
			}

			appendStatus := func(status string) string {
				if outputText == "" {
					return status
				}
				return outputText + "\n\n" + status
			}
			if capture.Cancelled {
				return agent.ToolResult{}, fmt.Errorf("%s", appendStatus("Command aborted"))
			}
			if ee := capture.ExecutionError; ee != nil {
				if ee.Code == env.ExecTimeout {
					return agent.ToolResult{}, fmt.Errorf("%s", appendStatus(
						fmt.Sprintf("Command timed out after %s seconds", formatJSNumber(timeout))))
				}
				return agent.ToolResult{}, ee
			}
			if capture.ExitCode != nil && *capture.ExitCode != 0 {
				return agent.ToolResult{}, fmt.Errorf("%s", appendStatus(
					fmt.Sprintf("Command exited with code %d", *capture.ExitCode)))
			}
			if outputText == "" {
				outputText = "(no output)"
			}
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: outputText}}, Details: details}, nil
		},
	})
}

// throttledUpdates rate-limits streaming bash output updates.
type throttledUpdates struct {
	onUpdate agent.UpdateFunc

	mu           sync.Mutex
	timer        *time.Timer
	dirty        bool
	lastUpdateAt time.Time
	getProgress  func() ShellCaptureProgress
	stopped      bool
}

func newThrottledUpdates(onUpdate agent.UpdateFunc) *throttledUpdates {
	return &throttledUpdates{onUpdate: onUpdate}
}

func (t *throttledUpdates) emitLocked() {
	if t.onUpdate == nil || !t.dirty || t.getProgress == nil {
		return
	}
	t.dirty = false
	t.lastUpdateAt = time.Now()
	progress := t.getProgress()
	var details any
	if progress.Truncation.Truncated || progress.FullOutputPath != "" {
		d := &BashToolDetails{FullOutputPath: progress.FullOutputPath}
		if progress.Truncation.Truncated {
			tr := progress.Truncation
			d.Truncation = &tr
		}
		details = d
	}
	t.onUpdate(agent.ToolResult{Content: []llm.Content{&llm.Text{Text: progress.Output}}, Details: details})
}

func (t *throttledUpdates) schedule(getProgress func() ShellCaptureProgress) {
	if t.onUpdate == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.getProgress = getProgress
	t.dirty = true
	delay := bashUpdateThrottle - time.Since(t.lastUpdateAt)
	if delay <= 0 {
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		t.emitLocked()
		return
	}
	if t.timer == nil {
		t.timer = time.AfterFunc(delay, func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.timer = nil
			if !t.stopped {
				t.emitLocked()
			}
		})
	}
}

func (t *throttledUpdates) flush(getProgress func() ShellCaptureProgress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.getProgress = getProgress
	t.dirty = true
	t.emitLocked()
}

func (t *throttledUpdates) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}
