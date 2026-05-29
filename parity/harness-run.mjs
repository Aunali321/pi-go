import { AgentHarness, InMemorySessionRepo } from "@earendil-works/pi-agent-core";
import { NodeExecutionEnv } from "@earendil-works/pi-agent-core/node";

const model = {
  id: "anthropic/claude-3.5-haiku", name: "Claude 3.5 Haiku", api: "openai-completions",
  provider: "openrouter", baseUrl: "http://127.0.0.1:8765/v1", reasoning: false, input: ["text"],
  cost: { input: 0.8, output: 4, cacheRead: 0.08, cacheWrite: 1 }, contextWindow: 200000, maxTokens: 1024,
};

const tool = {
  name: "get_weather", label: "Weather", description: "Get the current weather for a city.",
  parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
  execute: async (_id, params) => ({ content: [{ type: "text", text: "12C overcast" }], details: { city: params.city } }),
};

const env = new NodeExecutionEnv({ cwd: process.cwd() });
const repo = new InMemorySessionRepo();
const session = await repo.create();

const harness = new AgentHarness({
  env, session, tools: [tool], model,
  systemPrompt: "You are a helpful assistant.",
  getApiKeyAndHeaders: async () => ({ apiKey: "dummy" }),
  streamOptions: { cacheRetention: "short", headers: { "x-client": "js" } },
});

const final = await harness.prompt("What's the weather in Paris?");
const ctx = await session.buildContext();
const roles = ctx.messages.map((m) => m.role);
process.stdout.write(JSON.stringify({ stopReason: final.stopReason, roles }, null, 2));
