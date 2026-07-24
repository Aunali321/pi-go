// Classifier parity: isContextOverflow and isRetryableAssistantError verdicts
// over a shared table of provider error strings plus usage-based cases.
import { readFileSync } from "node:fs";
import { isContextOverflow, isRetryableAssistantError } from "@earendil-works/pi-ai/compat";

const zero = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
const msg = (stopReason, errorMessage, usage = zero) => ({
  role: "assistant", content: [], api: "openai-completions", provider: "openrouter",
  model: "m", usage, stopReason, errorMessage, timestamp: 1,
});

const errors = JSON.parse(readFileSync("classify-fixtures.json", "utf8"));
const out = { overflow: {}, retryable: {} };
for (const e of errors) {
  out.overflow[e] = isContextOverflow(msg("error", e), 200000);
  out.retryable[e] = isRetryableAssistantError(msg("error", e));
}
out.overflow["<silent: stop, input 210k>"] = isContextOverflow(msg("stop", undefined, { ...zero, input: 190000, cacheRead: 20000 }), 200000);
out.overflow["<silent: stop, input 100k>"] = isContextOverflow(msg("stop", undefined, { ...zero, input: 100000 }), 200000);
out.overflow["<length-stop: zero output, full window>"] = isContextOverflow(msg("length", undefined, { ...zero, input: 199000, cacheRead: 0 }), 200000);
out.overflow["<length-stop: with output>"] = isContextOverflow(msg("length", undefined, { ...zero, input: 199000, output: 50 }), 200000);
process.stdout.write(JSON.stringify(out, null, 2));
