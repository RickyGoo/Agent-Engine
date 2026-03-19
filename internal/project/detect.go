package project

import (
	"errors"
	"os"
	"path/filepath"

	"agent-engine/internal/model"
)

func DetectBuiltInProfile(root string) (model.ProfileDefinition, bool) {
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

func ResolveProfile(root string, project model.ProjectConfig, name string) (model.ProfileDefinition, error) {
	if name != "" {
		if profile, ok := project.Profiles[name]; ok {
			profile.Name = name
			return profile, nil
		}
		return model.ProfileDefinition{}, errors.New("requested profile not found in project config")
	}
	if project.Profile != "" {
		if profile, ok := project.Profiles[project.Profile]; ok {
			profile.Name = project.Profile
			return profile, nil
		}
	}
	if profile, ok := DetectBuiltInProfile(root); ok {
		return profile, nil
	}
	if len(project.Profiles) == 1 {
		for name, profile := range project.Profiles {
			profile.Name = name
			return profile, nil
		}
	}
	return model.ProfileDefinition{}, errors.New("no usable profile found")
}
