import { getBuiltinModels } from "@earendil-works/pi-ai/providers/all";

// Baked compat fields that merely restate what runtime URL/prefix detection
// yields are dropped so the diff surfaces only behavior-changing metadata.
function normalizeCompat(id, compat) {
  const c = { ...(compat ?? {}) };
  if (c.thinkingFormat === "openrouter") delete c.thinkingFormat;
  const detectedDevRole = id.startsWith("anthropic/") || id.startsWith("openai/");
  if (c.supportsDeveloperRole === detectedDevRole) delete c.supportsDeveloperRole;
  if (c.cacheControlFormat === "anthropic" && id.startsWith("anthropic/")) delete c.cacheControlFormat;
  return c;
}

const models = getBuiltinModels("openrouter");
const out = {};
for (const m of [...models].sort((a, b) => a.id.localeCompare(b.id))) {
  const entry = { thinkingLevelMap: { ...(m.thinkingLevelMap ?? {}) }, compat: normalizeCompat(m.id, m.compat) };
  if (["moonshotai/kimi-k3", "~moonshotai/kimi-latest", "moonshotai/kimi-k2.5"].includes(m.id)) entry.maxTokens = m.maxTokens;
  out[m.id] = entry;
}
process.stdout.write(JSON.stringify(out, null, 2));
