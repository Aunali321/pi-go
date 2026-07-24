package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Model discovery: pi generates its OpenRouter catalog by fetching
// https://openrouter.ai/api/v1/models at build time (see pi's
// packages/ai/scripts/generate-models.ts) and shipping the mapped result.
// This file ports that mapping, including the hand-maintained thinking-level
// and metadata tweaks that apply to OpenRouter entries, so the Go port can
// discover models at runtime instead of embedding a snapshot.

type openRouterModelList struct {
	Data []openRouterModel `json:"data"`
}

type openRouterModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Architecture  struct {
		Modality string `json:"modality"`
	} `json:"architecture"`
	Pricing struct {
		Prompt          string `json:"prompt"`
		Completion      string `json:"completion"`
		InputCacheRead  string `json:"input_cache_read"`
		InputCacheWrite string `json:"input_cache_write"`
	} `json:"pricing"`
	TopProvider struct {
		ContextLength       int `json:"context_length"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	SupportedParameters []string `json:"supported_parameters"`
}

// FetchOpenRouterModels fetches the OpenRouter model list and returns the
// tool-capable models mapped the way pi's catalog generator maps them. A nil
// client uses http.DefaultClient.
func FetchOpenRouterModels(ctx context.Context, client *http.Client) ([]*Model, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter API returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var list openRouterModelList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return mapOpenRouterModels(list.Data), nil
}

// roundCost mirrors pi's roundCost: Number(value.toFixed(6)).
func roundCost(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}

func parseCost(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return roundCost(v * 1_000_000)
}

func mapOpenRouterModels(raw []openRouterModel) []*Model {
	var models []*Model
	for _, m := range raw {
		// Only include models that support tools.
		if !slices.Contains(m.SupportedParameters, "tools") {
			continue
		}

		input := []InputModality{InputText}
		if strings.Contains(m.Architecture.Modality, "image") {
			input = append(input, InputImage)
		}

		contextWindow := m.TopProvider.ContextLength
		if contextWindow == 0 {
			contextWindow = m.ContextLength
		}
		if contextWindow == 0 {
			contextWindow = 4096
		}
		maxTokens := m.TopProvider.MaxCompletionTokens
		if maxTokens == 0 {
			maxTokens = 4096
		}

		model := &Model{
			ID:        m.ID,
			Name:      m.Name,
			Provider:  "openrouter",
			BaseURL:   "https://openrouter.ai/api/v1",
			Reasoning: slices.Contains(m.SupportedParameters, "reasoning"),
			Input:     input,
			Cost: Pricing{
				Input:      parseCost(m.Pricing.Prompt),
				Output:     parseCost(m.Pricing.Completion),
				CacheRead:  parseCost(m.Pricing.InputCacheRead),
				CacheWrite: parseCost(m.Pricing.InputCacheWrite),
			},
			ContextWindow: contextWindow,
			MaxTokens:     maxTokens,
		}
		ApplyOpenRouterCatalogTweaks(model)
		models = append(models, model)
	}
	return models
}

// mergeThinkingLevels merges entries into the model's thinking-level map. An
// empty value marks the level unsupported (pi's null), removing any mapping.
func mergeThinkingLevels(m *Model, levels map[ThinkingLevel]string, nulls ...ThinkingLevel) {
	if m.ThinkingLevelMap == nil {
		m.ThinkingLevelMap = map[ThinkingLevel]string{}
	}
	if m.NullLevels == nil {
		m.NullLevels = map[ThinkingLevel]bool{}
	}
	for level, v := range levels {
		m.ThinkingLevelMap[level] = v
		delete(m.NullLevels, level)
	}
	for _, level := range nulls {
		delete(m.ThinkingLevelMap, level)
		m.NullLevels[level] = true
	}
}

func ensureCompat(m *Model) *Compat {
	if m.Compat == nil {
		m.Compat = &Compat{}
	}
	return m.Compat
}

// ApplyOpenRouterCatalogTweaks ports the hand-maintained per-model
// adjustments pi's generator applies that affect OpenRouter catalog entries.
// Synced with pi v0.82.0; revisit on upstream bumps.
func ApplyOpenRouterCatalogTweaks(m *Model) {
	id := m.ID

	// OpenAI reasoning tiers.
	if strings.Contains(id, "gpt-5.2") || strings.Contains(id, "gpt-5.3") ||
		strings.Contains(id, "gpt-5.4") || strings.Contains(id, "gpt-5.5") || strings.Contains(id, "gpt-5.6") {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingXHigh: "xhigh"})
	}
	if strings.Contains(id, "gpt-5.6") {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingMax: "max"})
	}
	if strings.HasSuffix(id, "gpt-5.5-pro") {
		mergeThinkingLevels(m, nil, ThinkingOff, ThinkingMinimal, ThinkingLow)
	}

	// Anthropic adaptive-thinking effort support.
	if strings.Contains(id, "opus-4-6") || strings.Contains(id, "opus-4.6") ||
		strings.Contains(id, "sonnet-4-6") || strings.Contains(id, "sonnet-4.6") {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingMax: "max"})
	}
	if strings.Contains(id, "opus-4-7") || strings.Contains(id, "opus-4.7") ||
		strings.Contains(id, "opus-4-8") || strings.Contains(id, "opus-4.8") ||
		strings.Contains(id, "sonnet-5") || strings.Contains(id, "sonnet.5") {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingXHigh: "xhigh", ThinkingMax: "max"})
	}
	if strings.Contains(id, "fable-5") {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingXHigh: "xhigh", ThinkingMax: "max"}, ThinkingOff)
	}

	// DeepSeek V4 on OpenRouter.
	if strings.Contains(id, "deepseek-v4") {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingHigh: "high", ThinkingXHigh: "xhigh"},
			ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingMax)
	}

	// Mercury 2 in instant mode (reasoning_effort "none") disables tool
	// calling; mark "off" unsupported so the reasoning param is omitted.
	if strings.HasPrefix(id, "inception/mercury-2") {
		mergeThinkingLevels(m, nil, ThinkingOff)
	}
	if id == "z-ai/glm-5.2" {
		mergeThinkingLevels(m, map[ThinkingLevel]string{ThinkingXHigh: "xhigh"})
	}

	// Anthropic-style cache control also applies to OpenRouter's "~" alias
	// ids, which runtime URL/prefix detection does not cover.
	if strings.HasPrefix(id, "anthropic/") || strings.HasPrefix(id, "~anthropic/") {
		ensureCompat(m).CacheControlFormat = CacheControlAnthropic
	}

	// DeepSeek V4 replays require an explicit (empty) reasoning_content field
	// on assistant messages; OpenRouter preserves DeepSeek's native
	// reasoning-effort handling otherwise.
	if strings.Contains(id, "deepseek-v4") {
		t := true
		ensureCompat(m).RequiresReasoningContentOnAssistant = &t
	}

	// Metadata overrides until upstream model metadata is corrected.
	if id == "moonshotai/kimi-k3" || id == "~moonshotai/kimi-latest" {
		m.MaxTokens = 131072
	}
	if id == "moonshotai/kimi-k2.5" {
		m.Cost.Input = 0.41
		m.Cost.Output = 2.06
		m.Cost.CacheRead = 0.07
		m.MaxTokens = 4096
	}
	if strings.HasPrefix(id, "moonshotai/kimi-k2.6") {
		f := false
		t := true
		compat := ensureCompat(m)
		compat.SupportsDeveloperRole = &f
		compat.RequiresReasoningContentOnAssistant = &t
	}
	if id == "z-ai/glm-5" {
		m.Cost.Input = 0.6
		m.Cost.Output = 1.9
		m.Cost.CacheRead = 0.119
	}
}
