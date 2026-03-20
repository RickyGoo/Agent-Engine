package provider

import (
	"fmt"

	"agent-engine/internal/model"
	"agent-engine/internal/secret"
)

func NewClient(settings model.ProviderSettings, store secret.Store) (Client, error) {
	switch normalizeProviderName(settings.Name) {
	case "openai-compatible":
		return NewOpenAICompatibleClient(settings.Endpoint, settings.APIKeyRef, store), nil
	case "anthropic":
		return NewAnthropicClient(settings.Endpoint, settings.APIKeyRef, store), nil
	case "gemini":
		return NewGeminiClient(settings.Endpoint, settings.APIKeyRef, store), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", settings.Name)
	}
}
