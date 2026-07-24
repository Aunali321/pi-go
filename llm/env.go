package llm

import "os"

var providerEnvVars = map[string]string{
	"ant-ling":              "ANT_LING_API_KEY",
	"qwen-token-plan":       "QWEN_TOKEN_PLAN_API_KEY",
	"qwen-token-plan-cn":    "QWEN_TOKEN_PLAN_CN_API_KEY",
	"openai":                "OPENAI_API_KEY",
	"nvidia":                "NVIDIA_API_KEY",
	"deepseek":              "DEEPSEEK_API_KEY",
	"groq":                  "GROQ_API_KEY",
	"cerebras":              "CEREBRAS_API_KEY",
	"xai":                   "XAI_API_KEY",
	"openrouter":            "OPENROUTER_API_KEY",
	"vercel-ai-gateway":     "AI_GATEWAY_API_KEY",
	"zai":                   "ZAI_API_KEY",
	"zai-coding-cn":         "ZAI_CODING_CN_API_KEY",
	"mistral":               "MISTRAL_API_KEY",
	"minimax":               "MINIMAX_API_KEY",
	"minimax-cn":            "MINIMAX_CN_API_KEY",
	"moonshotai":            "MOONSHOT_API_KEY",
	"moonshotai-cn":         "MOONSHOT_API_KEY",
	"huggingface":           "HF_TOKEN",
	"fireworks":             "FIREWORKS_API_KEY",
	"together":              "TOGETHER_API_KEY",
	"opencode":              "OPENCODE_API_KEY",
	"opencode-go":           "OPENCODE_API_KEY",
	"kimi-coding":           "KIMI_API_KEY",
	"github-copilot":        "COPILOT_GITHUB_TOKEN",
	"cloudflare-workers-ai": "CLOUDFLARE_API_KEY",
	"cloudflare-ai-gateway": "CLOUDFLARE_API_KEY",
	"xiaomi":                "XIAOMI_API_KEY",
	"xiaomi-token-plan-cn":  "XIAOMI_TOKEN_PLAN_CN_API_KEY",
	"xiaomi-token-plan-ams": "XIAOMI_TOKEN_PLAN_AMS_API_KEY",
	"xiaomi-token-plan-sgp": "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
}

// providerEnvValue resolves an env value from request-scoped overrides first,
// then the process environment.
func providerEnvValue(name string, env map[string]string) string {
	if v := env[name]; v != "" {
		return v
	}
	return os.Getenv(name)
}

// envAPIKey returns the API key for a provider from its known environment
// variable, honoring request-scoped overrides.
func envAPIKey(provider string, env map[string]string) string {
	name, ok := providerEnvVars[provider]
	if !ok {
		return ""
	}
	return providerEnvValue(name, env)
}
