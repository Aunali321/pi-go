package tools

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aunali321/pi-go/harness/env"
)

// ShellCaptureProgress is a snapshot of captured output while a command runs.
type ShellCaptureProgress struct {
	Output         string
	Truncation     TruncationResult
	FullOutputPath string
	LastLineBytes  int
}

// ShellCaptureOptions configures ExecuteShellWithCapture.
type ShellCaptureOptions struct {
	Cwd        string
	Env        map[string]string
	InheritEnv *bool
	Timeout    float64 // seconds; 0 = none
	// OnChunk receives each sanitized output chunk plus a progress getter.
	OnChunk func(chunk string, getProgress func() ShellCaptureProgress)
	// ReturnExecutionErrors returns shell execution failures with captured
	// output instead of as an error.
	ReturnExecutionErrors bool
}

// ShellCaptureResult is the final captured output of a command.
type ShellCaptureResult struct {
	ShellCaptureProgress
	// ExitCode is nil when the command was cancelled or failed to run.
	ExitCode       *int
	Cancelled      bool
	Truncated      bool
	ExecutionError *env.ExecutionError
}

// SanitizeBinaryOutput drops control characters (except tab, LF and CR) and
// interlinear annotation characters from shell output.
func SanitizeBinaryOutput(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == 0x09 || r == 0x0a || r == 0x0d {
			b.WriteRune(r)
			continue
		}
		if r <= 0x1f {
			continue
		}
		if r >= 0xfff9 && r <= 0xfffb {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trimToLastBytes keeps the last maxBytes of text without splitting a UTF-8
// sequence.
func trimToLastBytes(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

type shellCapture struct {
	execEnv env.ExecutionEnv
	options *ShellCaptureOptions

	mu              sync.Mutex
	tailOutput      string
	totalBytes      int
	completedLines  int
	hasOpenLine     bool
	currentLineByte int
	fullOutputPath  string
	fullOutputAsked bool
	acceptingOutput bool
	captureError    *env.ExecutionError
}

func (c *shellCapture) progressLocked() ShellCaptureProgress {
	tailTruncation := TruncateTail(c.tailOutput, TruncationOptions{})
	totalLines := c.completedLines
	if c.hasOpenLine {
		totalLines++
	}
	truncated := totalLines > DefaultMaxLines || c.totalBytes > DefaultMaxBytes
	truncation := tailTruncation
	truncation.Truncated = truncated
	truncation.TruncatedBy = ""
	if truncated {
		truncation.TruncatedBy = tailTruncation.TruncatedBy
		if truncation.TruncatedBy == "" {
			if c.totalBytes > DefaultMaxBytes {
				truncation.TruncatedBy = "bytes"
			} else {
				truncation.TruncatedBy = "lines"
			}
		}
	}
	truncation.TotalLines = totalLines
	truncation.TotalBytes = c.totalBytes

	output := c.tailOutput
	if truncated {
		output = truncation.Content
	}
	return ShellCaptureProgress{
		Output:         output,
		Truncation:     truncation,
		FullOutputPath: c.fullOutputPath,
		LastLineBytes:  c.currentLineByte,
	}
}

func (c *shellCapture) progress() ShellCaptureProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.progressLocked()
}

func (c *shellCapture) ensureFullOutputFile(ctx context.Context, initialContent string) {
	if c.fullOutputAsked || c.captureError != nil {
		return
	}
	c.fullOutputAsked = true
	path, err := c.execEnv.CreateTempFile(ctx, "bash-", ".log")
	if err != nil {
		c.captureError = toExecutionError(err)
		return
	}
	c.fullOutputPath = path
	if err := c.execEnv.AppendFile(ctx, path, []byte(initialContent)); err != nil {
		c.captureError = toExecutionError(err)
	}
}

func (c *shellCapture) appendFullOutput(ctx context.Context, text string) {
	if !c.fullOutputAsked || c.captureError != nil {
		return
	}
	if c.fullOutputPath == "" {
		c.captureError = &env.ExecutionError{Code: env.ExecUnknown, Msg: "Full output path was not created"}
		return
	}
	if err := c.execEnv.AppendFile(ctx, c.fullOutputPath, []byte(text)); err != nil {
		c.captureError = toExecutionError(err)
	}
}

func (c *shellCapture) onChunk(ctx context.Context, chunk string) {
	c.mu.Lock()
	if !c.acceptingOutput {
		c.mu.Unlock()
		return
	}
	text := strings.ReplaceAll(SanitizeBinaryOutput(chunk), "\r", "")
	textBytes := len(text)
	c.totalBytes += textBytes
	c.completedLines += strings.Count(text, "\n")
	if lastNewline := strings.LastIndexByte(text, '\n'); lastNewline >= 0 {
		trailing := text[lastNewline+1:]
		c.currentLineByte = len(trailing)
		c.hasOpenLine = trailing != ""
	} else if text != "" {
		c.currentLineByte += textBytes
		c.hasOpenLine = true
	}

	c.tailOutput += text
	totalLines := c.completedLines
	if c.hasOpenLine {
		totalLines++
	}
	if (c.totalBytes > DefaultMaxBytes || totalLines > DefaultMaxLines) && !c.fullOutputAsked {
		c.ensureFullOutputFile(ctx, c.tailOutput)
	} else if c.fullOutputAsked {
		c.appendFullOutput(ctx, text)
	}
	c.tailOutput = trimToLastBytes(c.tailOutput, DefaultMaxBytes*2)
	onChunk := c.options.OnChunk
	c.mu.Unlock()

	if onChunk != nil {
		onChunk(text, c.progress)
	}
}

func toExecutionError(err error) *env.ExecutionError {
	if ee, ok := err.(*env.ExecutionError); ok {
		return ee
	}
	return &env.ExecutionError{Code: env.ExecUnknown, Msg: err.Error(), Err: err}
}

// ExecuteShellWithCapture runs a command capturing merged stdout/stderr with
// tail truncation. When output exceeds the limits, the full output is spooled
// to a temp file.
func ExecuteShellWithCapture(ctx context.Context, execEnv env.ExecutionEnv, command string, options *ShellCaptureOptions) (*ShellCaptureResult, error) {
	if options == nil {
		options = &ShellCaptureOptions{}
	}
	capture := &shellCapture{execEnv: execEnv, options: options, acceptingOutput: true}

	execOpts := env.ExecOptions{
		Cwd:        options.Cwd,
		Env:        options.Env,
		InheritEnv: options.InheritEnv,
		Timeout:    options.Timeout,
		OnStdout:   func(chunk string) { capture.onChunk(ctx, chunk) },
		OnStderr:   func(chunk string) { capture.onChunk(ctx, chunk) },
	}
	result, execErr := execEnv.Exec(ctx, command, execOpts)

	capture.mu.Lock()
	capture.acceptingOutput = false
	progress := capture.progressLocked()
	tail := capture.tailOutput
	capture.mu.Unlock()

	if progress.Truncation.Truncated && !capture.fullOutputAsked {
		capture.mu.Lock()
		capture.ensureFullOutputFile(ctx, tail)
		capture.mu.Unlock()
	}
	if capture.captureError != nil {
		return nil, capture.captureError
	}
	progress = capture.progress()

	if execErr != nil {
		ee := toExecutionError(execErr)
		if ee.Code == env.ExecAborted || ctx.Err() != nil {
			return &ShellCaptureResult{
				ShellCaptureProgress: progress,
				Cancelled:            true,
				Truncated:            progress.Truncation.Truncated,
			}, nil
		}
		if options.ReturnExecutionErrors {
			return &ShellCaptureResult{
				ShellCaptureProgress: progress,
				Truncated:            progress.Truncation.Truncated,
				ExecutionError:       ee,
			}, nil
		}
		return nil, ee
	}

	cancelled := ctx.Err() != nil
	res := &ShellCaptureResult{
		ShellCaptureProgress: progress,
		Cancelled:            cancelled,
		Truncated:            progress.Truncation.Truncated,
	}
	if !cancelled {
		exitCode := result.ExitCode
		res.ExitCode = &exitCode
	}
	return res, nil
}
