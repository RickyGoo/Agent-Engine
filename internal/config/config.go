package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"agent-engine/internal/model"
)

const (
	GlobalConfigFileName  = "config.json"
	ProjectConfigFileName = ".agent-engine.json"
)

func DefaultGlobalConfig() model.GlobalConfig {
	return model.GlobalConfig{
		Version:         1,
		DefaultProvider: "openai",
		Provider: model.ProviderSettings{
			Name:     "openai",
			Endpoint: "https://api.openai.com/v1/chat/completions",
			APIKeyRef: model.SecretRef{
				Kind: "env",
				Name: "OPENAI_API_KEY",
			},
		},
		RoleModels: model.RoleModels{
			Executor:  "gpt-4.1-mini",
			Judge:     "gpt-4.1",
			Optimizer: "gpt-4.1",
		},
		RunOutputDir: DefaultRunOutputDir(),
	}
}

func DefaultRunOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-engine", "runs")
	}
	return filepath.Join(home, ".local", "state", "agent-engine", "runs")
}

func GlobalConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-engine", GlobalConfigFileName), nil
}

func ProjectConfigPath(root string) string {
	return filepath.Join(root, ProjectConfigFileName)
}

func LoadGlobal(path string) (model.GlobalConfig, error) {
	cfg := DefaultGlobalConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return model.GlobalConfig{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return model.GlobalConfig{}, err
	}
	if cfg.RunOutputDir == "" {
		cfg.RunOutputDir = DefaultRunOutputDir()
	}
	if cfg.Provider.Endpoint == "" {
		cfg.Provider.Endpoint = "https://api.openai.com/v1/chat/completions"
	}
	if cfg.Provider.Name == "" {
		cfg.Provider.Name = cfg.DefaultProvider
	}
	return cfg, nil
}

func SaveGlobal(path string, cfg model.GlobalConfig) error {
	if path == "" {
		return errors.New("global config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func LoadProject(path string) (model.ProjectConfig, error) {
	cfg := model.ProjectConfig{Version: 1, Profiles: map[string]model.ProfileDefinition{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return model.ProjectConfig{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return model.ProjectConfig{}, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]model.ProfileDefinition{}
	}
	return cfg, nil
}

func SaveProject(path string, cfg model.ProjectConfig) error {
	if path == "" {
		return errors.New("project config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func Merge(global model.GlobalConfig, project model.ProjectConfig, root string) (model.EffectiveConfig, error) {
	effective := model.EffectiveConfig{
		Global:       global,
		Project:      project,
		RoleModels:   global.RoleModels,
		RunOutputDir: global.RunOutputDir,
		Provider:     global.Provider,
	}

	if project.RoleModels != nil {
		if project.RoleModels.Executor != "" {
			effective.RoleModels.Executor = project.RoleModels.Executor
		}
		if project.RoleModels.Judge != "" {
			effective.RoleModels.Judge = project.RoleModels.Judge
		}
		if project.RoleModels.Optimizer != "" {
			effective.RoleModels.Optimizer = project.RoleModels.Optimizer
		}
	}

	if project.Profile != "" {
		profile, ok := project.Profiles[project.Profile]
		if !ok {
			return model.EffectiveConfig{}, errors.New("project profile not found")
		}
		profile.Name = project.Profile
		effective.Profile = profile
	} else if len(project.Profiles) == 1 {
		for name, profile := range project.Profiles {
			profile.Name = name
			effective.Profile = profile
			break
		}
	}

	if effective.Profile.Name == "" {
		if builtIn, ok := builtinProfile(root); ok {
			effective.Profile = builtIn
		} else {
			return model.EffectiveConfig{}, errors.New("no usable profile found")
		}
	}

	return effective, nil
}

func ValidateEffective(effective model.EffectiveConfig) error {
	if strings.TrimSpace(effective.Provider.Endpoint) == "" {
		return errors.New("provider endpoint is required")
	}
	if effective.Provider.APIKeyRef.Kind == "" || effective.Provider.APIKeyRef.Name == "" {
		return errors.New("provider api key reference is required")
	}
	if strings.TrimSpace(effective.RoleModels.Executor) == "" {
		return errors.New("executor model is required")
	}
	if strings.TrimSpace(effective.RoleModels.Judge) == "" {
		return errors.New("judge model is required")
	}
	if strings.TrimSpace(effective.RoleModels.Optimizer) == "" {
		return errors.New("optimizer model is required")
	}
	if effective.Profile.Name == "" {
		return errors.New("profile is required")
	}
	if len(effective.Profile.Executor.Command) == 0 {
		return errors.New("profile executor command is required")
	}
	if len(effective.Profile.Verify.Command) == 0 {
		return errors.New("profile verify command is required")
	}
	return nil
}

func builtinProfile(root string) (model.ProfileDefinition, bool) {
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		return model.ProfileDefinition{
			Name:           "go-default",
			Description:    "Go project default profile",
			Executor:       model.CommandSpec{Command: []string{"go", "test", "./..."}},
			Verify:         model.CommandSpec{Command: []string{"go", "test", "./..."}},
			SensitivePaths: []string{".env", ".env.*", "secrets", "certs", "credentials"},
		}, true
	case fileExists(filepath.Join(root, "package.json")):
		return model.ProfileDefinition{
			Name:           "node-default",
			Description:    "Node project default profile",
			Executor:       model.CommandSpec{Command: []string{"npm", "test"}},
			Verify:         model.CommandSpec{Command: []string{"npm", "test"}},
			SensitivePaths: []string{".env", ".env.*", "secrets", "certs", "credentials"},
		}, true
	default:
		return model.ProfileDefinition{}, false
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
