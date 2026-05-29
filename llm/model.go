package llm

type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
)

type CacheRetention string

const (
	CacheNone  CacheRetention = "none"
	CacheShort CacheRetention = "short"
	CacheLong  CacheRetention = "long"
)

type Pricing struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
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

type ThinkingFormat string

const (
	ThinkingFormatOpenAI     ThinkingFormat = "openai"
	ThinkingFormatOpenRouter ThinkingFormat = "openrouter"
	ThinkingFormatDeepSeek   ThinkingFormat = "deepseek"
	ThinkingFormatTogether   ThinkingFormat = "together"
	ThinkingFormatZAI        ThinkingFormat = "zai"
	ThinkingFormatQwen       ThinkingFormat = "qwen"
)

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
	OpenRouterRouting                   *OpenRouterRouting
	SupportsStrictMode                  *bool
	CacheControlFormat                  CacheControlFormat
	SendSessionAffinityHeaders          *bool
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
	openRouterRouting                   *OpenRouterRouting
	supportsStrictMode                  bool
	cacheControlFormat                  CacheControlFormat
	sendSessionAffinityHeaders          bool
	supportsLongCacheRetention          bool
}
