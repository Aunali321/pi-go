# Work log

## 2026-07-24 — Port upstream pi v0.77.0 → v0.82.0

Upstream: /home/aun/Programming/Projects/ClonedProjects/pi (pulled; tags v0.77.0..v0.82.0).
The parity harness (`parity/`) pins `@earendil-works/pi-{ai,agent-core}` — bump 0.77.0 → 0.82.0 in the final phase.
Upstream restructure: providers/* split into api/* (openai-completions.ts moved to api/), estimation/uuid/retry/overflow moved into pi-ai utils, and pi-agent-core gained a `harness/tools/` suite (bash/read/write/edit/image) plus stream-fn.ts.

### Done: llm package (pi-ai changes)

- model.go: `ThinkingMax` level; `Pricing.Tiers` (tiered request-wide pricing); new ThinkingFormats (qwen-chat-template, chat-template, string-thinking, ant-ling); `SessionAffinityFormat`; `DeferredToolsKimi`; `ChatTemplateKwarg`; Compat gains ChatTemplateKwargs/VercelGatewayRouting/ZaiToolStream/SupportsOpenAIGrammarTools/DeferredToolsMode/SessionAffinityFormat.
- message.go: `Usage.CacheWrite1h`, `Usage.Reasoning *int` (pointer: TS omits it until a usage chunk arrives, then always writes it — parity), `ToolResultMessage.Usage/AddedToolNames`, `Tool.ConstrainedSampling` (+ JSON tags on Tool for estimate serialization).
- cost.go: tier selection + 2x-input pricing for 1h cache writes; xhigh AND max need explicit thinkingLevelMap entries.
- compat.go: full 0.82 detectCompat (nvidia/ant-ling/cloudflare/zai-coding-cn detection; OpenRouter developer-role only for anthropic|openai/* model ids; sessionAffinityFormat auto).
- completions.go: `clientAPIKey` (auth header ⇒ "unused"); session-affinity formats (openrouter ⇒ x-session-id); transport-level retry (`retryProviderRequest`: x-should-retry, retry-after(-ms), exp backoff w/ jitter, MaxRetryDelay cap); SDK-style error strings in httpError ("<status>: <error json>", metadata.raw dedup); buildParams: kimi deferred tools, tool_stream, chat_template_kwargs, new zai/deepseek/ant-ling/string-thinking reasoning formats, openRouterRouting/vercelGatewayRouting sent whenever set; StreamSimple clamps MaxTokens to context window (EstimateContextTokens + 4096 safety); Env overrides (PI_CACHE_RETENTION, env API keys).
- convert.go: tool-call id normalization keeps item id, shortHash fallback; "(no tool output)" placeholder; kimi system tool messages; grammar custom tool replay; convertTools strict/grammar (+error propagation).
- sse.go: refactored to sseState; grammar custom tool-call streaming (synthesized `{"prop":"..."} ` deltas via grammarInputBuffer); pending encrypted reasoning-detail buffering by tool-call id; usage reasoning tokens.
- cache.go: anthropic cache_control now also targets last `tool` message.
- New: hash.go (cyrb53 shortHash, UTF-16 faithful), grammar.go (constrained sampling), estimate.go (usage-anchored token estimation w/ timestamp prefix check + addedToolNames), overflow.go (IsContextOverflow), retry.go (RetryPolicy/RetryAssistantCall/IsRetryableAssistantError + provider retry), env.go (full provider env map, request-scoped Env).
- json.go: jsonMarshalJS/jsonString (JS-compatible escaping, no HTML escapes); ToolResult usage/addedToolNames (de)serialization.

Notes/decisions:
- Headers: `map[string]string` where empty value suppresses a default header (TS uses null).
- MaxRetryDelay: 0 ⇒ 60s default, negative ⇒ uncapped (TS: undefined/0 semantics).
- Reasoning-details signatures kept as raw JSON bytes (TS re-stringifies parsed object; equal for compact wire data, and Go re-marshal would reorder keys).
- transformMessages nil-content normalization not needed (typed slices).

### Done: agent package

- ToolResult gains Usage *llm.Usage + AddedToolNames; AfterToolResult gains Usage; toolResultMessage carries both (addedToolNames only when non-empty, matching TS).
- loop.go: "length"-stop tool calls all failed via failTruncatedToolCalls (truncated-arguments guard) instead of executed.
- execute.go: post-settlement onUpdate calls ignored (atomic guard).
- Agent.PrepareNextTurn hook now receives (ctx, TurnInfo) — upstream added prepareNextTurnWithContext alongside a legacy hook; Go keeps one hook with the context (no compat users).
- agent.Tool: optional ConstrainedSampler interface feeds llm.Tool.ConstrainedSampling.
- Kept Go's llm.StreamSimple default for Config.Stream (upstream removed the built-in default in favor of host-installed setDefaultStreamFn; Go's llm package IS the host runtime).

### Done: harness core

- AgentHarnessOptions: Env + GetAuth/Auth REMOVED (upstream: Models replaces both). New: Stream agent.StreamFunc (nil = llm.StreamSimple) and Retry *llm.RetryPolicy. SystemPromptContext lost Env. createStreamFn forwards MaxRetries/MaxRetryDelayMs now.
- Retry events: RetryScheduledEvent/RetryAttemptStartEvent/RetryFinishedEvent (operation "compaction"|"branch_summary"); harness.retryCallbacks bridges llm.RetryCallbacks.
- ToolResultEvent/ToolResultPatch/TreeSummary gain Usage.
- compaction: SummaryRunner{Stream,Model,ThinkingLevel,Retry,Callbacks}; completeWithRetries isolates summaries (cacheRetention none + fresh uuidv7 session id) and wraps llm.RetryAssistantCall; GenerateSummary returns usage; Compact returns Usage+RetainedTail (sequential split-turn calls per upstream, combineUsage); prepareCompaction builds RetainedTail; assistantUsage requires ctx tokens > 0; system prompt now says "AI assistant".
- session: entry short IDs from uuidv7 tail (slice(-8)); CompactionEntry{RetainedTail, Usage, optional FirstKeptEntryID}; BranchSummaryEntry.Usage; JSONL header metadata (create/fork pass-through); SessionStorage: GetSessionName/GetSessionStats/GetPathToRootOrCompaction (stops at retainedTail compaction or firstKeptEntryId)/GetEntries(*EntryCursor); Session.AppendCompaction takes CompactionInput; AppendSessionName strips newlines; context building refactored (ContextBuildOptions: EntryTransforms + custom-entry EntryProjectors; DefaultContextEntryTransform; compaction summary message + retainedTail).
- env: resolve() handles ~, ~/ and file:// paths; bash discovery (shellPath opt > /bin/bash > PATH bash > sh; Git Bash on Windows); ExecOptions.InheritEnv + float64 Timeout (validated, "timeout:<n>" error msg); cwd-existence check before exec; process-group kill (unix setpgid, windows taskkill) + Cleanup() kills active children; NewOSEnvWith{Cwd,ShellPath,ShellEnv}.

### Done: harness/tools (NEW package)

Ported the 0.82 pi-agent-core tools suite: truncate.go (head/tail truncation, byte-exact port), shellcapture.go (ExecuteShellWithCapture with live progress + full-output temp-file spooling), pathutil.go (@/Unicode path normalization, read-variant probing — needs golang.org/x/text for NFD/NFKC), filequeue.go (per-env canonical-path mutation serialization), image.go (jpeg/png/apng/gif/webp/bmp sniffing), bash.go (NewBashTool: throttled 100ms streaming updates, truncation notices, timeout/exit-code errors carrying output), read.go (offset/limit + image attachment + optional ReadImageProcessor), write.go, edit.go (NewEditTool with legacy oldText/newText + string-edits shims), editdiff.go (fuzzy matching: NFKC + smart-quote/dash/space normalization, overlap/duplicate detection, unchanged-line preservation), diffrender.go (Myers line diff + unified patch + numbered display diff; round-trip property-tested).
Tools take env.ExecutionEnv at construction (upstream's generic toolContext plumbing is TS-specific; Go closures cover it).
Note: write tool reports UTF-8 byte counts (TS reports UTF-16 code-unit length as "bytes").

### Done: parity vs npm 0.82.0

- parity/package.json bumped to ^0.82.0. JS scripts updated for the 0.82 surface: `streamSimple` now imported from `@earendil-works/pi-ai/compat`; harness-run.mjs passes a duck-typed `models` ({streamSimple, completeSimple}) instead of env/getApiKeyAndHeaders.
- Added 6 new payload scenarios (notooloutput, kimideferred, grammartool incl. strict json_schema, zaithinking, chattemplate, clampmax) and 2 new SSE fixtures (customtool grammar deltas, pendingdetail buffered reasoning detail).
- **Result: 42/42 byte-for-byte parity checks pass** (`parity/run-all.sh`), `go test ./...` and `go vet` clean, gofmt applied.
- The pre-existing idnormalize scenario now exercises the NEW 0.82 normalization (callId_itemId + shortHash fallback) on both sides and passes.

### Done: model discovery + deeper parity (follow-up session)

- NEW llm/discovery.go: `FetchOpenRouterModels` — pi has no runtime discovery; its OpenRouter catalog is generated at build time from GET /models (tool-capable filter, pricing ×1e6 rounded to 6dp, contextWindow/maxTokens fallbacks) and shipped as static data. The Go port does the same mapping at runtime. `ApplyOpenRouterCatalogTweaks` ports pi's hand-maintained per-model adjustments (gpt-5.x xhigh/max tiers, Claude adaptive-thinking tiers, fable-5, deepseek-v4 map, mercury-2, glm-5.2, kimi k3/k2.5/k2.6 metadata). The "~vendor/model-latest" ids come from the OpenRouter API itself.
- Catalog parity check (parity/models-cmp.mjs + cmd/dumpmodels): Go tweak output vs ALL 274 models in the shipped npm catalog — thinkingLevelMap, maxTokens and behavior-changing compat identical (detection-redundant baked fields normalized on both sides).
- This check FOUND two real divergences, now fixed: `~anthropic/*-latest` aliases need explicit cacheControlFormat=anthropic (runtime prefix detection misses the `~`), and openrouter deepseek-v4 models need requiresReasoningContentOnAssistantMessages=true.
- Tool-level parity (parity/tools-cmp.mjs + cmd/dumptools): npm createBash/Read/Write/EditTool vs Go tools on identical fixtures — 14 scenarios (truncation notices, offset/limit, edit diff+patch details, fuzzy edits, error messages, legacy args) — byte-identical, including the Myers-diff display diff and unified patch vs jsdiff.
- parity suite now 44/44 (payloads 20, SSE 20, catalog 1, compaction 1, tools 1, harness loop 1).

### Done: self-audit round (unprompted gaps)

Added parity for the areas the suite did NOT cover (the earlier "no divergences" claim was only as strong as the suite):
- Session files (parity/session-cmp.mjs + cmd/dumpsession): a rich JSONL session (thinking/model/tools changes, usage-bearing messages incl. reasoning + addedToolNames, retainedTail compaction w/ usage, custom message, label, session name, header metadata) written by each implementation and loaded by the other; full projection (context messages, stats, name, state) identical both directions.
- Compaction summarization requests (parity/compactreq-cmp.mjs + cmd/dumpcompactreq): Compact() end-to-end against a fake runner — captured prompt assembly (serializeConversation output, split-turn two-phase prompts, custom instructions, maxTokens math, cacheRetention none, session id isolation) + assembled result. FOUND+FIXED: computeFileLists returned nil slices → compaction details serialized modifiedFiles/readFiles as null vs TS [] (persisted into session entries).
- Overflow/retry classifiers (parity/classify-cmp.mjs + classify-fixtures.json + cmd/dumpclassify): 50 provider error strings + silent/length-stop usage cases through IsContextOverflow and IsRetryableAssistantError — verdicts identical.
- Suite now 48/48.

### Handoff notes

- Verified: 48/48 parity vs published npm 0.82.0 + full Go test suite. Not committed (user hasn't asked).
- Known intentional deviations (documented above): Headers "" = suppress (TS null), MaxRetryDelay 0/neg semantics, single PrepareNextTurn hook, tools take env at construction, write tool reports UTF-8 bytes, thought signatures kept as raw JSON.
- Out of scope (unchanged from original port): TUI, OAuth/auth registry, providers other than OpenAI-completions wire, proxy.ts, image-generation APIs, models catalog/registry.
