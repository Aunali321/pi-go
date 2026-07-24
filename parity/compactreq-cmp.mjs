// Compaction request parity: run compact() end-to-end against a fake model
// runner and capture the exact summarization requests (prompt assembly,
// serialized conversation, options) plus the assembled result.
import { compact, prepareCompaction } from "@earendil-works/pi-agent-core";

const usage = (n) => ({ input: n, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: n, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const texts = ["first question about the project setup and goals", "here is a detailed answer explaining the setup", "a follow up question about configuration", "another long detailed response with specifics", "third user turn asking something else entirely", "response number three with more detail here", "the most recent user question goes here now", "the final recent assistant response is here"];
const entries = texts.map((t, i) => {
  const id = "e" + (i + 1);
  const parentId = i === 0 ? null : "e" + i;
  const isUser = i % 2 === 0;
  const message = isUser
    ? { role: "user", content: [{ type: "text", text: t }], timestamp: i + 1 }
    : {
        role: "assistant",
        content: [
          { type: "text", text: t },
          { type: "toolCall", id: "tc" + i, name: "read", arguments: { path: "file" + i + ".txt" } },
        ],
        api: "openai-completions", provider: "openrouter", model: "m", usage: usage(500 * (i + 1)), stopReason: "stop", timestamp: i + 1,
      };
  return { type: "message", id, parentId, timestamp: new Date((i + 1) * 1000).toISOString(), message };
});

const model = {
  id: "m", name: "m", api: "openai-completions", provider: "openrouter",
  baseUrl: "https://openrouter.ai/api/v1", reasoning: false, input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 4096,
};

const captured = [];
const fakeResponse = {
  role: "assistant", content: [{ type: "text", text: "GENERATED SUMMARY" }],
  api: "openai-completions", provider: "openrouter", model: "m",
  usage: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0, totalTokens: 3, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
  stopReason: "stop", timestamp: 1,
};
const models = {
  completeSimple: async (m, context, options) => {
    captured.push({
      systemPrompt: context.systemPrompt,
      messages: context.messages.map((msg) => ({ role: msg.role, text: msg.content.map((c) => c.text).join("") })),
      maxTokens: options.maxTokens ?? null,
      cacheRetention: options.cacheRetention ?? null,
      reasoning: options.reasoning ?? null,
      sessionIdSet: typeof options.sessionId === "string" && options.sessionId.length > 0,
    });
    return fakeResponse;
  },
};

const settings = { enabled: true, reserveTokens: 16384, keepRecentTokens: 50 };
const prep = prepareCompaction(entries, settings).value;
const result = await compact(prep, models, model, "focus on the tests", undefined, "off");
const v = result.value;
process.stdout.write(JSON.stringify({
  captured,
  result: {
    summary: v.summary,
    tokensBefore: v.tokensBefore,
    firstKeptIndex: entries.findIndex((e) => e.id === v.firstKeptEntryId),
    retainedTailRoles: v.retainedTail.map((m) => m.role),
    usage: v.usage,
    details: v.details,
  },
}, null, 2));
