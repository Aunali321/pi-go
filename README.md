# pi-go

A Go port of the [pi](https://pi.dev) agent runtime, targeting **OpenRouter** (the
OpenAI chat-completions wire format). It ports the parts that matter for running
an agent: the turn loop, tool calling, streaming, message conversion, and prompt
cache control. The TUI and coding tools are out of scope.

Requires Go 1.26.

## Packages

Layered as a clean dependency DAG (each imports only those above it):

- `llm` — provider/wire layer: typed messages/content, model + compatibility
  settings, streaming over HTTP/SSE, message conversion, reasoning parameters,
  Anthropic-style cache control. No third-party dependencies.
- `agent` — the runtime: the turn loop, generic typed tools,
  sequential/parallel tool execution, steering/follow-up queues, the stateful
  `Agent`, and the `before`/`after`/`stop`/`next-turn` hooks.
- `harness/env` — `ExecutionEnv` (filesystem + shell) and an OS-backed impl.
- `harness/message` — application message types (bash/custom/summary) and
  `ConvertToLLM`.
- `harness/session` — the branching session tree: entries, in-memory + JSONL
  storage, repos, `Session`, context reconstruction, UUIDv7.
- `harness/compaction` — token estimation, compaction cut-point selection,
  summary + branch-summary generation.
- `harness` — the `AgentHarness` orchestrator: ties the agent loop to a
  persistent session with resources, compaction, branch navigation, and a rich
  event/hook system. Also skills, prompt templates, and system-prompt assembly.

Parity/debug commands (compare this port against the npm package) live in
`cmd/`; see `parity/`.

## Usage

```go
model := &llm.Model{
    ID:        "anthropic/claude-sonnet-4.5",
    Reasoning: true,
    Input:     []llm.InputModality{llm.InputText},
    Cost:      llm.Pricing{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
}

weather := agent.NewTool(agent.ToolDef[WeatherArgs]{
    Name:        "get_weather",
    Description: "Get the current weather for a city.",
    Schema:      map[string]any{ /* JSON Schema */ },
    Run: func(ctx context.Context, id string, args WeatherArgs, up agent.UpdateFunc) (agent.ToolResult, error) {
        return agent.ToolResult{Content: []llm.Content{&llm.Text{Text: "22°C"}}}, nil
    },
})

ctx := &agent.Context{SystemPrompt: "...", Tools: []agent.Tool{weather}}
cfg := &agent.Config{Model: model, Options: llm.StreamOptions{CacheRetention: llm.CacheShort}}

agent.Run(context.Background(),
    []llm.Message{llm.TextUser("Weather in Tokyo?")}, ctx, cfg, emit)
```

`emit` receives `agent.Event` values (turn/message/tool lifecycle). See
`examples/openrouter`.

## Design notes

- **Streaming** is a `*llm.Stream`: a channel of events plus a blocking
  `Result()`. Backpressure is the channel buffer.
- **Cancellation** is `context.Context` throughout.
- **Tools** are generic (`ToolDef[Args]`): raw model arguments are decoded into
  the typed `Args`; a decode failure becomes an error tool result.
- **Custom providers** implement `agent.StreamFunc`; `llm.StreamFromMessage`
  builds a stream from a finished message.

## Tests

```sh
go test ./...
```

The agent integration test hits OpenRouter and is skipped unless
`OPENROUTER_API_KEY` is set.
