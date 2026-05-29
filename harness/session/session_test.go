package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/llm"
)

func TestMessageJSONRoundTrip(t *testing.T) {
	orig := &llm.AssistantMessage{
		Content: []llm.Content{
			&llm.Thinking{Thinking: "hmm", Signature: "reasoning"},
			&llm.Text{Text: "hello"},
			&llm.ToolCall{ID: "c1", Name: "do", Arguments: map[string]any{"x": float64(1)}},
		},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "anthropic/claude",
		Usage:      llm.Usage{Input: 10, Output: 5, TotalTokens: 15},
		StopReason: llm.StopToolUse,
		Timestamp:  time.UnixMilli(1700000000000),
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	got, err := llm.DecodeMessage("assistant", data)
	if err != nil {
		t.Fatal(err)
	}
	am := got.(*llm.AssistantMessage)
	if len(am.Content) != 3 || am.Model != "anthropic/claude" || am.Usage.Input != 10 {
		t.Fatalf("round-trip mismatch: %+v", am)
	}
	if tc, ok := am.Content[2].(*llm.ToolCall); !ok || tc.ID != "c1" || tc.Arguments["x"] != float64(1) {
		t.Fatalf("tool call mismatch: %+v", am.Content[2])
	}
}

func TestJsonlSessionPersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	env := env.NewOSEnv(t.TempDir())
	repo := NewJsonlSessionRepo(env, t.TempDir())

	session, err := repo.Create(ctx, JsonlCreateOptions{Cwd: env.Cwd()})
	if err != nil {
		t.Fatal(err)
	}
	meta := session.GetMetadata().(JsonlSessionMetadata)

	if _, err := session.AppendMessage(ctx, llm.TextUser("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ctx, &llm.AssistantMessage{
		Content: []llm.Content{&llm.Text{Text: "hello back"}}, Provider: "openrouter", Model: "anthropic/claude", StopReason: llm.StopEnd,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomMessageEntry(ctx, "note", []llm.Content{&llm.Text{Text: "a note"}}, true, nil); err != nil {
		t.Fatal(err)
	}

	reopened, err := repo.Open(ctx, meta)
	if err != nil {
		t.Fatal(err)
	}
	sctx, err := reopened.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	if len(sctx.Messages) != 3 {
		t.Fatalf("expected 3 messages after reload, got %d", len(sctx.Messages))
	}
	if sctx.Model == nil || sctx.Model.ModelID != "anthropic/claude" {
		t.Fatalf("expected model derived from assistant message, got %+v", sctx.Model)
	}
	if _, ok := sctx.Messages[2].(*message.CustomMessage); !ok {
		t.Fatalf("expected custom message, got %T", sctx.Messages[2])
	}
}

func TestSessionBranching(t *testing.T) {
	ctx := context.Background()
	storage, err := NewInMemorySessionStorage(SessionMetadata{ID: "s1", CreatedAt: nowISO()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session := NewSession(storage)

	id1, _ := session.AppendMessage(ctx, llm.TextUser("first"))
	session.AppendMessage(ctx, &llm.AssistantMessage{Content: []llm.Content{&llm.Text{Text: "r1"}}, StopReason: llm.StopEnd})
	branchCtx, _ := session.BuildContext()
	if len(branchCtx.Messages) != 2 {
		t.Fatalf("expected 2 messages on main branch, got %d", len(branchCtx.Messages))
	}

	// Move back to first message; new branch should only see that message.
	if _, err := session.MoveTo(ctx, &id1, nil); err != nil {
		t.Fatal(err)
	}
	movedCtx, _ := session.BuildContext()
	if len(movedCtx.Messages) != 1 {
		t.Fatalf("expected 1 message after moving to first entry, got %d", len(movedCtx.Messages))
	}
	var _ agent.AgentMessage = movedCtx.Messages[0]
}
