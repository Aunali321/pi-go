package tools

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/llm"
)

func reconstructFromParts(parts []diffPart, useAdded bool) string {
	var b strings.Builder
	for _, p := range parts {
		if p.added && !useAdded || p.removed && useAdded {
			continue
		}
		for _, line := range p.lines {
			b.WriteString(line)
		}
	}
	return b.String()
}

func TestDiffLinesRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"a\nb\nc\n", "a\nx\nc\n"},
		{"", "a\n"},
		{"a\n", ""},
		{"a\nb\nc", "a\nb\nc"},
		{"one\ntwo\nthree\nfour\n", "zero\none\nthree\nfive\n"},
		{"x", "y"},
		{"a\nb\n", "b\na\n"},
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		mk := func() string {
			n := rng.Intn(12)
			var lines []string
			for j := 0; j < n; j++ {
				lines = append(lines, string(rune('a'+rng.Intn(4))))
			}
			s := strings.Join(lines, "\n")
			if n > 0 && rng.Intn(2) == 0 {
				s += "\n"
			}
			return s
		}
		cases = append(cases, [2]string{mk(), mk()})
	}
	for _, c := range cases {
		parts := diffLines(c[0], c[1])
		if got := reconstructFromParts(parts, false); got != c[0] {
			t.Fatalf("old reconstruction mismatch:\nwant %q\ngot  %q", c[0], got)
		}
		if got := reconstructFromParts(parts, true); got != c[1] {
			t.Fatalf("new reconstruction mismatch:\nwant %q\ngot  %q", c[1], got)
		}
	}
}

func TestGenerateDiffString(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	new := "a\nb\nc\nd\ne\nf\nX\nh\ni\nj\nk\nl\n"
	diff, firstChanged := generateDiffString(old, new, 4)
	if firstChanged != 7 {
		t.Fatalf("firstChangedLine = %d, want 7", firstChanged)
	}
	if !strings.Contains(diff, "- 7 g") || !strings.Contains(diff, "+ 7 X") {
		t.Fatalf("unexpected diff:\n%s", diff)
	}
}

func TestGenerateUnifiedPatch(t *testing.T) {
	patch := generateUnifiedPatch("f.txt", "a\nb\nc\n", "a\nx\nc\n", 4)
	want := "--- f.txt\n+++ f.txt\n@@ -1,3 +1,3 @@\n a\n-b\n+x\n c\n"
	if patch != want {
		t.Fatalf("patch mismatch:\nwant:\n%s\ngot:\n%s", want, patch)
	}

	patch = generateUnifiedPatch("f.txt", "a\nb", "a\nc", 4)
	if !strings.Contains(patch, `\ No newline at end of file`) {
		t.Fatalf("missing no-EOL marker:\n%s", patch)
	}
}

func TestApplyEdits(t *testing.T) {
	base, updated, err := applyEditsToNormalizedContent("hello world\nsecond line\n", []Edit{{OldText: "world", NewText: "there"}}, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if base != "hello world\nsecond line\n" || updated != "hello there\nsecond line\n" {
		t.Fatalf("unexpected result: %q", updated)
	}

	// Fuzzy: smart quote in file, ASCII quote in edit.
	_, updated, err = applyEditsToNormalizedContent("it’s here\nok\n", []Edit{{OldText: "it's here", NewText: "it is here"}}, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, "it is here") {
		t.Fatalf("fuzzy edit failed: %q", updated)
	}

	_, _, err = applyEditsToNormalizedContent("dup\ndup\n", []Edit{{OldText: "dup", NewText: "x"}}, "f.txt")
	if err == nil || !strings.Contains(err.Error(), "occurrences") {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	_, _, err = applyEditsToNormalizedContent("abcdef\n", []Edit{{OldText: "abcd", NewText: "x"}, {OldText: "cdef", NewText: "y"}}, "f.txt")
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("expected overlap error, got %v", err)
	}

	_, _, err = applyEditsToNormalizedContent("aaa\n", []Edit{{OldText: "missing", NewText: "x"}}, "f.txt")
	if err == nil || !strings.Contains(err.Error(), "Could not find") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestTruncate(t *testing.T) {
	content := strings.Repeat("line\n", 3000)
	r := TruncateTail(content, TruncationOptions{})
	if !r.Truncated || r.TruncatedBy != "lines" || r.OutputLines != DefaultMaxLines {
		t.Fatalf("unexpected tail truncation: %+v", r)
	}
	r = TruncateHead(content, TruncationOptions{})
	if !r.Truncated || r.OutputLines != DefaultMaxLines {
		t.Fatalf("unexpected head truncation: %+v", r)
	}
	r = TruncateHead("short\n", TruncationOptions{})
	if r.Truncated {
		t.Fatalf("should not truncate: %+v", r)
	}
	// Single huge line: tail keeps the end, head refuses.
	huge := strings.Repeat("x", DefaultMaxBytes+100)
	r = TruncateTail(huge, TruncationOptions{})
	if !r.LastLinePartial || len(r.Content) > DefaultMaxBytes {
		t.Fatalf("tail partial expected: partial=%v len=%d", r.LastLinePartial, len(r.Content))
	}
	r = TruncateHead(huge, TruncationOptions{})
	if !r.FirstLineExceedsLimit {
		t.Fatalf("head first-line-exceeds expected: %+v", r)
	}
}

func runTool(t *testing.T, tool agent.Tool, args any) (agent.ToolResult, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), "call1", raw, nil)
}

func textContent(t *testing.T, res agent.ToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if txt, ok := c.(*llm.Text); ok {
			b.WriteString(txt.Text)
		}
	}
	return b.String()
}

func TestBashTool(t *testing.T) {
	e := env.NewOSEnv(t.TempDir())
	tool := NewBashTool(e, nil)

	res, err := runTool(t, tool, map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got := textContent(t, res); got != "hello\n" {
		t.Fatalf("unexpected output %q", got)
	}

	_, err = runTool(t, tool, map[string]any{"command": "echo boom >&2; exit 3"})
	if err == nil || !strings.Contains(err.Error(), "Command exited with code 3") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected exit-code error with output, got %v", err)
	}

	_, err = runTool(t, tool, map[string]any{"command": "sleep 5", "timeout": 0.2})
	if err == nil || !strings.Contains(err.Error(), "Command timed out after 0.2 seconds") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestBashToolTruncation(t *testing.T) {
	e := env.NewOSEnv(t.TempDir())
	tool := NewBashTool(e, nil)
	res, err := runTool(t, tool, map[string]any{"command": "seq 1 3000"})
	if err != nil {
		t.Fatal(err)
	}
	out := textContent(t, res)
	if !strings.Contains(out, "[Showing lines 1001-3000 of 3000. Full output: ") {
		t.Fatalf("expected truncation notice, got tail: %q", out[max(0, len(out)-200):])
	}
	details, ok := res.Details.(*BashToolDetails)
	if !ok || details.FullOutputPath == "" {
		t.Fatalf("expected details with full output path, got %#v", res.Details)
	}
	full, err := os.ReadFile(details.FullOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(full), "1\n2\n3\n") || !strings.HasSuffix(string(full), "3000\n") {
		t.Fatalf("full output spool mismatch (%d bytes)", len(full))
	}
}

func TestWriteAndReadTools(t *testing.T) {
	dir := t.TempDir()
	e := env.NewOSEnv(dir)
	write := NewWriteTool(e)
	read := NewReadTool(e, nil)

	res, err := runTool(t, write, map[string]any{"path": "sub/file.txt", "content": "one\ntwo\nthree"})
	if err != nil {
		t.Fatal(err)
	}
	if got := textContent(t, res); got != "Successfully wrote 13 bytes to sub/file.txt" {
		t.Fatalf("unexpected write result %q", got)
	}

	res, err = runTool(t, read, map[string]any{"path": "sub/file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if got := textContent(t, res); got != "one\ntwo\nthree" {
		t.Fatalf("unexpected read result %q", got)
	}

	res, err = runTool(t, read, map[string]any{"path": "sub/file.txt", "offset": 2, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := textContent(t, res); got != "two\n\n[1 more lines in file. Use offset=3 to continue.]" {
		t.Fatalf("unexpected limited read %q", got)
	}

	_, err = runTool(t, read, map[string]any{"path": "sub/file.txt", "offset": 99})
	if err == nil || !strings.Contains(err.Error(), "beyond end of file") {
		t.Fatalf("expected offset error, got %v", err)
	}
}

func TestReadToolImage(t *testing.T) {
	dir := t.TempDir()
	// Minimal PNG header: signature + IHDR chunk length/type.
	png := append([]byte{}, pngSignature...)
	png = append(png, 0, 0, 0, 13)
	png = append(png, []byte("IHDR")...)
	png = append(png, make([]byte, 17)...)
	if err := os.WriteFile(filepath.Join(dir, "img.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}

	e := env.NewOSEnv(dir)
	res, err := runTool(t, NewReadTool(e, nil), map[string]any{"path": "img.png"})
	if err != nil {
		t.Fatal(err)
	}
	if got := textContent(t, res); got != "Read image file [image/png]" {
		t.Fatalf("unexpected image read text %q", got)
	}
	if len(res.Content) != 2 {
		t.Fatalf("expected image attachment, got %d blocks", len(res.Content))
	}
	img, ok := res.Content[1].(*llm.Image)
	if !ok || img.MimeType != "image/png" || img.Data != EncodeBase64(png) {
		t.Fatalf("unexpected image block %#v", res.Content[1])
	}
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := env.NewOSEnv(dir)
	tool := NewEditTool(e)
	res, err := runTool(t, tool, map[string]any{
		"path":  "code.go",
		"edits": []map[string]string{{"oldText": "println(\"hi\")", "newText": "println(\"bye\")"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := textContent(t, res); got != "Successfully replaced 1 block(s) in code.go." {
		t.Fatalf("unexpected edit result %q", got)
	}
	details, ok := res.Details.(*EditToolDetails)
	if !ok || details.FirstChangedLine != 4 || !strings.Contains(details.Patch, "+\tprintln(\"bye\")") {
		t.Fatalf("unexpected details %#v", res.Details)
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "bye") {
		t.Fatalf("file not updated: %s", updated)
	}

	// Legacy top-level oldText/newText arguments.
	_, err = runTool(t, tool, map[string]any{"path": "code.go", "oldText": "bye", "newText": "hi again"})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = os.ReadFile(path)
	if !strings.Contains(string(updated), "hi again") {
		t.Fatalf("legacy edit not applied: %s", updated)
	}
}

func TestDetectImageMimeTypes(t *testing.T) {
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff, 0xe0}); got != "image/jpeg" {
		t.Fatalf("jpeg: %q", got)
	}
	if got := DetectSupportedImageMimeType([]byte("GIF89a")); got != "image/gif" {
		t.Fatalf("gif: %q", got)
	}
	if got := DetectSupportedImageMimeType([]byte("RIFF1234WEBPVP8 ")); got != "image/webp" {
		t.Fatalf("webp: %q", got)
	}
	if got := DetectSupportedImageMimeType([]byte("plain text")); got != "" {
		t.Fatalf("text: %q", got)
	}
}
