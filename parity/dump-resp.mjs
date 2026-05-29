import { streamSimple } from "@earendil-works/pi-ai";

const fixture = process.argv[2];
const model = {
  id: "test-model", name: "test-model", api: "openai-completions", provider: "openrouter",
  baseUrl: "http://127.0.0.1:8766/v1", reasoning: true, input: ["text"],
  cost: { input: 1, output: 1, cacheRead: 1, cacheWrite: 1 }, contextWindow: 200000, maxTokens: 1024,
  headers: { "x-fixture": fixture },
};
const context = { systemPrompt: "s", messages: [{ role: "user", content: [{ type: "text", text: "hi" }], timestamp: 1 }] };

const stream = streamSimple(model, context, { apiKey: "dummy" });
const result = await stream.result();
delete result.timestamp;
process.stdout.write(JSON.stringify(result, null, 2));
