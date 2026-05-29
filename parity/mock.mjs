import http from "node:http";
import fs from "node:fs";

const OUT = process.env.MOCK_OUT || "/tmp/pi-parity/requests.jsonl";
fs.writeFileSync(OUT, "");

function chunk(obj) { return `data: ${JSON.stringify(obj)}\n\n`; }

function toolCallResponse() {
  const base = { id: "gen-1", object: "chat.completion.chunk", created: 1, model: "anthropic/claude-3.5-haiku" };
  return [
    chunk({ ...base, choices: [{ index: 0, delta: { role: "assistant", content: null, tool_calls: [{ index: 0, id: "call_test_1", type: "function", function: { name: "get_weather", arguments: '{"city":"Paris"}' } }] }, finish_reason: null }] }),
    chunk({ ...base, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }] }),
    chunk({ ...base, choices: [], usage: { prompt_tokens: 100, completion_tokens: 10, total_tokens: 110 } }),
    "data: [DONE]\n\n",
  ].join("");
}

function finalResponse() {
  const base = { id: "gen-2", object: "chat.completion.chunk", created: 2, model: "anthropic/claude-3.5-haiku" };
  return [
    chunk({ ...base, choices: [{ index: 0, delta: { role: "assistant", content: "It is 12C and overcast in Paris." }, finish_reason: null }] }),
    chunk({ ...base, choices: [{ index: 0, delta: {}, finish_reason: "stop" }] }),
    chunk({ ...base, choices: [], usage: { prompt_tokens: 120, completion_tokens: 12, total_tokens: 132 } }),
    "data: [DONE]\n\n",
  ].join("");
}

const server = http.createServer((req, res) => {
  if (req.method !== "POST") { res.writeHead(404).end(); return; }
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    const client = req.headers["x-client"] || "unknown";
    let parsed;
    try { parsed = JSON.parse(body); } catch { parsed = {}; }
    fs.appendFileSync(OUT, JSON.stringify({ client, body: parsed }) + "\n");
    const hasToolResult = Array.isArray(parsed.messages) && parsed.messages.some((m) => m.role === "tool");
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    res.end(hasToolResult ? finalResponse() : toolCallResponse());
  });
});

server.listen(8765, "127.0.0.1", () => {
  process.stdout.write("MOCK_READY\n");
});
