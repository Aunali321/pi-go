// Session-file parity: build a rich JSONL session, print a full projection
// of what loading it yields (context messages, stats, name, state). Modes:
//   node session-cmp.mjs write <dir>   — create session via npm impl, print path
//   node session-cmp.mjs read <path>   — load a session file, print projection
import { JsonlSessionRepo, loadJsonlSessionMetadata } from "@earendil-works/pi-agent-core";
import { NodeExecutionEnv } from "@earendil-works/pi-agent-core/node";

const usage = (n) => ({
  input: n, output: n + 1, cacheRead: n + 2, cacheWrite: n + 3, reasoning: 5,
  totalTokens: 4 * n + 6,
  cost: { input: 0.1, output: 0.2, cacheRead: 0.3, cacheWrite: 0.4, total: 1 },
});

async function buildSession(dir) {
  const env = new NodeExecutionEnv({ cwd: dir });
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot: dir + "/sessions" });
  const session = await repo.create({ cwd: dir, metadata: { app: "parity", n: 1 } });

  await session.appendMessage({ role: "user", content: [{ type: "text", text: "first question" }], timestamp: 1700000000001 });
  await session.appendMessage({
    role: "assistant",
    content: [
      { type: "thinking", thinking: "pondering", thinkingSignature: "reasoning_content" },
      { type: "text", text: "calling tool" },
      { type: "toolCall", id: "c1", name: "get_weather", arguments: { city: "Paris" } },
    ],
    api: "openai-completions", provider: "openrouter", model: "m1",
    usage: usage(100), stopReason: "toolUse", timestamp: 1700000000002,
  });
  await session.appendMessage({
    role: "toolResult", toolCallId: "c1", toolName: "get_weather",
    content: [{ type: "text", text: "12C" }], usage: usage(10), addedToolNames: ["extra_tool"],
    isError: false, timestamp: 1700000000003,
  });
  await session.appendThinkingLevelChange("high");
  await session.appendModelChange("openrouter", "m2");
  await session.appendActiveToolsChange(["get_weather", "extra_tool"]);
  await session.appendCustomMessageEntry(
    "note", [{ type: "text", text: "custom note" }], true, { k: "v" },
  );
  await session.appendCompaction(
    "the summary", undefined, 4321, { readFiles: ["a.txt"], modifiedFiles: [] }, false, usage(50),
    [
      { role: "user", content: [{ type: "text", text: "kept user" }], timestamp: 1700000000004 },
      {
        role: "assistant", content: [{ type: "text", text: "kept answer" }],
        api: "openai-completions", provider: "openrouter", model: "m2",
        usage: usage(20), stopReason: "stop", timestamp: 1700000000005,
      },
    ],
  );
  await session.appendMessage({ role: "user", content: [{ type: "text", text: "after compaction" }], timestamp: 1700000000006 });
  await session.appendSessionName("  My\nSession  ");
  const entries = await session.getEntries();
  await session.appendLabel(entries[0].id, "start");
  return (await session.getMetadata()).path;
}

function textOf(content) {
  if (typeof content === "string") return content;
  return content.filter((c) => c.type === "text").map((c) => c.text).join("|");
}

async function project(path) {
  const env = new NodeExecutionEnv({ cwd: "/" });
  const meta = await loadJsonlSessionMetadata(env, path);
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot: "/" });
  const session = await repo.open(meta);
  const ctx = await session.buildContext();
  const entries = await session.getEntries();
  return {
    headerMetadata: meta.metadata ?? null,
    entryTypes: entries.map((e) => e.type),
    label: await session.getLabel(entries[0].id) ?? null,
    name: (await session.getSessionName()) ?? null,
    stats: await session.getSessionStats(),
    thinkingLevel: ctx.thinkingLevel,
    model: ctx.model,
    activeToolNames: ctx.activeToolNames,
    messages: ctx.messages.map((m) => ({
      role: m.role,
      text: textOf(m.content ?? []),
      summary: m.summary ?? null,
      usageTotal: m.usage?.totalTokens ?? null,
      reasoning: m.usage?.reasoning ?? null,
      addedToolNames: m.addedToolNames ?? null,
    })),
  };
}

const [mode, arg] = process.argv.slice(2);
if (mode === "write") {
  process.stdout.write(await buildSession(arg));
} else {
  process.stdout.write(JSON.stringify(await project(arg), null, 2));
}
