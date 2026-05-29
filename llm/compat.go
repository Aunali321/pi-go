package llm

import "strings"

func detectCompat(m *Model) resolvedCompat {
	provider := m.provider()
	baseURL := m.baseURL()

	isZai := provider == "zai" || strings.Contains(baseURL, "api.z.ai")
	isTogether := provider == "together" || strings.Contains(baseURL, "api.together.ai") || strings.Contains(baseURL, "api.together.xyz")
	isMoonshot := provider == "moonshotai" || provider == "moonshotai-cn" || strings.Contains(baseURL, "api.moonshot.")
	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	isDeepSeek := provider == "deepseek" || strings.Contains(baseURL, "deepseek.com")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai")

	isNonStandard := provider == "cerebras" || strings.Contains(baseURL, "cerebras.ai") ||
		isGrok || isTogether || strings.Contains(baseURL, "chutes.ai") || isDeepSeek ||
		isZai || isMoonshot || provider == "opencode" || strings.Contains(baseURL, "opencode.ai")

	useMaxTokens := strings.Contains(baseURL, "chutes.ai") || isMoonshot || isTogether

	thinkingFormat := ThinkingFormatOpenAI
	switch {
	case isDeepSeek:
		thinkingFormat = ThinkingFormatDeepSeek
	case isZai:
		thinkingFormat = ThinkingFormatZAI
	case isTogether:
		thinkingFormat = ThinkingFormatTogether
	case isOpenRouter:
		thinkingFormat = ThinkingFormatOpenRouter
	}

	var cacheControlFormat CacheControlFormat
	if provider == "openrouter" && strings.HasPrefix(m.ID, "anthropic/") {
		cacheControlFormat = CacheControlAnthropic
	}

	maxTokensField := "max_completion_tokens"
	if useMaxTokens {
		maxTokensField = "max_tokens"
	}

	return resolvedCompat{
		supportsStore:                       !isNonStandard,
		supportsDeveloperRole:               !isNonStandard,
		supportsReasoningEffort:             !isGrok && !isZai && !isMoonshot && !isTogether,
		supportsUsageInStreaming:            true,
		maxTokensField:                      maxTokensField,
		requiresToolResultName:              false,
		requiresAssistantAfterToolResult:    false,
		requiresThinkingAsText:              false,
		requiresReasoningContentOnAssistant: isDeepSeek,
		thinkingFormat:                      thinkingFormat,
		supportsStrictMode:                  !isMoonshot && !isTogether,
		cacheControlFormat:                  cacheControlFormat,
		sendSessionAffinityHeaders:          false,
		supportsLongCacheRetention:          !isTogether,
	}
}

func getCompat(m *Model) resolvedCompat {
	d := detectCompat(m)
	c := m.Compat
	if c == nil {
		return d
	}

	pick := func(p *bool, def bool) bool {
		if p != nil {
			return *p
		}
		return def
	}

	d.supportsStore = pick(c.SupportsStore, d.supportsStore)
	d.supportsDeveloperRole = pick(c.SupportsDeveloperRole, d.supportsDeveloperRole)
	d.supportsReasoningEffort = pick(c.SupportsReasoningEffort, d.supportsReasoningEffort)
	d.supportsUsageInStreaming = pick(c.SupportsUsageInStreaming, d.supportsUsageInStreaming)
	if c.MaxTokensField != "" {
		d.maxTokensField = c.MaxTokensField
	}
	d.requiresToolResultName = pick(c.RequiresToolResultName, d.requiresToolResultName)
	d.requiresAssistantAfterToolResult = pick(c.RequiresAssistantAfterToolResult, d.requiresAssistantAfterToolResult)
	d.requiresThinkingAsText = pick(c.RequiresThinkingAsText, d.requiresThinkingAsText)
	d.requiresReasoningContentOnAssistant = pick(c.RequiresReasoningContentOnAssistant, d.requiresReasoningContentOnAssistant)
	if c.ThinkingFormat != "" {
		d.thinkingFormat = c.ThinkingFormat
	}
	d.openRouterRouting = c.OpenRouterRouting
	d.supportsStrictMode = pick(c.SupportsStrictMode, d.supportsStrictMode)
	if c.CacheControlFormat != "" {
		d.cacheControlFormat = c.CacheControlFormat
	}
	d.sendSessionAffinityHeaders = pick(c.SendSessionAffinityHeaders, d.sendSessionAffinityHeaders)
	d.supportsLongCacheRetention = pick(c.SupportsLongCacheRetention, d.supportsLongCacheRetention)
	return d
}
