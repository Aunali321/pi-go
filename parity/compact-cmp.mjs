import { prepareCompaction } from "@earendil-works/pi-agent-core";

const usage = (n) => ({ input: n, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: n, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const texts = ["first question about the project setup and goals", "here is a detailed answer explaining the setup", "a follow up question about configuration", "another long detailed response with specifics", "third user turn asking something else entirely", "response number three with more detail here", "the most recent user question goes here now", "the final recent assistant response is here"];
const entries = texts.map((t, i) => {
  const id = "e" + (i + 1);
  const parentId = i === 0 ? null : "e" + i;
  const isUser = i % 2 === 0;
  const message = isUser
    ? { role: "user", content: [{ type: "text", text: t }], timestamp: i + 1 }
    : { role: "assistant", content: [{ type: "text", text: t }], api: "openai-completions", provider: "openrouter", model: "m", usage: usage(500 * (i + 1)), stopReason: "stop", timestamp: i + 1 };
  return { type: "message", id, parentId, timestamp: new Date((i + 1) * 1000).toISOString(), message };
});

const settings = { enabled: true, reserveTokens: 16384, keepRecentTokens: 50 };
const r = prepareCompaction(entries, settings);
const v = r.value;
const idx = entries.findIndex((e) => e.id === v.firstKeptEntryId);
const out = {
  tokensBefore: v.tokensBefore,
  isSplitTurn: v.isSplitTurn,
  firstKeptIndex: idx,
  summarizeRoles: v.messagesToSummarize.map((m) => m.role),
  summarizeTexts: v.messagesToSummarize.map((m) => (typeof m.content === "string" ? m.content : m.content.filter((c) => c.type === "text").map((c) => c.text).join(""))),
  prefixRoles: v.turnPrefixMessages.map((m) => m.role),
};
process.stdout.write(JSON.stringify(out, null, 2));
