// dumpcompactreq mirrors parity/compactreq-cmp.mjs: runs Compact against a
// fake runner and prints the captured summarization requests and result.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/compaction"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

func main() {
	ctx := context.Background()
	repo := session.NewInMemorySessionRepo()
	sess, _ := repo.Create("")

	texts := []string{"first question about the project setup and goals", "here is a detailed answer explaining the setup", "a follow up question about configuration", "another long detailed response with specifics", "third user turn asking something else entirely", "response number three with more detail here", "the most recent user question goes here now", "the final recent assistant response is here"}
	for i, t := range texts {
		if i%2 == 0 {
			sess.AppendMessage(ctx, &llm.UserMessage{Content: []llm.Content{&llm.Text{Text: t}}, Timestamp: time.UnixMilli(int64(i + 1))})
		} else {
			sess.AppendMessage(ctx, &llm.AssistantMessage{
				Content: []llm.Content{
					&llm.Text{Text: t},
					&llm.ToolCall{ID: "tc" + itoa(i), Name: "read", Arguments: map[string]any{"path": "file" + itoa(i) + ".txt"}},
				},
				API: "openai-completions", Provider: "openrouter", Model: "m",
				Usage:      llm.Usage{Input: 500 * (i + 1), TotalTokens: 500 * (i + 1)},
				StopReason: llm.StopEnd, Timestamp: time.UnixMilli(int64(i + 1)),
			})
		}
	}

	model := &llm.Model{
		ID: "m", Name: "m", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1",
		Input: []llm.InputModality{llm.InputText}, ContextWindow: 200000, MaxTokens: 4096,
	}

	var captured []map[string]any
	fakeStream := func(ctx context.Context, m *llm.Model, reqCtx *llm.Context, opts *llm.StreamOptions) *llm.Stream {
		var msgs []map[string]any
		for _, msg := range reqCtx.Messages {
			um := msg.(*llm.UserMessage)
			var text strings.Builder
			for _, c := range um.Content {
				if t, ok := c.(*llm.Text); ok {
					text.WriteString(t.Text)
				}
			}
			msgs = append(msgs, map[string]any{"role": um.Role(), "text": text.String()})
		}
		var maxTokens any
		if opts.MaxTokens != 0 {
			maxTokens = opts.MaxTokens
		}
		var reasoning any
		if opts.Reasoning != "" {
			reasoning = string(opts.Reasoning)
		}
		captured = append(captured, map[string]any{
			"systemPrompt":   reqCtx.SystemPrompt,
			"messages":       msgs,
			"maxTokens":      maxTokens,
			"cacheRetention": string(opts.CacheRetention),
			"reasoning":      reasoning,
			"sessionIdSet":   opts.SessionID != "",
		})
		return llm.StreamFromMessage(&llm.AssistantMessage{
			Content: []llm.Content{&llm.Text{Text: "GENERATED SUMMARY"}},
			API:     "openai-completions", Provider: "openrouter", Model: "m",
			Usage:      llm.Usage{Input: 1, Output: 2, TotalTokens: 3},
			StopReason: llm.StopEnd, Timestamp: time.UnixMilli(1),
		})
	}

	entries, _ := sess.GetBranch(nil)
	prep, _ := compaction.PrepareCompaction(entries, compaction.CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 50})
	runner := compaction.SummaryRunner{Stream: agent.StreamFunc(fakeStream), Model: model, ThinkingLevel: llm.ThinkingOff}
	result, err := compaction.Compact(ctx, prep, runner, "focus on the tests")
	if err != nil {
		panic(err)
	}

	firstKeptIndex := -1
	for i, e := range entries {
		if e.EntryID() == result.FirstKeptEntryID {
			firstKeptIndex = i
			break
		}
	}
	var tailRoles []string
	for _, m := range result.RetainedTail {
		tailRoles = append(tailRoles, m.Role())
	}
	out := map[string]any{
		"captured": captured,
		"result": map[string]any{
			"summary":           result.Summary,
			"tokensBefore":      result.TokensBefore,
			"firstKeptIndex":    firstKeptIndex,
			"retainedTailRoles": tailRoles,
			"usage":             result.Usage,
			"details":           result.Details,
		},
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.Stdout.Write(b)
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
