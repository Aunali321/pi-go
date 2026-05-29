# Examples

Each is a standalone `main.go`. All except where noted require `OPENROUTER_API_KEY`.

```sh
export OPENROUTER_API_KEY=sk-or-...
go run ./examples/openrouter      # basic: agent.Run with one tool
go run ./examples/agentclass      # stateful Agent: Subscribe + Prompt + State
go run ./examples/harness         # AgentHarness + JSONL session persisted to disk, then reopened
go run ./examples/tools           # multiple tools + an afterToolCall (OnToolResult) hook
go run ./examples/vision <img.png># image input to a vision-capable model
```

Notes:
- `vision` defaults to `openai/gpt-4o-mini`; override with `PI_TEST_MODEL`.
  Some models (e.g. `anthropic/claude-3.5-haiku`) reject image input.
- `harness` writes a real `.jsonl` session under a temp dir and prints its path,
  then reopens it to show the reconstructed transcript.
