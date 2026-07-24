# Parity harness

Execution-parity tests comparing this Go port against the published
`@earendil-works/pi-agent-core` / `@earendil-works/pi-ai` (npm, pinned at
0.82.x). Identical inputs are run through both implementations and the
results are diffed byte-for-byte.

## Run

```sh
npm install        # once: pulls the reference npm package
./run-all.sh
```

## What it compares

1. **Request payloads** — the exact chat-completions body built for 20
   conversations (tools, anthropic cache_control short/long, reasoning params,
   DeepSeek/zai/chat-template formats, images, cross-model thinking, id
   normalization with hash fallback, orphaned tool calls, kimi deferred tools,
   grammar/strict constrained-sampling tools, empty tool output, context-window
   maxTokens clamping, ...). Captured via the `onPayload` hook on both sides.
2. **Response parsing** — the final `AssistantMessage` for 20 raw SSE fixtures
   (fragmented/late tool args, malformed-JSON repair, reasoning fields, cache
   tokens, finish reasons, encrypted reasoning_details incl. buffered-before-
   tool-call, custom grammar tool deltas, ...) served identically by a mock to
   both parsers.
3. **Model catalog** — Go's OpenRouter discovery mapping/tweaks
   (`llm.FetchOpenRouterModels`) vs all models in the npm package's shipped
   catalog (thinking level maps, compat, metadata overrides).
4. **Built-in tools** — bash/read/write/edit executed on identical fixtures
   through the npm tools and the Go `harness/tools` package (truncation,
   diff/patch details, fuzzy matching, error messages).
5. **Compaction** — `prepareCompaction` cut-point math (token estimation,
   split-turn detection) on an identical session.
6. **Full harness loop** — a real 2-turn run (prompt → tool call → tool result
   → answer) driven through each implementation's own `AgentHarness` against a
   mock LLM; the request bodies each loop *produces on its own* are diffed.

The Go side is built from `../cmd/{dumppayload,dumpresp,dumpcompact,harnessrun}`.
