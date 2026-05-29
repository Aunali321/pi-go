---
name: pi-go
description: Build Go agents/apps on the pi-go runtime (OpenRouter-only port of @earendil-works/pi-agent-core). Use when writing Go that runs an LLM agent loop, defines tools, streams responses, or persists sessions via this module (github.com/aunali321/pi-go).
---

# pi-go

A Go 1:1 port of pi-agent-core targeting OpenRouter (OpenAI chat-completions wire format). Zero third-party deps. Module path: `github.com/aunali321/pi-go` (local, not yet published — import via a `replace` directive in your `go.mod` pointing at the checkout).

Packages (dependency order): `llm` (provider/wire) → `agent` (loop, tools, stateful Agent) → `harness/{env,message,session,compaction}` → `harness` (orchestrator). Most apps need only `llm` + `agent`; add `harness` for on-disk session persistence, compaction, skills, and hooks.

## Model

There is NO model registry. Construct `*llm.Model` directly. `Provider` and `BaseURL` default to OpenRouter.

```go
model := &llm.Model{
    ID:            "anthropic/claude-3.5-haiku", // OpenRouter model id
    Input:         []llm.InputModality{llm.InputText}, // add llm.InputImage for vision
    ContextWindow: 200000,
    MaxTokens:     1024,
    Cost:          llm.Pricing{Input: 0.8, Output: 4}, // $/1M tokens; only affects Usage.Cost
}
```

API key: env `OPENROUTER_API_KEY`, or set explicitly (`llm.StreamOptions.APIKey`, `agent.Config.APIKey`, or harness `GetAuth`).

## Tools

Generic and type-safe: model arguments are JSON-decoded into `Args`. Return an `error` to fail a tool — never embed errors in `Content`.

```go
type weatherArgs struct{ City string `json:"city"` }

tool := agent.NewTool(agent.ToolDef[weatherArgs]{
    Name:        "get_weather",
    Description: "Get the current weather for a city.",
    Schema:      map[string]any{ // JSON Schema for the params
        "type": "object",
        "properties": map[string]any{"city": map[string]any{"type": "string"}},
        "required": []string{"city"},
    },
    Run: func(ctx context.Context, callID string, args weatherArgs, onUpdate agent.UpdateFunc) (agent.ToolResult, error) {
        return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "22°C in " + args.City}}}, nil
    },
})
```

- `onUpdate(agent.ToolResult{...})` streams partial progress (emits `ToolExecutionUpdate`).
- Return `agent.ToolResult{Terminate: true}` to stop the loop after this batch (only honored if every tool in the batch terminates).
- Per-tool `Mode: agent.ModeSequential` forces the whole batch sequential; default is parallel.

## Run a turn (functional loop)

```go
ctx := &agent.Context{SystemPrompt: "Be concise. Use tools when relevant.", Tools: []agent.Tool{tool}}
cfg := &agent.Config{Model: model, Options: llm.StreamOptions{CacheRetention: llm.CacheShort}}

emit := func(e agent.Event) {
    switch ev := e.(type) {
    case agent.MessageUpdate: // streaming deltas
        if d, ok := ev.Event.(llm.TextDeltaEvent); ok { fmt.Print(d.Delta) }
    case agent.ToolExecutionStart:
        fmt.Printf("[tool] %s(%v)\n", ev.ToolName, ev.Args)
    }
}

msgs := agent.Run(context.Background(),
    []agent.AgentMessage{llm.TextUser("Weather in Tokyo?")}, ctx, cfg, emit)
```

`agent.Run` blocks and returns the new messages. Events: `AgentStart`, `TurnStart`, `MessageStart`/`MessageUpdate`/`MessageEnd`, `ToolExecutionStart`/`Update`/`End`, `TurnEnd`, `AgentEnd`. Streaming text arrives only via `MessageUpdate.Event.(llm.TextDeltaEvent)`.

## Stateful Agent

Owns the transcript and emits events; reconfigure between prompts with setters.

```go
a := agent.NewAgent(agent.AgentOptions{
    SystemPrompt: "...", Model: model, Tools: []agent.Tool{tool},
    Options: llm.StreamOptions{APIKey: key},
})
unsub := a.Subscribe(func(e agent.Event, _ context.Context) { /* ... */ })
defer unsub()
err := a.Prompt(context.Background(), "Hello")          // blocks until idle
_ = a.State().Messages                                  // snapshot
a.SetModel(other); a.SetThinkingLevel(llm.ThinkingMedium) // between prompts
a.Steer(llm.TextUser("...")); a.FollowUp(llm.TextUser("...")) // mid/post-run queues
```

## Harness + session persistence

```go
fsenv := env.NewOSEnv("")                                  // filesystem + shell
repo := session.NewJsonlSessionRepo(fsenv, sessionsDir)
sess, _ := repo.Create(ctx, session.JsonlCreateOptions{Cwd: fsenv.Cwd()})

h, _ := harness.NewAgentHarness(harness.AgentHarnessOptions{
    Env: fsenv, Session: sess, Tools: []agent.Tool{tool}, Model: model,
    SystemPrompt:  "...",
    GetAuth:       func(m *llm.Model) (*harness.Auth, error) { return &harness.Auth{APIKey: key}, nil },
    StreamOptions: harness.HarnessStreamOptions{CacheRetention: llm.CacheShort},
})
final, err := h.Prompt(ctx, "...")        // (*llm.AssistantMessage, error)
```

The harness persists every message to JSONL automatically. Reopen with `repo.Open(ctx, meta)` then `sess.BuildContext()`. Hooks: `h.OnToolCall` (block), `h.OnToolResult` (patch), `h.OnContext`, `h.OnBeforeProviderRequest/Payload`, `h.OnSessionBeforeCompact/Tree`. Lifecycle/own events via `h.Subscribe(func(event any, ctx context.Context))`. `h.Compact(ctx, instr)` and `h.NavigateTree(...)` need a model + `GetAuth`.

## Custom messages

`agent.AgentMessage` is any type with `Role() string`. The three LLM messages plus harness types (`message.BashExecutionMessage`, `CustomMessage`, `BranchSummaryMessage`, `CompactionSummaryMessage`) implement it. To inject your own, implement `Role()` and map it to LLM messages in `Config.ConvertToLLM` (the harness uses `message.ConvertToLLM`).

## Gotchas

- Streaming text is ONLY in `MessageUpdate.Event.(llm.TextDeltaEvent).Delta`. `MessageEnd.Message` carries the final assistant message.
- Vision needs `llm.InputImage` in `Model.Input` AND a vision-capable model. `anthropic/claude-3.5-haiku` rejects images; use `openai/gpt-4o-mini`, gemini, or claude-sonnet. Images: `&llm.Image{Data: base64Std, MimeType: "image/png"}` in a `UserMessage`.
- Anthropic cache control (`cache_control` markers) auto-applies for `anthropic/*` model ids on OpenRouter when `CacheRetention != CacheNone`.
- Pass a real `context.Context`; cancel it to abort a run (provider stream + tools observe it).
- Tool args must be JSON-decodable into the typed struct; a decode failure surfaces as an error tool result.
- `agent.Run` / `harness.Prompt` block. For concurrent steer/abort, run them in a goroutine and use `a.Steer`/`a.Abort` / `h.Steer`/`h.Abort` from another.

