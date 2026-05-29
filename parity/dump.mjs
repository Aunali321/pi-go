import { streamSimple } from "@earendil-works/pi-ai";

const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };

function model(id, reasoning) {
  return {
    id, name: id, api: "openai-completions", provider: "openrouter",
    baseUrl: "https://openrouter.ai/api/v1", reasoning, input: ["text", "image"],
    cost: { input: 1, output: 1, cacheRead: 1, cacheWrite: 1 }, contextWindow: 200000, maxTokens: 1024,
  };
}

const weatherTool = {
  name: "get_weather", description: "Get the current weather for a city.",
  parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
};

const scenarios = {
  base: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "short", maxTokens: 1024 },
    context: {
      systemPrompt: "You are a helpful assistant.",
      messages: [
        { role: "user", content: [{ type: "text", text: "What's the weather in Paris?" }], timestamp: 1700000000000 },
        { role: "assistant", content: [{ type: "text", text: "Let me check." }, { type: "toolCall", id: "call_1", name: "get_weather", arguments: { city: "Paris" } }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "toolUse", timestamp: 1700000001000 },
        { role: "toolResult", toolCallId: "call_1", toolName: "get_weather", content: [{ type: "text", text: "12C overcast" }], isError: false, timestamp: 1700000002000 },
        { role: "user", content: [{ type: "text", text: "Thanks. Anything else notable?" }], timestamp: 1700000003000 },
      ],
      tools: [weatherTool],
    },
  },
  reasoning: {
    model: model("openai/gpt-4o-mini", true),
    options: { cacheRetention: "short", maxTokens: 2048, reasoning: "high" },
    context: { systemPrompt: "sys", messages: [{ role: "user", content: [{ type: "text", text: "hi" }], timestamp: 1 }] },
  },
  longcache: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "long", maxTokens: 1024 },
    context: { systemPrompt: "sys", messages: [{ role: "user", content: [{ type: "text", text: "hi" }], timestamp: 1 }], tools: [weatherTool] },
  },
  images: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "look" }, { type: "image", data: "AAAA", mimeType: "image/png" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "toolCall", id: "c1", name: "get_weather", arguments: {} }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "toolUse", timestamp: 2 },
        { role: "toolResult", toolCallId: "c1", toolName: "get_weather", content: [{ type: "text", text: "see image" }, { type: "image", data: "BBBB", mimeType: "image/jpeg" }], isError: false, timestamp: 3 },
      ],
      tools: [weatherTool],
    },
  },
  thinking: {
    model: model("anthropic/claude-3.5-haiku", true),
    options: { cacheRetention: "none", maxTokens: 1024, reasoning: "medium" },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "solve" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "thinking", thinking: "let me think", thinkingSignature: "reasoning_content" }, { type: "text", text: "answer" }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "stop", timestamp: 2 },
        { role: "user", content: [{ type: "text", text: "more" }], timestamp: 3 },
      ],
    },
  },
  multitoolresult: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "do two" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "toolCall", id: "a", name: "get_weather", arguments: { city: "A" } }, { type: "toolCall", id: "b", name: "get_weather", arguments: { city: "B" } }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "toolUse", timestamp: 2 },
        { role: "toolResult", toolCallId: "a", toolName: "get_weather", content: [{ type: "text", text: "AR" }], isError: false, timestamp: 3 },
        { role: "toolResult", toolCallId: "b", toolName: "get_weather", content: [{ type: "text", text: "BR" }], isError: false, timestamp: 4 },
      ],
      tools: [weatherTool],
    },
  },
  deepseek: {
    model: { id: "deepseek-chat", name: "deepseek", api: "openai-completions", provider: "deepseek", baseUrl: "https://api.deepseek.com", reasoning: true, input: ["text"], cost: { input: 1, output: 1, cacheRead: 1, cacheWrite: 1 }, contextWindow: 200000, maxTokens: 1024 },
    options: { cacheRetention: "none", maxTokens: 1024, reasoning: "high" },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "hi" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "text", text: "prev" }], api: "openai-completions", provider: "deepseek", model: "deepseek-chat", usage, stopReason: "stop", timestamp: 2 },
        { role: "user", content: [{ type: "text", text: "more" }], timestamp: 3 },
      ],
    },
  },
  imageonly: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: { systemPrompt: "sys", messages: [{ role: "user", content: [{ type: "image", data: "AAAA", mimeType: "image/png" }], timestamp: 1 }] },
  },
  toolhistory: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "x" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "toolCall", id: "h", name: "t", arguments: {} }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "toolUse", timestamp: 2 },
        { role: "toolResult", toolCallId: "h", toolName: "t", content: [{ type: "text", text: "r" }], isError: false, timestamp: 3 },
      ],
    },
  },
  reasoningoff: {
    model: model("openai/gpt-4o-mini", true),
    options: { cacheRetention: "short", maxTokens: 1024 },
    context: { systemPrompt: "sys", messages: [{ role: "user", content: [{ type: "text", text: "hi" }], timestamp: 1 }] },
  },
  crossmodel: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "q" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "thinking", thinking: "secret", thinkingSignature: "sig" }, { type: "thinking", thinking: "", thinkingSignature: "x", redacted: true }, { type: "text", text: "answer" }, { type: "toolCall", id: "tc1", name: "t", arguments: {} }], api: "openai-completions", provider: "openai", model: "gpt-4", usage, stopReason: "toolUse", timestamp: 2 },
        { role: "toolResult", toolCallId: "tc1", toolName: "t", content: [{ type: "text", text: "r" }], isError: false, timestamp: 3 },
      ],
      tools: [weatherTool],
    },
  },
  erroredmsg: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "a" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "text", text: "partial" }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "error", errorMessage: "boom", timestamp: 2 },
        { role: "user", content: [{ type: "text", text: "b" }], timestamp: 3 },
      ],
    },
  },
  idnormalize: {
    model: { id: "gpt-4o", name: "gpt-4o", api: "openai-completions", provider: "openai", baseUrl: "https://api.openai.com/v1", reasoning: false, input: ["text"], cost: { input: 1, output: 1, cacheRead: 1, cacheWrite: 1 }, contextWindow: 200000, maxTokens: 1024 },
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "q" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "toolCall", id: "call_abcdefghijabcdefghijabcdefghijabcdefghij", name: "t", arguments: {} }], api: "openai-completions", provider: "anthropic", model: "claude", usage, stopReason: "toolUse", timestamp: 2 },
        { role: "toolResult", toolCallId: "call_abcdefghijabcdefghijabcdefghijabcdefghij", toolName: "t", content: [{ type: "text", text: "r" }], isError: false, timestamp: 3 },
      ],
    },
  },
  orphan: {
    model: model("anthropic/claude-3.5-haiku", false),
    options: { cacheRetention: "none", maxTokens: 1024 },
    context: {
      systemPrompt: "sys",
      messages: [
        { role: "user", content: [{ type: "text", text: "go" }], timestamp: 1 },
        { role: "assistant", content: [{ type: "toolCall", id: "orphan1", name: "get_weather", arguments: { city: "X" } }], api: "openai-completions", provider: "openrouter", model: "anthropic/claude-3.5-haiku", usage, stopReason: "toolUse", timestamp: 2 },
        { role: "user", content: [{ type: "text", text: "next" }], timestamp: 3 },
      ],
      tools: [weatherTool],
    },
  },
};

const name = process.argv[2];
const s = scenarios[name];
if (!s) { process.stderr.write("unknown scenario " + name); process.exit(1); }

let captured;
const stream = streamSimple(s.model, s.context, {
  apiKey: "dummy", ...s.options,
  onPayload: (payload) => { captured = payload; throw new Error("stop"); },
});
await stream.result().catch(() => {});
process.stdout.write(JSON.stringify(captured, null, 2));
