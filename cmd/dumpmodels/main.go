// dumpmodels prints the catalog-tweak output (thinking level map and compat)
// for model IDs read as a JSON array on stdin, for parity comparison against
// the npm package's shipped OpenRouter catalog.
package main

import (
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/aunali321/pi-go/llm"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		panic(err)
	}
	sort.Strings(ids)

	out := map[string]any{}
	for _, id := range ids {
		m := &llm.Model{ID: id, Provider: "openrouter"}
		llm.ApplyOpenRouterCatalogTweaks(m)

		levels := map[string]any{}
		for level, v := range m.ThinkingLevelMap {
			levels[string(level)] = v
		}
		for level := range m.NullLevels {
			levels[string(level)] = nil
		}
		entry := map[string]any{"thinkingLevelMap": levels}
		compat := map[string]any{}
		if m.Compat != nil {
			detectedDevRole := strings.HasPrefix(id, "anthropic/") || strings.HasPrefix(id, "openai/")
			if m.Compat.SupportsDeveloperRole != nil && *m.Compat.SupportsDeveloperRole != detectedDevRole {
				compat["supportsDeveloperRole"] = *m.Compat.SupportsDeveloperRole
			}
			if m.Compat.RequiresReasoningContentOnAssistant != nil {
				compat["requiresReasoningContentOnAssistantMessages"] = *m.Compat.RequiresReasoningContentOnAssistant
			}
			if m.Compat.CacheControlFormat == llm.CacheControlAnthropic && !strings.HasPrefix(id, "anthropic/") {
				compat["cacheControlFormat"] = "anthropic"
			}
		}
		entry["compat"] = compat
		if m.MaxTokens != 0 {
			entry["maxTokens"] = m.MaxTokens
		}
		out[id] = entry
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.Stdout.Write(b)
}
