package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/compaction"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

func textOf(m agent.AgentMessage) string {
	var content []llm.Content
	switch v := m.(type) {
	case *llm.UserMessage:
		content = v.Content
	case *llm.AssistantMessage:
		content = v.Content
	}
	var sb strings.Builder
	for _, c := range content {
		if t, ok := c.(*llm.Text); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

func main() {
	ctx := context.Background()
	repo := session.NewInMemorySessionRepo()
	sess, _ := repo.Create("")

	texts := []string{"first question about the project setup and goals", "here is a detailed answer explaining the setup", "a follow up question about configuration", "another long detailed response with specifics", "third user turn asking something else entirely", "response number three with more detail here", "the most recent user question goes here now", "the final recent assistant response is here"}
	for i, t := range texts {
		if i%2 == 0 {
			sess.AppendMessage(ctx, &llm.UserMessage{Content: []llm.Content{&llm.Text{Text: t}}})
		} else {
			sess.AppendMessage(ctx, &llm.AssistantMessage{
				Content: []llm.Content{&llm.Text{Text: t}}, API: "openai-completions", Provider: "openrouter", Model: "m",
				Usage: llm.Usage{Input: 500 * (i + 1), TotalTokens: 500 * (i + 1)}, StopReason: llm.StopEnd,
			})
		}
	}

	entries, _ := sess.GetBranch(nil)
	prep, _ := compaction.PrepareCompaction(entries, compaction.CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 50})

	idx := -1
	for i, e := range entries {
		if e.EntryID() == prep.FirstKeptEntryID {
			idx = i
			break
		}
	}
	summarizeRoles := make([]string, 0)
	summarizeTexts := make([]string, 0)
	for _, m := range prep.MessagesToSummarize {
		summarizeRoles = append(summarizeRoles, m.Role())
		summarizeTexts = append(summarizeTexts, textOf(m))
	}
	prefixRoles := make([]string, 0)
	for _, m := range prep.TurnPrefixMessages {
		prefixRoles = append(prefixRoles, m.Role())
	}
	out := map[string]any{
		"tokensBefore":   prep.TokensBefore,
		"isSplitTurn":    prep.IsSplitTurn,
		"firstKeptIndex": idx,
		"summarizeRoles": summarizeRoles,
		"summarizeTexts": summarizeTexts,
		"prefixRoles":    prefixRoles,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.Stdout.Write(b)
}
