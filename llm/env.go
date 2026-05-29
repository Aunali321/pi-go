package llm

import "os"

var providerEnvVars = map[string]string{
	"openrouter": "OPENROUTER_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"xai":        "XAI_API_KEY",
	"groq":       "GROQ_API_KEY",
	"together":   "TOGETHER_API_KEY",
	"zai":        "ZAI_API_KEY",
	"mistral":    "MISTRAL_API_KEY",
}

func envAPIKey(provider string) string {
	if name, ok := providerEnvVars[provider]; ok {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return os.Getenv("OPENAI_API_KEY")
}
