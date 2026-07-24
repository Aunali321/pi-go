import http from "node:http";

const base = { id: "resp-1", object: "chat.completion.chunk", created: 1, model: "test-model" };
const usage = { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 };
function c(choices, u) { return { ...base, choices, ...(u ? { usage: u } : {}) }; }
function d(delta, finish = null) { return c([{ index: 0, delta, finish_reason: finish }]); }

const fixtures = {
  text: [d({ role: "assistant", content: "Hello" }), d({ content: " world" }), d({}, "stop"), c([], usage)],
  toolfrag: [
    d({ role: "assistant", tool_calls: [{ index: 0, id: "call_1", type: "function", function: { name: "get_weather", arguments: "" } }] }),
    d({ tool_calls: [{ index: 0, function: { arguments: '{"ci' } }] }),
    d({ tool_calls: [{ index: 0, function: { arguments: 'ty":"Par' } }] }),
    d({ tool_calls: [{ index: 0, function: { arguments: 'is"}' } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  multitool: [
    d({ role: "assistant", tool_calls: [{ index: 0, id: "c0", type: "function", function: { name: "a", arguments: "{}" } }] }),
    d({ tool_calls: [{ index: 1, id: "c1", type: "function", function: { name: "b", arguments: '{"x":1}' } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  reasoning_content: [
    d({ role: "assistant", reasoning_content: "thinking" }),
    d({ reasoning_content: " more" }),
    d({ content: "answer" }),
    d({}, "stop"), c([], usage),
  ],
  reasoning_field: [d({ reasoning: "r1" }), d({ content: "a" }), d({}, "stop"), c([], usage)],
  cachetokens: [d({ role: "assistant", content: "hi" }), d({}, "stop"), c([], { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120, prompt_tokens_details: { cached_tokens: 50 } })],
  lengthfinish: [d({ content: "truncated" }), d({}, "length"), c([], usage)],
  contentfilter: [d({ content: "x" }), d({}, "content_filter"), c([], usage)],
  customtool: [
    d({ role: "assistant", tool_calls: [{ index: 0, id: "g1", type: "custom", custom: { name: "run_sql", input: "" } }] }),
    d({ tool_calls: [{ index: 0, custom: { input: "select" } }] }),
    d({ tool_calls: [{ index: 0, custom: { input: " 1" } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  pendingdetail: [
    d({ role: "assistant", reasoning_details: [{ type: "reasoning.encrypted", id: "c9", data: "ENC" }] }),
    d({ tool_calls: [{ index: 0, id: "c9", type: "function", function: { name: "a", arguments: "{}" } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  reasoning_details: [
    d({ role: "assistant", tool_calls: [{ index: 0, id: "c0", type: "function", function: { name: "a", arguments: "{}" } }] }),
    d({ reasoning_details: [{ type: "reasoning.encrypted", id: "c0", data: "ENC" }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  responsemodel: [d({ role: "assistant", content: "hi" }, null), { ...base, model: "actual-model", choices: [{ index: 0, delta: {}, finish_reason: "stop" }] }, c([], usage)],
  malformed: [
    d({ role: "assistant", tool_calls: [{ index: 0, id: "m1", type: "function", function: { name: "q", arguments: "" } }] }),
    d({ tool_calls: [{ index: 0, function: { arguments: '{"q":"a\nb"}' } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  texttool: [
    d({ role: "assistant", content: "calling now" }),
    d({ tool_calls: [{ index: 0, id: "t1", type: "function", function: { name: "go", arguments: "{}" } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
  bothreasoning: [d({ role: "assistant", reasoning_content: "first", reasoning: "second" }), d({ content: "x" }), d({}, "stop"), c([], usage)],
  cachewrite: [d({ role: "assistant", content: "hi" }), d({}, "stop"), c([], { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120, prompt_tokens_details: { cached_tokens: 30, cache_write_tokens: 10 } })],
  choiceusage: [d({ role: "assistant", content: "hi" }), { ...base, choices: [{ index: 0, delta: {}, finish_reason: "stop", usage: { prompt_tokens: 7, completion_tokens: 3, total_tokens: 10 } }] }],
  functioncall: [d({ role: "assistant", tool_calls: [{ index: 0, id: "f1", type: "function", function: { name: "go", arguments: "{}" } }] }), d({}, "function_call"), c([], usage)],
  nousage: [d({ role: "assistant", content: "hi" }), d({}, "stop")],
  lateid: [
    d({ role: "assistant", tool_calls: [{ index: 0, type: "function", function: { name: "go", arguments: "{" } }] }),
    d({ tool_calls: [{ index: 0, id: "late1", function: { arguments: '"a":1}' } }] }),
    d({}, "tool_calls"), c([], usage),
  ],
};

function sse(chunks) {
  return chunks.map((ch) => `data: ${JSON.stringify(ch)}\n\n`).join("") + "data: [DONE]\n\n";
}

const server = http.createServer((req, res) => {
  let body = "";
  req.on("data", (x) => (body += x));
  req.on("end", () => {
    const fx = req.headers["x-fixture"];
    const chunks = fixtures[fx];
    if (!chunks) { res.writeHead(400).end("unknown fixture " + fx); return; }
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    res.end(sse(chunks));
  });
});
server.listen(8766, "127.0.0.1", () => process.stdout.write("MOCK_READY\n"));
process.stdout.write("FIXTURES:" + Object.keys(fixtures).join(",") + "\n");
