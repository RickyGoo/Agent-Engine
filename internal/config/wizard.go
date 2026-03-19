package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent-engine/internal/model"
	"agent-engine/internal/project"
	"agent-engine/internal/secret"
	"agent-engine/internal/ui"
)

type WizardResult struct {
	Global  model.GlobalConfig
	Project model.ProjectConfig
}

func RunWizard(ctx context.Context, root string, console *ui.Console, secretStore secret.Store) (WizardResult, error) {
	global := DefaultGlobalConfig()
	projectCfg := model.ProjectConfig{
		Version:  1,
		Profiles: map[string]model.ProfileDefinition{},
	}

	console.Println("Agent Engine initial setup")

	providerName, err := console.AskDefault("LLM provider name", global.DefaultProvider)
	if err != nil {
		return WizardResult{}, err
	}
	if providerName != "" {
		global.DefaultProvider = providerName
		global.Provider.Name = providerName
	}

	endpoint, err := console.AskDefault("Provider API endpoint (for example https://api.openai.com/v1/chat/completions)", global.Provider.Endpoint)
	if err != nil {
		return WizardResult{}, err
	}
	if endpoint != "" {
		global.Provider.Endpoint = endpoint
	}

	useBuiltIn, err := console.Confirm("Use the built-in profile template detected for this project?", true)
	if err != nil {
		return WizardResult{}, err
	}

	profile := model.ProfileDefinition{}
	if useBuiltIn {
		if builtIn, ok := project.DetectBuiltInProfile(root); ok {
			profile = builtIn
		} else {
			console.Println("No built-in template was detected; enter the commands manually.")
		}
	}

	if profile.Name == "" {
		name, err := console.AskDefault("Profile name", "default")
		if err != nil {
			return WizardResult{}, err
		}
		executor, err := askCommand(console, "Execution command", []string{"go", "test", "./..."})
		if err != nil {
			return WizardResult{}, err
		}
		verify, err := askCommand(console, "Verification command", executor)
		if err != nil {
			return WizardResult{}, err
		}
		profile = model.ProfileDefinition{
			Name:     name,
			Executor: model.CommandSpec{Command: executor},
			Verify:   model.CommandSpec{Command: verify},
		}
	}

	profile.SensitivePaths = []string{".env", ".env.*", "secrets", "certs", "credentials"}
	projectCfg.Profile = profile.Name
	projectCfg.Profiles[profile.Name] = profile

	executorModel, err := console.AskDefault("Executor model", global.RoleModels.Executor)
	if err != nil {
		return WizardResult{}, err
	}
	if executorModel != "" {
		global.RoleModels.Executor = executorModel
	}
	judgeModel, err := console.AskDefault("Judge model", global.RoleModels.Judge)
	if err != nil {
		return WizardResult{}, err
	}
	if judgeModel != "" {
		global.RoleModels.Judge = judgeModel
	}
	optimizerModel, err := console.AskDefault("Optimizer model", global.RoleModels.Optimizer)
	if err != nil {
		return WizardResult{}, err
	}
	if optimizerModel != "" {
		global.RoleModels.Optimizer = optimizerModel
	}

	useGoalTemplate, err := console.Confirm("Configure a default optimization goal template?", false)
	if err != nil {
		return WizardResult{}, err
	}
	if useGoalTemplate {
		template, err := askGoalTemplate(console)
		if err != nil {
			return WizardResult{}, err
		}
		projectCfg.GoalTemplate = &template
	}

	ref, err := configureSecret(ctx, console, secretStore, global.Provider.Name)
	if err != nil {
		return WizardResult{}, err
	}
	global.Provider.APIKeyRef = ref

	if err := validateWizard(ctx, global, console, secretStore); err != nil {
		return WizardResult{}, err
	}

	return WizardResult{Global: global, Project: projectCfg}, nil
}

func askCommand(console *ui.Console, prompt string, defaultValue []string) ([]string, error) {
	raw, err := console.AskDefault(prompt+" (space-separated)", strings.Join(defaultValue, " "))
	if err != nil {
		return nil, err
	}
	parts := splitCommand(raw)
	if len(parts) == 0 {
		return nil, errors.New("command cannot be empty")
	}
	return parts, nil
}

func splitCommand(raw string) []string {
	fields := strings.Fields(strings.TrimSpace(raw))
	return fields
}

func askGoalTemplate(console *ui.Console) (model.GoalInput, error) {
	direction, err := console.Ask("Template goal direction (for example: simplify logic, improve performance): ")
	if err != nil {
		return model.GoalInput{}, err
	}
	constraints, err := console.AskDefault("Template constraints (optional)", "")
	if err != nil {
		return model.GoalInput{}, err
	}
	success, err := console.AskDefault("Template success criteria (optional)", "")
	if err != nil {
		return model.GoalInput{}, err
	}
	risk, err := console.AskDefault("Template risk preference (conservative/balanced/aggressive, optional)", "conservative")
	if err != nil {
		return model.GoalInput{}, err
	}
	notes, err := console.AskDefault("Template notes (optional)", "")
	if err != nil {
		return model.GoalInput{}, err
	}
	return model.GoalInput{
		Direction:       direction,
		Constraints:     constraints,
		SuccessCriteria: success,
		RiskPreference:  risk,
		Notes:           notes,
	}, nil
}

func configureSecret(ctx context.Context, console *ui.Console, secretStore secret.Store, providerName string) (model.SecretRef, error) {
	mode, err := console.AskDefault("API key storage method (keychain/env)", "env")
	if err != nil {
		return model.SecretRef{}, err
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	key, err := console.Ask("Enter the API key (used only for immediate verification): ")
	if err != nil {
		return model.SecretRef{}, err
	}
	if key == "" {
		return model.SecretRef{}, errors.New("api key cannot be empty")
	}

	if mode == "keychain" {
		ref := model.SecretRef{Kind: "keychain", Name: "agent-engine/" + providerName, Account: "default"}
		if err := secretStore.Save(ctx, secret.Ref(ref), key); err != nil {
			return model.SecretRef{}, err
		}
		return ref, nil
	}

	envName, err := console.AskDefault("Environment variable name for storing the API key", strings.ToUpper(providerName)+"_API_KEY")
	if err != nil {
		return model.SecretRef{}, err
	}
	if envName == "" {
		return model.SecretRef{}, errors.New("environment variable name cannot be empty")
	}
	if err := secretStore.Save(ctx, secret.Ref{Kind: "env", Name: envName}, key); err != nil {
		return model.SecretRef{}, err
	}
	return model.SecretRef{Kind: "env", Name: envName}, nil
}

func validateWizard(ctx context.Context, global model.GlobalConfig, console *ui.Console, secretStore secret.Store) error {
	_, err := secretStore.Resolve(ctx, secret.Ref(global.Provider.APIKeyRef))
	if err != nil {
		return fmt.Errorf("API key verification failed: %w", err)
	}
	console.Println("Initial setup complete.")
	return nil
}

func ProjectConfigFromWizard(result WizardResult) model.ProjectConfig {
	return result.Project
}
