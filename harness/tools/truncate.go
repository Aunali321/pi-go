// Package tools provides the built-in execution tools (bash, read, write,
// edit) and their shared output-handling utilities.
package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Truncation is based on two independent limits; whichever is hit first wins.
const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024
	// GrepMaxLineLength is the max chars per grep match line.
	GrepMaxLineLength = 500
)

// TruncationResult describes a head or tail truncation.
type TruncationResult struct {
	// Content is the truncated content.
	Content string `json:"content"`
	// Truncated reports whether truncation occurred.
	Truncated bool `json:"truncated"`
	// TruncatedBy is "lines", "bytes" or "" when not truncated.
	TruncatedBy string `json:"truncatedBy"`
	TotalLines  int    `json:"totalLines"`
	TotalBytes  int    `json:"totalBytes"`
	OutputLines int    `json:"outputLines"`
	OutputBytes int    `json:"outputBytes"`
	// LastLinePartial reports whether the last line was partially truncated
	// (tail truncation edge case only).
	LastLinePartial bool `json:"lastLinePartial"`
	// FirstLineExceedsLimit reports whether the first line exceeded the byte
	// limit (head truncation).
	FirstLineExceedsLimit bool `json:"firstLineExceedsLimit"`
	MaxLines              int  `json:"maxLines"`
	MaxBytes              int  `json:"maxBytes"`
}

// TruncationOptions overrides the default limits.
type TruncationOptions struct {
	MaxLines int
	MaxBytes int
}

func (o TruncationOptions) limits() (maxLines, maxBytes int) {
	maxLines, maxBytes = o.MaxLines, o.MaxBytes
	if maxLines == 0 {
		maxLines = DefaultMaxLines
	}
	if maxBytes == 0 {
		maxBytes = DefaultMaxBytes
	}
	return
}

// splitLinesForCounting splits on \n without counting a trailing final
// newline as an extra empty line.
func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// FormatSize formats bytes as a human-readable size.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

func untruncated(content string, totalLines, totalBytes, maxLines, maxBytes int) TruncationResult {
	return TruncationResult{
		Content:     content,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: totalLines,
		OutputBytes: totalBytes,
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

// TruncateHead keeps the first N lines/bytes. Suitable for file reads where
// the beginning matters. Never returns partial lines; if the first line
// exceeds the byte limit, Content is empty with FirstLineExceedsLimit set.
func TruncateHead(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return untruncated(content, totalLines, totalBytes, maxLines, maxBytes)
	}

	if len(lines) > 0 && len(lines[0]) > maxBytes {
		return TruncationResult{
			Truncated:             true,
			TruncatedBy:           "bytes",
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	var out []string
	outBytes := 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++ // newline
		}
		if outBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		out = append(out, lines[i])
		outBytes += lineBytes
	}
	if len(out) >= maxLines && outBytes <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(out, "\n")
	return TruncationResult{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(out),
		OutputBytes: len(outputContent),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

// TruncateTail keeps the last N lines/bytes. Suitable for bash output where
// the end matters. May return a partial first line when a single line exceeds
// the byte limit.
func TruncateTail(content string, options TruncationOptions) TruncationResult {
	maxLines, maxBytes := options.limits()
	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return untruncated(content, totalLines, totalBytes, maxLines, maxBytes)
	}

	var out []string
	outBytes := 0
	truncatedBy := "lines"
	lastLinePartial := false
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		lineBytes := len(lines[i])
		if len(out) > 0 {
			lineBytes++ // newline
		}
		if outBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			// Edge case: no lines collected yet and this one alone exceeds
			// the limit; take the end of the line.
			if len(out) == 0 {
				partial := truncateToBytesFromEnd(lines[i], maxBytes)
				out = append(out, partial)
				outBytes = len(partial)
				lastLinePartial = true
			}
			break
		}
		out = append([]string{lines[i]}, out...)
		outBytes += lineBytes
	}
	if len(out) >= maxLines && outBytes <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(out, "\n")
	return TruncationResult{
		Content:         outputContent,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(out),
		OutputBytes:     len(outputContent),
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines,
		MaxBytes:        maxBytes,
	}
}

// truncateToBytesFromEnd keeps the last maxBytes of s without splitting a
// UTF-8 sequence.
func truncateToBytesFromEnd(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// TruncateLine truncates a single line to max characters, adding a
// [truncated] suffix. Used for grep match lines.
func TruncateLine(line string, maxChars int) (text string, wasTruncated bool) {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLength
	}
	runes := []rune(line)
	if len(runes) <= maxChars {
		return line, false
	}
	return string(runes[:maxChars]) + "... [truncated]", true
}
