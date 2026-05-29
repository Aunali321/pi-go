# Parity harness

Execution-parity tests comparing this Go port against the published
`@earendil-works/pi-agent-core` (npm). Identical inputs are run through both
implementations and the results are diffed byte-for-byte.

## Run

```sh
npm install        # once: pulls the reference npm package
./run-all.sh
```

## What it compares

1. **Request payloads** — the exact chat-completions body built for 14
   conversations (tools, anthropic cache_control short/long, reasoning params,
   DeepSeek format, images, cross-model thinking, id normalization, orphaned
   tool calls, ...). Captured via the `onPayload` hook on both sides.
2. **Response parsing** — the final `AssistantMessage` for 18 raw SSE fixtures
   (fragmented/late tool args, malformed-JSON repair, reasoning fields, cache
   tokens, finish reasons, encrypted reasoning_details, ...) served identically
   by a mock to both parsers.
3. **Compaction** — `prepareCompaction` cut-point math (token estimation,
   split-turn detection) on an identical session.
4. **Full harness loop** — a real 2-turn run (prompt → tool call → tool result
   → answer) driven through each implementation's own `AgentHarness` against a
   mock LLM; the request bodies each loop *produces on its own* are diffed.

The Go side is built from `../cmd/{dumppayload,dumpresp,dumpcompact,harnessrun}`.
