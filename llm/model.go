package llm

type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
	ThinkingMax     ThinkingLevel = "max"
)

type CacheRetention string

const (
	CacheNone  CacheRetention = "none"
	CacheShort CacheRetention = "short"
	CacheLong  CacheRetention = "long"
)

type Pricing struct {
	Input      float64 // $/million tokens
	Output     float64
	CacheRead  float64
	CacheWrite float64
	// Tiers holds request-wide pricing tiers. The highest matching input
	// threshold applies to the full request.
	Tiers []PricingTier
}

type PricingTier struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	// InputTokensAbove selects this tier for requests whose total input usage
	// (input + cacheRead + cacheWrite) exceeds this token count.
	InputTokensAbove int
}

type Model struct {
	ID       string
	Name     string
	Provider string
	BaseURL  string

	Reasoning        bool
	ThinkingLevelMap map[ThinkingLevel]string
	NullLevels       map[ThinkingLevel]bool

	Input []InputModality

	Cost          Pricing
	ContextWindow int
	MaxTokens     int

	Headers map[string]string

	Compat *Compat
}

type InputModality string

const (
	InputText  InputModality = "text"
	InputImage InputModality = "image"
)

func (m *Model) provider() string {
	if m.Provider != "" {
		return m.Provider
	}
	return "openrouter"
}

func (m *Model) ResolvedProvider() string { return m.provider() }

func (m *Model) baseURL() string {
	if m.BaseURL != "" {
		return m.BaseURL
	}
	return "https://openrouter.ai/api/v1"
}

func (m *Model) supportsImageInput() bool {
	for _, in := range m.Input {
		if in == InputImage {
			return true
		}
	}
	return false
}

type OpenRouterRouting struct {
	AllowFallbacks    *bool    `json:"allow_fallbacks,omitempty"`
	RequireParameters *bool    `json:"require_parameters,omitempty"`
	DataCollection    string   `json:"data_collection,omitempty"`
	Order             []string `json:"order,omitempty"`
	Only              []string `json:"only,omitempty"`
	Ignore            []string `json:"ignore,omitempty"`
	Quantizations     []string `json:"quantizations,omitempty"`
	Sort              string   `json:"sort,omitempty"`
}

type VercelGatewayRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

type ThinkingFormat string

const (
	ThinkingFormatOpenAI           ThinkingFormat = "openai"
	ThinkingFormatOpenRouter       ThinkingFormat = "openrouter"
	ThinkingFormatDeepSeek         ThinkingFormat = "deepseek"
	ThinkingFormatTogether         ThinkingFormat = "together"
	ThinkingFormatZAI              ThinkingFormat = "zai"
	ThinkingFormatQwen             ThinkingFormat = "qwen"
	ThinkingFormatQwenChatTemplate ThinkingFormat = "qwen-chat-template"
	ThinkingFormatChatTemplate     ThinkingFormat = "chat-template"
	ThinkingFormatStringThinking   ThinkingFormat = "string-thinking"
	ThinkingFormatAntLing          ThinkingFormat = "ant-ling"
)

// SessionAffinityFormat selects which session-affinity headers are sent from
// StreamOptions.SessionID: "openai" sends session_id, x-client-request-id and
// x-session-affinity; "openai-nosession" drops session_id; "openrouter" sends
// x-session-id.
type SessionAffinityFormat string

const (
	SessionAffinityOpenAI          SessionAffinityFormat = "openai"
	SessionAffinityOpenAINoSession SessionAffinityFormat = "openai-nosession"
	SessionAffinityOpenRouter      SessionAffinityFormat = "openrouter"
)

// DeferredToolsKimi is the only provider-specific deferred tool serialization
// mode: tools named by ToolResultMessage.AddedToolNames are dropped from the
// request tools param and replayed as Kimi system messages carrying tool
// definitions at their load point.
const DeferredToolsKimi = "kimi"

// ChatTemplateKwarg is one chat_template_kwargs value for the "chat-template"
// thinking format: either a literal (string, number, bool or nil) or a
// reference to a pi-controlled thinking variable.
type ChatTemplateKwarg struct {
	Value any
	// Var is "thinking.enabled" or "thinking.effort"; when set, Value is ignored.
	Var         string
	OmitWhenOff bool
}

type CacheControlFormat string

const CacheControlAnthropic CacheControlFormat = "anthropic"

// Compat overrides auto-detected OpenAI-compatibility settings. Nil pointer
// fields fall back to detection from Provider and BaseURL.
type Compat struct {
	SupportsStore                       *bool
	SupportsDeveloperRole               *bool
	SupportsReasoningEffort             *bool
	SupportsUsageInStreaming            *bool
	MaxTokensField                      string
	RequiresToolResultName              *bool
	RequiresAssistantAfterToolResult    *bool
	RequiresThinkingAsText              *bool
	RequiresReasoningContentOnAssistant *bool
	ThinkingFormat                      ThinkingFormat
	ChatTemplateKwargs                  map[string]ChatTemplateKwarg
	OpenRouterRouting                   *OpenRouterRouting
	VercelGatewayRouting                *VercelGatewayRouting
	ZaiToolStream                       *bool
	SupportsOpenAIGrammarTools          *bool
	SupportsStrictMode                  *bool
	CacheControlFormat                  CacheControlFormat
	SendSessionAffinityHeaders          *bool
	DeferredToolsMode                   string
	SessionAffinityFormat               SessionAffinityFormat
	SupportsLongCacheRetention          *bool
}

type resolvedCompat struct {
	supportsStore                       bool
	supportsDeveloperRole               bool
	supportsReasoningEffort             bool
	supportsUsageInStreaming            bool
	maxTokensField                      string
	requiresToolResultName              bool
	requiresAssistantAfterToolResult    bool
	requiresThinkingAsText              bool
	requiresReasoningContentOnAssistant bool
	thinkingFormat                      ThinkingFormat
	chatTemplateKwargs                  map[string]ChatTemplateKwarg
	openRouterRouting                   *OpenRouterRouting
	vercelGatewayRouting                *VercelGatewayRouting
	zaiToolStream                       bool
	supportsOpenAIGrammarTools          bool
	supportsStrictMode                  bool
	cacheControlFormat                  CacheControlFormat
	sendSessionAffinityHeaders          bool
	deferredToolsMode                   string
	sessionAffinityFormat               SessionAffinityFormat
	supportsLongCacheRetention          bool
}
