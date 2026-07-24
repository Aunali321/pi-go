// dumptools runs the built-in tools on the same fixture inputs as
// parity/tools-cmp.mjs and prints the results for diffing.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/harness/tools"
	"github.com/aunali321/pi-go/llm"
)

var (
	fixtureDir  string
	tmpPathized = regexp.MustCompile(`/tmp/[A-Za-z0-9._/-]*bash-[A-Za-z0-9._-]*\.log`)
)

func normalize(v any) any {
	data, _ := json.Marshal(v)
	s := strings.ReplaceAll(string(data), fixtureDir, "<DIR>")
	s = regexp.MustCompile(`"fullOutputPath":"[^"]*"`).ReplaceAllString(s, `"fullOutputPath":"<TMP>"`)
	s = tmpPathized.ReplaceAllString(s, "<TMP>")
	var out any
	json.Unmarshal([]byte(s), &out)
	return out
}

func contentJSON(content []llm.Content) any {
	out := make([]map[string]any, 0, len(content))
	for _, c := range content {
		switch b := c.(type) {
		case *llm.Text:
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case *llm.Image:
			out = append(out, map[string]any{"type": "image", "data": b.Data, "mimeType": b.MimeType})
		}
	}
	return out
}

func run(tool agent.Tool, args map[string]any) map[string]any {
	raw, _ := json.Marshal(args)
	result, err := tool.Execute(context.Background(), "call1", raw, nil)
	if err != nil {
		return map[string]any{"ok": false, "error": normalize(err.Error())}
	}
	var details any
	if result.Details != nil {
		details = result.Details
	}
	return map[string]any{"ok": true, "content": normalize(contentJSON(result.Content)), "details": normalize(details)}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	dir, err := os.MkdirTemp("", "pi-tools-parity-")
	must(err)
	fixtureDir = dir

	var bigLines []string
	for i := 1; i <= 2500; i++ {
		bigLines = append(bigLines, "line "+jsonNumber(i))
	}
	must(os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(bigLines, "\n")), 0o644))
	must(os.WriteFile(filepath.Join(dir, "small.txt"), []byte("alpha\nbeta\ngamma"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "code.txt"), []byte("func main() {\n\tprintln(\"hi\")\n}\n\nfunc other() {\n\tprintln(\"other\")\n}\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "fuzzy.txt"), []byte("it’s a “smart” file\nplain line\n"), 0o644))

	execEnv := env.NewOSEnv(dir)
	bash := tools.NewBashTool(execEnv, nil)
	read := tools.NewReadTool(execEnv, nil)
	write := tools.NewWriteTool(execEnv)
	edit := tools.NewEditTool(execEnv)

	out := map[string]any{
		"bash-echo":     run(bash, map[string]any{"command": "echo hello; sleep 0.1; echo err >&2"}),
		"bash-exit":     run(bash, map[string]any{"command": "echo boom; exit 3"}),
		"bash-truncate": run(bash, map[string]any{"command": "seq 1 3000"}),
		"read-small":    run(read, map[string]any{"path": "small.txt"}),
		"read-offset-limit": run(read, map[string]any{
			"path": "small.txt", "offset": 2, "limit": 1,
		}),
		"read-truncated":     run(read, map[string]any{"path": "big.txt"}),
		"read-offset-beyond": run(read, map[string]any{"path": "small.txt", "offset": 99}),
		"write":              run(write, map[string]any{"path": "new/file.txt", "content": "written content\n"}),
		"edit-basic": run(edit, map[string]any{
			"path": "code.txt",
			"edits": []map[string]string{
				{"oldText": "println(\"hi\")", "newText": "println(\"bye\")"},
				{"oldText": "println(\"other\")", "newText": "println(\"changed\")"},
			},
		}),
		"edit-fuzzy": run(edit, map[string]any{
			"path":  "fuzzy.txt",
			"edits": []map[string]string{{"oldText": "it's a \"smart\" file", "newText": "it is a plain file"}},
		}),
		"edit-notfound":  run(edit, map[string]any{"path": "small.txt", "edits": []map[string]string{{"oldText": "missing", "newText": "x"}}}),
		"edit-duplicate": run(edit, map[string]any{"path": "code.txt", "edits": []map[string]string{{"oldText": "func", "newText": "fn"}}}),
		"edit-overlap": run(edit, map[string]any{
			"path": "small.txt",
			"edits": []map[string]string{
				{"oldText": "alpha\nbeta", "newText": "x"},
				{"oldText": "beta\ngamma", "newText": "y"},
			},
		}),
		"edit-legacy": run(edit, map[string]any{"path": "small.txt", "oldText": "gamma", "newText": "delta", "edits": []map[string]string{}}),
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	os.Stdout.Write(b)
	os.RemoveAll(dir)
}

func jsonNumber(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
