package model

import (
	"fmt"
	"strings"
)

type ProviderKind string

const (
	ProviderOpenAICompatible ProviderKind = "openai-compatible"
	ProviderAnthropic        ProviderKind = "anthropic"
	ProviderGemini           ProviderKind = "gemini"
)

type ModelOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Provider    string `json:"provider"`
	Description string `json:"description,omitempty"`
}

func (o ModelOption) PromptLabel() string {
	if o.Description == "" {
		return fmt.Sprintf("%s (%s)", o.Label, o.ID)
	}
	return fmt.Sprintf("%s (%s) - %s", o.Label, o.ID, o.Description)
}

var modelCatalog = map[string]ModelOption{
	"gpt-5.4": {
		ID:          "gpt-5.4",
		Label:       "OpenAI GPT-5.4",
		Provider:    string(ProviderOpenAICompatible),
		Description: "latest OpenAI flagship for coding and agentic tasks",
	},
	"gpt-5.1": {
		ID:          "gpt-5.1",
		Label:       "OpenAI GPT-5.1",
		Provider:    string(ProviderOpenAICompatible),
		Description: "strong coding and reasoning model",
	},
	"gpt-5": {
		ID:          "gpt-5",
		Label:       "OpenAI GPT-5",
		Provider:    string(ProviderOpenAICompatible),
		Description: "previous OpenAI reasoning model",
	},
	"gpt-5-mini": {
		ID:          "gpt-5-mini",
		Label:       "OpenAI GPT-5 mini",
		Provider:    string(ProviderOpenAICompatible),
		Description: "faster, lower-cost OpenAI option",
	},
	"gpt-4.1": {
		ID:          "gpt-4.1",
		Label:       "OpenAI GPT-4.1",
		Provider:    string(ProviderOpenAICompatible),
		Description: "smart non-reasoning model",
	},
	"gpt-4.1-mini": {
		ID:          "gpt-4.1-mini",
		Label:       "OpenAI GPT-4.1 mini",
		Provider:    string(ProviderOpenAICompatible),
		Description: "balanced speed and cost",
	},
	"claude-opus-4-1-20250805": {
		ID:          "claude-opus-4-1-20250805",
		Label:       "Anthropic Claude Opus 4.1",
		Provider:    string(ProviderAnthropic),
		Description: "strong choice for complex coding and reasoning",
	},
	"claude-sonnet-4-20250514": {
		ID:          "claude-sonnet-4-20250514",
		Label:       "Anthropic Claude Sonnet 4",
		Provider:    string(ProviderAnthropic),
		Description: "high-performance balanced model",
	},
	"claude-3-7-sonnet-20250219": {
		ID:          "claude-3-7-sonnet-20250219",
		Label:       "Anthropic Claude Sonnet 3.7",
		Provider:    string(ProviderAnthropic),
		Description: "capable coding model with strong reasoning",
	},
	"gemini-2.5-pro": {
		ID:          "gemini-2.5-pro",
		Label:       "Google Gemini 2.5 Pro",
		Provider:    string(ProviderGemini),
		Description: "advanced model for large-context tasks",
	},
	"gemini-2.5-flash": {
		ID:          "gemini-2.5-flash",
		Label:       "Google Gemini 2.5 Flash",
		Provider:    string(ProviderGemini),
		Description: "fast price-performance option",
	},
	"gemini-2.5-flash-lite": {
		ID:          "gemini-2.5-flash-lite",
		Label:       "Google Gemini 2.5 Flash-Lite",
		Provider:    string(ProviderGemini),
		Description: "fastest and lowest-cost Gemini option",
	},
}

var providerModelOrder = map[ProviderKind][]string{
	ProviderOpenAICompatible: {
		"gpt-5.4",
		"gpt-5.1",
		"gpt-5",
		"gpt-5-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
	},
	ProviderAnthropic: {
		"claude-opus-4-1-20250805",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
	},
	ProviderGemini: {
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
	},
}

var providerRoleDefaults = map[ProviderKind]map[Role]string{
	ProviderOpenAICompatible: {
		RoleExecutor:  "gpt-5-mini",
		RoleJudge:     "gpt-5.4",
		RoleOptimizer: "gpt-5.4",
	},
	ProviderAnthropic: {
		RoleExecutor:  "claude-sonnet-4-20250514",
		RoleJudge:     "claude-opus-4-1-20250805",
		RoleOptimizer: "claude-opus-4-1-20250805",
	},
	ProviderGemini: {
		RoleExecutor:  "gemini-2.5-flash",
		RoleJudge:     "gemini-2.5-pro",
		RoleOptimizer: "gemini-2.5-pro",
	},
}

func RecommendedModelOptions(providerName string, role Role) []ModelOption {
	providerKey := normalizeProviderKind(providerName)
	order := providerModelOrder[providerKey]
	if len(order) == 0 {
		order = providerModelOrder[ProviderOpenAICompatible]
	}
	options := make([]ModelOption, 0, len(order))
	for _, id := range order {
		if option, ok := modelCatalog[id]; ok {
			options = append(options, option)
		}
	}
	return options
}

func SuggestedModelID(providerName string, role Role) string {
	providerKey := normalizeProviderKind(providerName)
	byRole := providerRoleDefaults[providerKey]
	if byRole == nil {
		byRole = providerRoleDefaults[ProviderOpenAICompatible]
	}
	if id := byRole[role]; id != "" {
		return id
	}
	order := providerModelOrder[providerKey]
	if len(order) == 0 {
		order = providerModelOrder[ProviderOpenAICompatible]
	}
	if len(order) > 0 {
		return order[0]
	}
	return ""
}

func ModelOptionByID(id string) (ModelOption, bool) {
	option, ok := modelCatalog[id]
	return option, ok
}

func normalizeProviderKind(providerName string) ProviderKind {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "", "openai", "openai-compatible", "openai_compatible":
		return ProviderOpenAICompatible
	case "anthropic", "claude":
		return ProviderAnthropic
	case "gemini", "google", "google-gemini":
		return ProviderGemini
	default:
		return ProviderKind(strings.ToLower(strings.TrimSpace(providerName)))
	}
}
