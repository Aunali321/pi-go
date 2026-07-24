import { AgentHarness, JsonlSessionRepo } from "@earendil-works/pi-agent-core";
import { NodeExecutionEnv } from "@earendil-works/pi-agent-core/node";
import { streamSimple } from "@earendil-works/pi-ai/compat";
import { readFileSync } from "node:fs";

function visionModel() {
  return { ...model(), input: ["text", "image"] };
}

const MODEL = process.env.PI_TEST_MODEL || "anthropic/claude-3.5-haiku";

function model() {
  return {
    id: MODEL, name: MODEL, api: "openai-completions", provider: "openrouter",
    baseUrl: "https://openrouter.ai/api/v1", reasoning: false, input: ["text"],
    cost: { input: 0.8, output: 4, cacheRead: 0.08, cacheWrite: 1 }, contextWindow: 200000, maxTokens: 1024,
  };
}

function textOf(content) {
  if (typeof content === "string") return content;
  return content.filter((c) => c.type === "text").map((c) => c.text).join("");
}
function summary(messages) {
  return messages.map((m) => ({ role: m.role, text: textOf(m.content).slice(0, 80) }));
}

const mode = process.argv[2];
const env = new NodeExecutionEnv({ cwd: process.cwd() });

if (mode === "run") {
  const sessionsRoot = process.argv[3];
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot });
  const session = await repo.create({ cwd: env.cwd });
  const tool = {
    name: "get_weather", label: "Weather", description: "Get the current weather for a city.",
    parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
    execute: async (_id, params) => ({ content: [{ type: "text", text: `It is 14C and windy in ${params.city}.` }], details: { city: params.city } }),
  };
  const harness = new AgentHarness({
    env, session, tools: [tool], model: model(),
    systemPrompt: "You MUST call the get_weather tool to answer any weather question, then give a one-sentence answer.",
    getApiKeyAndHeaders: async () => ({ apiKey: process.env.OPENROUTER_API_KEY }),
    streamOptions: { cacheRetention: "short" },
  });
  const final = await harness.prompt("What's the weather in Berlin?");
  const meta = await session.getMetadata();
  const ctx = await session.buildContext();
  process.stdout.write(JSON.stringify({ impl: "js", path: meta.path, stopReason: final.stopReason, summary: summary(ctx.messages) }, null, 2));
} else if (mode === "run2") {
  const sessionsRoot = process.argv[3];
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot });
  const session = await repo.create({ cwd: env.cwd });
  const tool = {
    name: "get_weather", label: "Weather", description: "Get the current weather for a city.",
    parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
    execute: async (_id, params) => ({ content: [{ type: "text", text: `It is 14C and windy in ${params.city}.` }], details: { city: params.city } }),
  };
  const harness = new AgentHarness({
    env, session, tools: [tool], model: model(),
    systemPrompt: "You MUST call the get_weather tool to answer any weather question, then give a one-sentence answer.",
    getApiKeyAndHeaders: async () => ({ apiKey: process.env.OPENROUTER_API_KEY }),
    streamOptions: { cacheRetention: "short" },
  });
  await harness.setThinkingLevel("medium");
  await harness.prompt("What's the weather in Berlin?");
  await harness.prompt("And in Paris?");
  const meta = await session.getMetadata();
  const ctx = await session.buildContext();
  const entries = await session.getEntries();
  process.stdout.write(JSON.stringify({ impl: "js", path: meta.path, thinkingLevel: ctx.thinkingLevel, entryTypes: entries.map((e) => e.type), roles: ctx.messages.map((m) => m.role) }, null, 2));
} else if (mode === "run3") {
  const sessionsRoot = process.argv[3];
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot });
  const session = await repo.create({ cwd: env.cwd });
  const tool = {
    name: "get_weather", label: "Weather", description: "Get the current weather for a city.",
    parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
    execute: async (_id, params) => ({ content: [{ type: "text", text: `It is 14C and windy in ${params.city}.` }], details: { city: params.city } }),
  };
  const harness = new AgentHarness({
    env, session, tools: [tool], model: model(),
    systemPrompt: "You MUST call the get_weather tool to answer any weather question, then give a one-sentence answer.",
    getApiKeyAndHeaders: async () => ({ apiKey: process.env.OPENROUTER_API_KEY }),
    streamOptions: { cacheRetention: "short" },
  });
  await harness.prompt("What's the weather in Berlin?");
  await session.appendCustomMessageEntry("note", [{ type: "text", text: "a side note" }], true);
  await harness.setModel({ id: "qwen/qwen-2.5-72b-instruct", name: "qwen", api: "openai-completions", provider: "openrouter", baseUrl: "https://openrouter.ai/api/v1", reasoning: false, input: ["text"], cost: { input: 1, output: 1, cacheRead: 1, cacheWrite: 1 }, contextWindow: 32000, maxTokens: 1024 });
  await harness.setActiveTools(["get_weather"]);
  const meta = await session.getMetadata();
  const ctx = await session.buildContext();
  const entries = await session.getEntries();
  process.stdout.write(JSON.stringify({ impl: "js", path: meta.path, model: ctx.model, entryTypes: entries.map((e) => e.type), roles: ctx.messages.map((m) => m.role) }, null, 2));
} else if (mode === "read") {
  const filePath = process.argv[3];
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot: "/" });
  const session = await repo.open({ id: "x", createdAt: "x", cwd: env.cwd, path: filePath });
  const ctx = await session.buildContext();
  const entries = await session.getEntries();
  process.stdout.write(JSON.stringify({ impl: "js-read", thinkingLevel: ctx.thinkingLevel, model: ctx.model, entryTypes: entries.map((e) => e.type), roles: ctx.messages.map((m) => m.role), summary: summary(ctx.messages) }, null, 2));
} else if (mode === "imgpayload") {
  const data = readFileSync(process.argv[3]).toString("base64");
  const context = { systemPrompt: "You are a helpful assistant.", messages: [{ role: "user", content: [{ type: "text", text: "Describe this screenshot in one sentence." }, { type: "image", data, mimeType: "image/png" }], timestamp: 1 }] };
  let captured;
  const s = streamSimple(visionModel(), context, { apiKey: "dummy", cacheRetention: "none", maxTokens: 1024, onPayload: (p) => { captured = p; throw new Error("stop"); } });
  await s.result().catch(() => {});
  process.stdout.write(JSON.stringify(captured, null, 2));
} else if (mode === "vision") {
  const data = readFileSync(process.argv[3]).toString("base64");
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot: process.argv[4] });
  const session = await repo.create({ cwd: env.cwd });
  const harness = new AgentHarness({
    env, session, tools: [], model: visionModel(),
    systemPrompt: "You are a helpful assistant. Describe images concisely.",
    getApiKeyAndHeaders: async () => ({ apiKey: process.env.OPENROUTER_API_KEY }),
    streamOptions: { cacheRetention: "none" },
  });
  const final = await harness.prompt("Describe this screenshot in one sentence.", { images: [{ type: "image", data, mimeType: "image/png" }] });
  const ctx = await session.buildContext();
  process.stdout.write(JSON.stringify({ impl: "js", stopReason: final.stopReason, text: textOf(final.content), roles: ctx.messages.map((m) => m.role) }, null, 2));
} else if (mode === "filetool") {
  const dir = process.argv[3];
  const readTool = {
    name: "read_file", label: "Read", description: "Read a text file by name from the working directory.",
    parameters: { type: "object", properties: { name: { type: "string" } }, required: ["name"] },
    execute: async (_id, params) => {
      const r = await env.readTextFile(`${dir}/${params.name}`);
      return { content: [{ type: "text", text: r.ok ? r.value : `error: ${r.error.message}` }], details: {} };
    },
  };
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot: process.argv[4] });
  const session = await repo.create({ cwd: env.cwd });
  const harness = new AgentHarness({
    env, session, tools: [readTool], model: model(),
    systemPrompt: "You MUST use the read_file tool to read files. Then answer.",
    getApiKeyAndHeaders: async () => ({ apiKey: process.env.OPENROUTER_API_KEY }),
    streamOptions: { cacheRetention: "none" },
  });
  const final = await harness.prompt("Read the file notes.txt and tell me the secret code it contains.");
  const ctx = await session.buildContext();
  process.stdout.write(JSON.stringify({ impl: "js", stopReason: final.stopReason, text: textOf(final.content), roles: ctx.messages.map((m) => m.role) }, null, 2));
} else if (mode === "tool") {
  const feature = process.argv[3];
  const root = process.argv[4];
  const ev = { toolEnds: [], updateCount: 0, executed: [] };
  const repo = new JsonlSessionRepo({ fs: env, sessionsRoot: root });
  const session = await repo.create({ cwd: env.cwd });

  const weather = (opts = {}) => ({
    name: "get_weather", label: "W", description: "Get the current weather for a city.",
    parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
    execute: async (_id, params, _signal, onUpdate) => {
      ev.executed.push(params.city || "?");
      if (opts.update && onUpdate) onUpdate({ content: [{ type: "text", text: "working..." }], details: {} });
      if (opts.error) throw new Error("tool boom");
      const r = { content: [{ type: "text", text: `14C in ${params.city}` }], details: {} };
      if (opts.terminate) r.terminate = true;
      return r;
    },
  });

  let tools = [], prompt = "Get the weather in Berlin using the get_weather tool.", configure = () => {};
  if (feature === "error") tools = [weather({ error: true })];
  else if (feature === "terminate") tools = [weather({ terminate: true })];
  else if (feature === "update") tools = [weather({ update: true })];
  else if (feature === "block") { tools = [weather()]; configure = (h) => h.on("tool_call", () => ({ block: true, reason: "blocked by test" })); }
  else if (feature === "patch") { tools = [weather()]; configure = (h) => h.on("tool_result", () => ({ content: [{ type: "text", text: "PATCHED" }] })); }
  else if (feature === "multi") { tools = [weather()]; prompt = "Get the weather in BOTH Berlin and Paris. You must call get_weather separately for each city."; }

  const harness = new AgentHarness({
    env, session, tools, model: model(),
    systemPrompt: "You must use the provided tools to answer. Always call the tool when asked.",
    getApiKeyAndHeaders: async () => ({ apiKey: process.env.OPENROUTER_API_KEY }),
    streamOptions: { cacheRetention: "none" },
  });
  configure(harness);
  harness.subscribe((e) => {
    if (e.type === "tool_execution_end") ev.toolEnds.push({ toolName: e.toolName, isError: e.isError });
    if (e.type === "tool_execution_update") ev.updateCount++;
  });
  await harness.prompt(prompt);
  const ctx = await session.buildContext();
  const toolResults = ctx.messages.filter((m) => m.role === "toolResult").map((m) => ({ text: textOf(m.content), isError: m.isError }));
  process.stdout.write(JSON.stringify({ impl: "js", feature, toolEnds: ev.toolEnds, updateCount: ev.updateCount, executed: ev.executed, roles: ctx.messages.map((m) => m.role), toolResults }, null, 2));
} else {
  process.stderr.write("usage: live.mjs run|run2|run3|vision|filetool|tool <feature> <dir> | read <file> | imgpayload <img>\n");
  process.exit(1);
}
