package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/llm"
)

type readArgs struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset"`
	Limit  *int   `json:"limit"`
}

// ReadToolDetails are attached to read tool results when output is truncated.
type ReadToolDetails struct {
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// ReadImageResult is the outcome of an injected image processor.
type ReadImageResult struct {
	OK       bool
	Data     string
	MimeType string
	Hints    []string
	// Message describes why processing failed when OK is false.
	Message string
}

// ReadImageProcessor converts or resizes image files before attachment.
type ReadImageProcessor func(ctx context.Context, data []byte, mimeType string, autoResize bool) (ReadImageResult, error)

// ReadToolOptions configures NewReadTool.
type ReadToolOptions struct {
	// AutoResizeImages controls whether an injected image processor should
	// resize images. Nil defaults to true.
	AutoResizeImages *bool
	ImageProcessor   ReadImageProcessor
}

// NewReadTool creates the file read tool bound to an execution environment.
func NewReadTool(execEnv env.ExecutionEnv, options *ReadToolOptions) agent.Tool {
	if options == nil {
		options = &ReadToolOptions{}
	}
	return agent.NewTool(agent.ToolDef[readArgs]{
		Name: "read",
		Description: fmt.Sprintf(
			"Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
			DefaultMaxLines, DefaultMaxBytes/1024),
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to the file to read (relative or absolute)"},
				"offset": map[string]any{"type": "number", "description": "Line number to start reading from (1-indexed)"},
				"limit":  map[string]any{"type": "number", "description": "Maximum number of lines to read"},
			},
			"required": []string{"path"},
		},
		Run: func(ctx context.Context, callID string, args readArgs, onUpdate agent.UpdateFunc) (agent.ToolResult, error) {
			absolutePath, err := ResolveReadToolPath(ctx, execEnv, args.Path)
			if err != nil {
				return agent.ToolResult{}, err
			}
			data, err := execEnv.ReadBinaryFile(ctx, absolutePath)
			if err != nil {
				return agent.ToolResult{}, err
			}

			if mimeType := DetectSupportedImageMimeType(data); mimeType != "" {
				return readImage(ctx, options, data, mimeType)
			}

			return readText(args, string(data))
		},
	})
}

func readImage(ctx context.Context, options *ReadToolOptions, data []byte, mimeType string) (agent.ToolResult, error) {
	if options.ImageProcessor != nil {
		autoResize := options.AutoResizeImages == nil || *options.AutoResizeImages
		processed, err := options.ImageProcessor(ctx, data, mimeType, autoResize)
		if err != nil {
			return agent.ToolResult{}, err
		}
		if !processed.OK {
			text := fmt.Sprintf("Read image file [%s]\n%s", mimeType, processed.Message)
			return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: text}}}, nil
		}
		text := fmt.Sprintf("Read image file [%s]", processed.MimeType)
		if len(processed.Hints) > 0 {
			text += "\n" + strings.Join(processed.Hints, "\n")
		}
		return agent.ToolResult{Content: []llm.Content{
			&llm.Text{Text: text},
			&llm.Image{Data: processed.Data, MimeType: processed.MimeType},
		}}, nil
	}
	if mimeType == "image/bmp" {
		text := "Read image file [image/bmp]\n[Image omitted: configure an imageProcessor to convert BMP images.]"
		return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: text}}}, nil
	}
	return agent.ToolResult{Content: []llm.Content{
		&llm.Text{Text: fmt.Sprintf("Read image file [%s]", mimeType)},
		&llm.Image{Data: EncodeBase64(data), MimeType: mimeType},
	}}, nil
}

func readText(args readArgs, textContent string) (agent.ToolResult, error) {
	allLines := strings.Split(textContent, "\n")
	totalFileLines := len(allLines)
	startLine := 0
	if args.Offset != nil && *args.Offset > 1 {
		startLine = *args.Offset - 1
	}
	startLineDisplay := startLine + 1
	if startLine >= len(allLines) {
		return agent.ToolResult{}, fmt.Errorf("Offset %d is beyond end of file (%d lines total)", derefInt(args.Offset), len(allLines))
	}

	var selectedContent string
	userLimitedLines := -1
	if args.Limit != nil {
		endLine := min(startLine+*args.Limit, len(allLines))
		selectedContent = strings.Join(allLines[startLine:endLine], "\n")
		userLimitedLines = endLine - startLine
	} else {
		selectedContent = strings.Join(allLines[startLine:], "\n")
	}

	truncation := TruncateHead(selectedContent, TruncationOptions{})
	var outputText string
	var details any
	switch {
	case truncation.FirstLineExceedsLimit:
		firstLineSize := FormatSize(len(allLines[startLine]))
		outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
			startLineDisplay, firstLineSize, FormatSize(DefaultMaxBytes), startLineDisplay, args.Path, DefaultMaxBytes)
		details = &ReadToolDetails{Truncation: &truncation}
	case truncation.Truncated:
		endLineDisplay := startLineDisplay + truncation.OutputLines - 1
		nextOffset := endLineDisplay + 1
		outputText = truncation.Content
		if truncation.TruncatedBy == "lines" {
			outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
				startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
		} else {
			outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
				startLineDisplay, endLineDisplay, totalFileLines, FormatSize(DefaultMaxBytes), nextOffset)
		}
		details = &ReadToolDetails{Truncation: &truncation}
	case userLimitedLines >= 0 && startLine+userLimitedLines < len(allLines):
		remaining := len(allLines) - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]",
			truncation.Content, remaining, nextOffset)
	default:
		outputText = truncation.Content
	}

	return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: outputText}}, Details: details}, nil
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
