package llm

import "strings"

// detectCompat auto-detects compatibility settings from provider name and
// baseUrl. Used as the base when Model.Compat is not set; explicit Compat
// entries override these detected values.
func detectCompat(m *Model) resolvedCompat {
	provider := m.provider()
	baseURL := m.baseURL()

	isZai := provider == "zai" || provider == "zai-coding-cn" ||
		strings.Contains(baseURL, "api.z.ai") || strings.Contains(baseURL, "open.bigmodel.cn")
	isTogether := provider == "together" || strings.Contains(baseURL, "api.together.ai") || strings.Contains(baseURL, "api.together.xyz")
	isMoonshot := provider == "moonshotai" || provider == "moonshotai-cn" || strings.Contains(baseURL, "api.moonshot.")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai")
	isCloudflareWorkersAI := provider == "cloudflare-workers-ai" || strings.Contains(baseURL, "api.cloudflare.com")
	isCloudflareAiGateway := provider == "cloudflare-ai-gateway" || strings.Contains(baseURL, "gateway.ai.cloudflare.com")
	isNvidia := provider == "nvidia" || strings.Contains(baseURL, "integrate.api.nvidia.com")
	isAntLing := provider == "ant-ling" || strings.Contains(baseURL, "api.ant-ling.com")
	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	isDeepSeek := provider == "deepseek" || strings.Contains(baseURL, "deepseek.com")

	isNonStandard := isNvidia ||
		provider == "cerebras" || strings.Contains(baseURL, "cerebras.ai") ||
		isGrok || isTogether || strings.Contains(baseURL, "chutes.ai") || isDeepSeek ||
		isZai || isMoonshot || provider == "opencode" || strings.Contains(baseURL, "opencode.ai") ||
		isCloudflareWorkersAI || isCloudflareAiGateway || isAntLing

	useMaxTokens := strings.Contains(baseURL, "chutes.ai") || isMoonshot || isCloudflareAiGateway ||
		isTogether || isNvidia || isAntLing

	isOpenRouterDeveloperRoleModel := isOpenRouter &&
		(strings.HasPrefix(m.ID, "anthropic/") || strings.HasPrefix(m.ID, "openai/"))

	thinkingFormat := ThinkingFormatOpenAI
	switch {
	case isDeepSeek:
		thinkingFormat = ThinkingFormatDeepSeek
	case isZai:
		thinkingFormat = ThinkingFormatZAI
	case isTogether:
		thinkingFormat = ThinkingFormatTogether
	case isAntLing:
		thinkingFormat = ThinkingFormatAntLing
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

	sessionAffinityFormat := SessionAffinityOpenAI
	if isOpenRouter {
		sessionAffinityFormat = SessionAffinityOpenRouter
	}

	return resolvedCompat{
		supportsStore:                       !isNonStandard,
		supportsDeveloperRole:               isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter),
		supportsReasoningEffort:             !isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAiGateway && !isNvidia && !isAntLing,
		supportsUsageInStreaming:            true,
		maxTokensField:                      maxTokensField,
		requiresToolResultName:              false,
		requiresAssistantAfterToolResult:    false,
		requiresThinkingAsText:              false,
		requiresReasoningContentOnAssistant: isDeepSeek,
		thinkingFormat:                      thinkingFormat,
		chatTemplateKwargs:                  nil,
		openRouterRouting:                   nil,
		vercelGatewayRouting:                nil,
		zaiToolStream:                       false,
		supportsStrictMode:                  !isMoonshot && !isTogether && !isCloudflareAiGateway && !isNvidia,
		supportsOpenAIGrammarTools:          false,
		cacheControlFormat:                  cacheControlFormat,
		sendSessionAffinityHeaders:          false,
		deferredToolsMode:                   "",
		sessionAffinityFormat:               sessionAffinityFormat,
		supportsLongCacheRetention:          !(isTogether || isCloudflareWorkersAI || isCloudflareAiGateway || isNvidia || isAntLing),
	}
}

// getCompat resolves compatibility settings for a model: auto-detects from
// provider/URL then overrides with the explicit Model.Compat entries.
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
	if c.ChatTemplateKwargs != nil {
		d.chatTemplateKwargs = c.ChatTemplateKwargs
	}
	d.openRouterRouting = c.OpenRouterRouting
	if c.VercelGatewayRouting != nil {
		d.vercelGatewayRouting = c.VercelGatewayRouting
	}
	d.zaiToolStream = pick(c.ZaiToolStream, d.zaiToolStream)
	d.supportsStrictMode = pick(c.SupportsStrictMode, d.supportsStrictMode)
	d.supportsOpenAIGrammarTools = pick(c.SupportsOpenAIGrammarTools, d.supportsOpenAIGrammarTools)
	if c.CacheControlFormat != "" {
		d.cacheControlFormat = c.CacheControlFormat
	}
	d.sendSessionAffinityHeaders = pick(c.SendSessionAffinityHeaders, d.sendSessionAffinityHeaders)
	if c.DeferredToolsMode != "" {
		d.deferredToolsMode = c.DeferredToolsMode
	}
	if c.SessionAffinityFormat != "" {
		d.sessionAffinityFormat = c.SessionAffinityFormat
	}
	d.supportsLongCacheRetention = pick(c.SupportsLongCacheRetention, d.supportsLongCacheRetention)
	return d
}
