package provider

import "strings"

type ProviderOption struct {
	ID               string
	Label            string
	Description      string
	DefaultEndpoint  string
	DefaultAPIKeyEnv string
}

var providerOptions = []ProviderOption{
	{
		ID:               "openai-compatible",
		Label:            "OpenAI-compatible",
		Description:      "chat/completions-style API",
		DefaultEndpoint:  "https://api.openai.com/v1/chat/completions",
		DefaultAPIKeyEnv: "OPENAI_API_KEY",
	},
	{
		ID:               "anthropic",
		Label:            "Anthropic Claude",
		Description:      "Messages API",
		DefaultEndpoint:  "https://api.anthropic.com/v1/messages",
		DefaultAPIKeyEnv: "ANTHROPIC_API_KEY",
	},
	{
		ID:               "gemini",
		Label:            "Google Gemini",
		Description:      "generateContent API",
		DefaultEndpoint:  "https://generativelanguage.googleapis.com/v1beta",
		DefaultAPIKeyEnv: "GOOGLE_API_KEY",
	},
}

func SupportedProviders() []ProviderOption {
	options := make([]ProviderOption, len(providerOptions))
	copy(options, providerOptions)
	return options
}

func ProviderOptionByName(name string) (ProviderOption, bool) {
	switch normalizeProviderName(name) {
	case "openai-compatible":
		return providerOptions[0], true
	case "anthropic":
		return providerOptions[1], true
	case "gemini":
		return providerOptions[2], true
	default:
		return ProviderOption{}, false
	}
}

func normalizeProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "openai", "openai-compatible", "openai_compatible":
		return "openai-compatible"
	case "anthropic", "claude":
		return "anthropic"
	case "gemini", "google", "google-gemini":
		return "gemini"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}
