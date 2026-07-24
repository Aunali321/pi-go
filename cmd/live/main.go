package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aunali321/pi-go/agent"
	"github.com/aunali321/pi-go/harness"
	"github.com/aunali321/pi-go/harness/env"
	"github.com/aunali321/pi-go/harness/message"
	"github.com/aunali321/pi-go/harness/session"
	"github.com/aunali321/pi-go/llm"
)

type weatherArgs struct {
	City string `json:"city"`
}

type fileArgs struct {
	Name string `json:"name"`
}

func visionModel() *llm.Model {
	m := mkModel()
	m.Input = []llm.InputModality{llm.InputText, llm.InputImage}
	return m
}

func modelID() string {
	if m := os.Getenv("PI_TEST_MODEL"); m != "" {
		return m
	}
	return "anthropic/claude-3.5-haiku"
}

func mkModel() *llm.Model {
	return &llm.Model{
		ID: modelID(), Name: modelID(), Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1",
		Reasoning: false, Input: []llm.InputModality{llm.InputText},
		Cost: llm.Pricing{Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1}, ContextWindow: 200000, MaxTokens: 1024,
	}
}

func textOf(m agent.AgentMessage) string {
	var content []llm.Content
	switch v := m.(type) {
	case *llm.UserMessage:
		content = v.Content
	case *llm.AssistantMessage:
		content = v.Content
	case *llm.ToolResultMessage:
		content = v.Content
	case *message.CustomMessage:
		content = v.Content
	}
	var sb strings.Builder
	for _, c := range content {
		if t, ok := c.(*llm.Text); ok {
			sb.WriteString(t.Text)
		}
	}
	s := sb.String()
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func summarize(msgs []agent.AgentMessage) []map[string]string {
	out := make([]map[string]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]string{"role": m.Role(), "text": textOf(m)})
	}
	return out
}

func main() {
	ctx := context.Background()
	env := env.NewOSEnv("")
	mode := os.Args[1]

	switch mode {
	case "run":
		sessionsRoot := os.Args[2]
		repo := session.NewJsonlSessionRepo(env, sessionsRoot)
		sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: env.Cwd()})
		must(err)
		tool := agent.NewTool(agent.ToolDef[weatherArgs]{
			Name: "get_weather", Label: "Weather", Description: "Get the current weather for a city.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
			Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
				return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "It is 14C and windy in " + args.City + "."}}, Details: map[string]any{"city": args.City}}, nil
			},
		})
		h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
			Session: sess, Tools: []agent.Tool{tool}, Model: mkModel(),
			SystemPrompt:  "You MUST call the get_weather tool to answer any weather question, then give a one-sentence answer.",
			StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort},
		})
		must(err)
		final, err := h.Prompt(ctx, "What's the weather in Berlin?")
		must(err)
		meta := sess.GetMetadata().(session.JsonlSessionMetadata)
		sctx, _ := sess.BuildContext()
		emit(map[string]any{"impl": "go", "path": meta.Path, "stopReason": final.StopReason, "summary": summarize(sctx.Messages)})

	case "run2":
		sessionsRoot := os.Args[2]
		repo := session.NewJsonlSessionRepo(env, sessionsRoot)
		sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: env.Cwd()})
		must(err)
		tool := agent.NewTool(agent.ToolDef[weatherArgs]{
			Name: "get_weather", Label: "Weather", Description: "Get the current weather for a city.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
			Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
				return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "It is 14C and windy in " + args.City + "."}}, Details: map[string]any{"city": args.City}}, nil
			},
		})
		h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
			Session: sess, Tools: []agent.Tool{tool}, Model: mkModel(),
			SystemPrompt:  "You MUST call the get_weather tool to answer any weather question, then give a one-sentence answer.",
			StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort},
		})
		must(err)
		must(h.SetThinkingLevel(ctx, llm.ThinkingMedium))
		_, err = h.Prompt(ctx, "What's the weather in Berlin?")
		must(err)
		_, err = h.Prompt(ctx, "And in Paris?")
		must(err)
		meta := sess.GetMetadata().(session.JsonlSessionMetadata)
		sctx, _ := sess.BuildContext()
		emit(map[string]any{"impl": "go", "path": meta.Path, "thinkingLevel": sctx.ThinkingLevel, "entryTypes": entryTypes(sess.GetEntries(nil)), "roles": roles(sctx.Messages)})

	case "run3":
		sessionsRoot := os.Args[2]
		repo := session.NewJsonlSessionRepo(env, sessionsRoot)
		sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: env.Cwd()})
		must(err)
		tool := agent.NewTool(agent.ToolDef[weatherArgs]{
			Name: "get_weather", Label: "Weather", Description: "Get the current weather for a city.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
			Run: func(ctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
				return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "It is 14C and windy in " + args.City + "."}}, Details: map[string]any{"city": args.City}}, nil
			},
		})
		h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
			Session: sess, Tools: []agent.Tool{tool}, Model: mkModel(),
			SystemPrompt:  "You MUST call the get_weather tool to answer any weather question, then give a one-sentence answer.",
			StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort},
		})
		must(err)
		_, err = h.Prompt(ctx, "What's the weather in Berlin?")
		must(err)
		_, err = sess.AppendCustomMessageEntry(ctx, "note", []llm.Content{&llm.Text{Text: "a side note"}}, true, nil)
		must(err)
		must(h.SetModel(ctx, &llm.Model{ID: "qwen/qwen-2.5-72b-instruct", Name: "qwen", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: []llm.InputModality{llm.InputText}, Cost: llm.Pricing{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1}, ContextWindow: 32000, MaxTokens: 1024}))
		must(h.SetActiveTools(ctx, []string{"get_weather"}))
		meta := sess.GetMetadata().(session.JsonlSessionMetadata)
		sctx, _ := sess.BuildContext()
		emit(map[string]any{"impl": "go", "path": meta.Path, "model": modelOut(sctx.Model), "entryTypes": entryTypes(sess.GetEntries(nil)), "roles": roles(sctx.Messages)})

	case "imgpayload":
		raw, err := os.ReadFile(os.Args[2])
		must(err)
		data := base64.StdEncoding.EncodeToString(raw)
		reqCtx := &llm.Context{
			SystemPrompt: "You are a helpful assistant.",
			Messages: []llm.Message{&llm.UserMessage{Content: []llm.Content{
				&llm.Text{Text: "Describe this screenshot in one sentence."},
				&llm.Image{Data: data, MimeType: "image/png"},
			}}},
		}
		llm.StreamSimple(ctx, visionModel(), reqCtx, &llm.StreamOptions{
			APIKey: "dummy", CacheRetention: llm.CacheNone, MaxTokens: 1024,
			OnPayload: func(p map[string]any) map[string]any {
				out, _ := json.MarshalIndent(p, "", "  ")
				os.Stdout.Write(out)
				os.Exit(0)
				return nil
			},
		}).Result()

	case "vision":
		raw, err := os.ReadFile(os.Args[2])
		must(err)
		data := base64.StdEncoding.EncodeToString(raw)
		repo := session.NewJsonlSessionRepo(env, os.Args[3])
		sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: env.Cwd()})
		must(err)
		h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
			Session: sess, Model: visionModel(),
			SystemPrompt:  "You are a helpful assistant. Describe images concisely.",
			StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheNone},
		})
		must(err)
		final, err := h.Prompt(ctx, "Describe this screenshot in one sentence.", &llm.Image{Data: data, MimeType: "image/png"})
		must(err)
		sctx, _ := sess.BuildContext()
		emit(map[string]any{"impl": "go", "stopReason": final.StopReason, "text": textOf(final), "roles": roles(sctx.Messages)})

	case "filetool":
		dir := os.Args[2]
		tool := agent.NewTool(agent.ToolDef[fileArgs]{
			Name: "read_file", Label: "Read", Description: "Read a text file by name from the working directory.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}},
			Run: func(rctx context.Context, id string, args fileArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
				content, ferr := env.ReadTextFile(rctx, filepath.Join(dir, args.Name))
				if ferr != nil {
					content = "error: " + ferr.Error()
				}
				return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: content}}, Details: map[string]any{}}, nil
			},
		})
		repo := session.NewJsonlSessionRepo(env, os.Args[3])
		sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: env.Cwd()})
		must(err)
		h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
			Session: sess, Tools: []agent.Tool{tool}, Model: mkModel(),
			SystemPrompt:  "You MUST use the read_file tool to read files. Then answer.",
			StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheNone},
		})
		must(err)
		final, err := h.Prompt(ctx, "Read the file notes.txt and tell me the secret code it contains.")
		must(err)
		sctx, _ := sess.BuildContext()
		emit(map[string]any{"impl": "go", "stopReason": final.StopReason, "text": textOf(final), "roles": roles(sctx.Messages)})

	case "tool":
		feature := os.Args[2]
		root := os.Args[3]
		var mu sync.Mutex
		var executed []string
		toolEnds := []map[string]any{}
		updateCount := 0

		repo := session.NewJsonlSessionRepo(env, root)
		sess, err := repo.Create(ctx, session.JsonlCreateOptions{Cwd: env.Cwd()})
		must(err)

		var optErr, optTerminate, optUpdate bool
		prompt := "Get the weather in Berlin using the get_weather tool."
		switch feature {
		case "error":
			optErr = true
		case "terminate":
			optTerminate = true
		case "update":
			optUpdate = true
		case "multi":
			prompt = "Get the weather in BOTH Berlin and Paris. You must call get_weather separately for each city."
		}

		tool := agent.NewTool(agent.ToolDef[weatherArgs]{
			Name: "get_weather", Label: "W", Description: "Get the current weather for a city.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
			Run: func(rctx context.Context, id string, args weatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
				mu.Lock()
				executed = append(executed, args.City)
				mu.Unlock()
				if optUpdate && up != nil {
					up(agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "working..."}}})
				}
				if optErr {
					return agent.ToolResult{}, errors.New("tool boom")
				}
				r := agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "14C in " + args.City}}, Details: map[string]any{}}
				if optTerminate {
					r.Terminate = true
				}
				return r, nil
			},
		})

		h, err := harness.NewAgentHarness(harness.AgentHarnessOptions{
			Session: sess, Tools: []agent.Tool{tool}, Model: mkModel(),
			SystemPrompt:  "You must use the provided tools to answer. Always call the tool when asked.",
			StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheNone},
		})
		must(err)
		if feature == "block" {
			h.OnToolCall(func(e harness.ToolCallEvent) *harness.ToolCallResult {
				return &harness.ToolCallResult{Block: true, Reason: "blocked by test"}
			})
		}
		if feature == "patch" {
			h.OnToolResult(func(e harness.ToolResultEvent) *harness.ToolResultPatch {
				return &harness.ToolResultPatch{Content: []llm.Content{&llm.Text{Text: "PATCHED"}}}
			})
		}
		h.Subscribe(func(event any, _ context.Context) {
			switch e := event.(type) {
			case agent.ToolExecutionEnd:
				mu.Lock()
				toolEnds = append(toolEnds, map[string]any{"toolName": e.ToolName, "isError": e.IsError})
				mu.Unlock()
			case agent.ToolExecutionUpdate:
				mu.Lock()
				updateCount++
				mu.Unlock()
			}
		})
		_, perr := h.Prompt(ctx, prompt)
		sctx, _ := sess.BuildContext()
		toolResults := []map[string]any{}
		for _, m := range sctx.Messages {
			if tr, ok := m.(*llm.ToolResultMessage); ok {
				toolResults = append(toolResults, map[string]any{"text": textOf(tr), "isError": tr.IsError})
			}
		}
		out := map[string]any{"impl": "go", "feature": feature, "toolEnds": toolEnds, "updateCount": updateCount, "executed": executed, "roles": roles(sctx.Messages), "toolResults": toolResults}
		if perr != nil {
			out["promptError"] = perr.Error()
		}
		emit(out)

	case "read":
		filePath := os.Args[2]
		storage, err := session.OpenJsonlSessionStorage(ctx, env, filePath)
		must(err)
		sess := session.NewSession(storage)
		sctx, err := sess.BuildContext()
		must(err)
		emit(map[string]any{"impl": "go-read", "thinkingLevel": sctx.ThinkingLevel, "model": modelOut(sctx.Model), "entryTypes": entryTypes(sess.GetEntries(nil)), "roles": roles(sctx.Messages), "summary": summarize(sctx.Messages)})
	}
}

func entryTypes(entries []session.SessionTreeEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.EntryType()
	}
	return out
}

func modelOut(m *session.ModelRef) any {
	if m == nil {
		return nil
	}
	return map[string]string{"provider": m.Provider, "modelId": m.ModelID}
}

func roles(msgs []agent.AgentMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role()
	}
	return out
}

func emit(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	os.Stdout.Write(b)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
