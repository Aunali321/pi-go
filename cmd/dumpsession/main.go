// dumpsession mirrors parity/session-cmp.mjs: mode "write <dir>" creates the
// same logical session via the Go implementation and prints its path; mode
// "read <path>" loads a session file and prints the shared projection.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

func mkUsage(n int) llm.Usage {
	reasoning := 5
	return llm.Usage{
		Input: n, Output: n + 1, CacheRead: n + 2, CacheWrite: n + 3, Reasoning: &reasoning,
		TotalTokens: 4*n + 6,
		Cost:        llm.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func buildSession(ctx context.Context, dir string) string {
	fs := env.NewOSEnv(dir)
	repo := session.NewJsonlSessionRepo(fs, dir+"/sessions")
	sess := must(repo.Create(ctx, session.JsonlCreateOptions{Cwd: dir, Metadata: map[string]any{"app": "parity", "n": 1}}))

	must(sess.AppendMessage(ctx, &llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "first question"}}, Timestamp: time.UnixMilli(1700000000001)}))
	must(sess.AppendMessage(ctx, &llm.AssistantMessage{
		Content: []llm.Content{
			&llm.Thinking{Thinking: "pondering", Signature: "reasoning_content"},
			&llm.Text{Text: "calling tool"},
			&llm.ToolCall{ID: "c1", Name: "get_weather", Arguments: map[string]any{"city": "Paris"}},
		},
		API: "openai-completions", Provider: "openrouter", Model: "m1",
		Usage: mkUsage(100), StopReason: llm.StopToolUse, Timestamp: time.UnixMilli(1700000000002),
	}))
	u10 := mkUsage(10)
	must(sess.AppendMessage(ctx, &llm.ToolResultMessage{
		ToolCallID: "c1", ToolName: "get_weather",
		Content: []llm.Content{&llm.Text{Text: "12C"}}, Usage: &u10, AddedToolNames: []string{"extra_tool"},
		Timestamp: time.UnixMilli(1700000000003),
	}))
	must(sess.AppendThinkingLevelChange(ctx, "high"))
	must(sess.AppendModelChange(ctx, "openrouter", "m2"))
	must(sess.AppendActiveToolsChange(ctx, []string{"get_weather", "extra_tool"}))
	must(sess.AppendCustomMessageEntry(ctx, "note", []llm.Content{&llm.Text{Text: "custom note"}}, true, map[string]any{"k": "v"}))
	u50 := mkUsage(50)
	must(sess.AppendCompaction(ctx, session.CompactionInput{
		Summary: "the summary", TokensBefore: 4321,
		Details: map[string]any{"readFiles": []string{"a.txt"}, "modifiedFiles": []string{}},
		Usage:   &u50,
		RetainedTail: []agent.AgentMessage{
			&llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "kept user"}}, Timestamp: time.UnixMilli(1700000000004)},
			&llm.AssistantMessage{
				Content: []llm.Content{&llm.Text{Text: "kept answer"}},
				API:     "openai-completions", Provider: "openrouter", Model: "m2",
				Usage: mkUsage(20), StopReason: llm.StopEnd, Timestamp: time.UnixMilli(1700000000005),
			},
		},
	}))
	must(sess.AppendMessage(ctx, &llm.UserMessage{Content: []llm.Content{&llm.Text{Text: "after compaction"}}, Timestamp: time.UnixMilli(1700000000006)}))
	must(sess.AppendSessionName(ctx, "  My\nSession  "))
	entries := sess.GetEntries(nil)
	label := "start"
	must(sess.AppendLabel(ctx, entries[0].EntryID(), &label))
	return sess.GetMetadata().(session.JsonlSessionMetadata).Path
}

func textOf(content []llm.Content) string {
	var parts []string
	for _, c := range content {
		if t, ok := c.(*llm.Text); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "|")
}

func messageProjection(m agent.AgentMessage) map[string]any {
	out := map[string]any{"role": m.Role(), "text": "", "summary": nil, "usageTotal": nil, "reasoning": nil, "addedToolNames": nil}
	switch v := m.(type) {
	case *llm.UserMessage:
		out["text"] = textOf(v.Content)
	case *llm.AssistantMessage:
		out["text"] = textOf(v.Content)
		out["usageTotal"] = v.Usage.TotalTokens
		if v.Usage.Reasoning != nil {
			out["reasoning"] = *v.Usage.Reasoning
		}
	case *llm.ToolResultMessage:
		out["text"] = textOf(v.Content)
		if v.Usage != nil {
			out["usageTotal"] = v.Usage.TotalTokens
			if v.Usage.Reasoning != nil {
				out["reasoning"] = *v.Usage.Reasoning
			}
		}
		if v.AddedToolNames != nil {
			out["addedToolNames"] = v.AddedToolNames
		}
	case *message.CustomMessage:
		out["text"] = textOf(v.Content)
	case *message.CompactionSummaryMessage:
		out["summary"] = v.Summary
	case *message.BranchSummaryMessage:
		out["summary"] = v.Summary
	}
	return out
}

func project(ctx context.Context, path string) map[string]any {
	fs := env.NewOSEnv("/")
	meta := must(session.LoadJsonlSessionMetadata(ctx, fs, path))
	repo := session.NewJsonlSessionRepo(fs, "/")
	sess := must(repo.Open(ctx, meta))
	sctx := must(sess.BuildContext())
	entries := sess.GetEntries(nil)

	var entryTypes []string
	for _, e := range entries {
		entryTypes = append(entryTypes, e.EntryType())
	}
	var label any
	if l, ok := sess.GetLabel(entries[0].EntryID()); ok {
		label = l
	}
	var name any
	if n := sess.GetSessionName(); n != "" {
		name = n
	}
	var model any
	if sctx.Model != nil {
		model = map[string]any{"provider": sctx.Model.Provider, "modelId": sctx.Model.ModelID}
	}
	stats := sess.GetSessionStats()
	var headerMetadata any = meta.Metadata

	var messages []map[string]any
	for _, m := range sctx.Messages {
		messages = append(messages, messageProjection(m))
	}

	return map[string]any{
		"headerMetadata": headerMetadata,
		"entryTypes":     entryTypes,
		"label":          label,
		"name":           name,
		"stats": map[string]any{
			"messageCount": stats.MessageCount, "cachedTokens": stats.CachedTokens,
			"uncachedTokens": stats.UncachedTokens, "totalTokens": stats.TotalTokens, "costTotal": stats.CostTotal,
		},
		"thinkingLevel":   sctx.ThinkingLevel,
		"model":           model,
		"activeToolNames": sctx.ActiveToolNames,
		"messages":        messages,
	}
}

func main() {
	ctx := context.Background()
	mode, arg := os.Args[1], os.Args[2]
	if mode == "write" {
		os.Stdout.WriteString(buildSession(ctx, arg))
		return
	}
	b, _ := json.MarshalIndent(project(ctx, arg), "", "  ")
	os.Stdout.Write(b)
}
