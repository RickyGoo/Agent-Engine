package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"agent-engine/internal/config"
	"agent-engine/internal/model"
	"agent-engine/internal/provider"
	"agent-engine/internal/secret"
	"agent-engine/internal/ui"
	"agent-engine/internal/workflow"
)

func Run(args []string, in io.Reader, out, errOut io.Writer) error {
	console := ui.NewConsole(in, out, errOut)
	if len(args) == 0 {
		printUsage(out)
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], console)
	case "run":
		return runWorkflow(args[1:], console, false)
	case "validate":
		return runValidate(args[1:], console)
	case "config":
		return runShowConfig(args[1:], console)
	case "help", "-h", "--help":
		printUsage(out)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runInit(args []string, console *ui.Console) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "project root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}

	globalPath, err := config.GlobalConfigPath()
	if err != nil {
		return err
	}
	store := secret.CompositeStore{}
	wizardResult, err := config.RunWizard(context.Background(), absRoot, console, store)
	if err != nil {
		return err
	}
	probeClient, err := provider.NewClient(wizardResult.Global.Provider, store)
	if err != nil {
		return err
	}
	if err := validateProviderConnectivity(context.Background(), probeClient, wizardResult.Global.RoleModels); err != nil {
		return err
	}

	projectPath := config.ProjectConfigPath(absRoot)
	if err := config.SaveGlobal(globalPath, wizardResult.Global); err != nil {
		return err
	}
	if err := config.SaveProject(projectPath, wizardResult.Project); err != nil {
		return err
	}
	console.Println("Configuration saved.")
	console.Println("Global config:", globalPath)
	console.Println("Project config:", projectPath)
	return nil
}

func runWorkflow(args []string, console *ui.Console, showConfigOnly bool) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "project root")
	profile := fs.String("profile", "", "profile name")
	goalText := fs.String("goal", "", "goal direction")
	constraints := fs.String("constraints", "", "goal constraints")
	success := fs.String("success", "", "goal success criteria")
	risk := fs.String("risk", "", "goal risk preference")
	notes := fs.String("notes", "", "goal notes")
	dryRun := fs.Bool("dry-run", false, "preview only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}

	effective, secrets, err := loadEffectiveConfig(absRoot, *profile)
	if err != nil {
		return err
	}
	if err := config.ValidateEffective(effective); err != nil {
		return err
	}
	providerClient, err := provider.NewClient(effective.Provider, secrets)
	if err != nil {
		return err
	}
	runner := workflow.NewRunner(effective, console, providerClient, secrets, absRoot)
	mode, err := chooseRunMode(console)
	if err != nil {
		return err
	}
	console.Println("Selected mode:", modeLabel(mode))

	var goal *model.GoalInput
	if strings.TrimSpace(*goalText) != "" || strings.TrimSpace(*constraints) != "" || strings.TrimSpace(*success) != "" || strings.TrimSpace(*risk) != "" || strings.TrimSpace(*notes) != "" {
		goal = &model.GoalInput{
			Direction:       strings.TrimSpace(*goalText),
			Constraints:     strings.TrimSpace(*constraints),
			SuccessCriteria: strings.TrimSpace(*success),
			RiskPreference:  strings.TrimSpace(*risk),
			Notes:           strings.TrimSpace(*notes),
		}
	}

	if showConfigOnly {
		return printEffectiveConfig(console, effective)
	}

	summary, err := runner.Run(context.Background(), workflow.Options{
		ProjectRoot: absRoot,
		ProfileName: *profile,
		Goal:        goal,
		DryRun:      *dryRun,
		Mode:        mode,
	})
	if err != nil {
		return err
	}
	if mode == workflow.RunModeScan {
		console.Println("Scan complete:", summary.RunDir)
	} else {
		console.Println("Run complete:", summary.RunDir)
	}
	console.Println("Accepted:", summary.Accepted)
	return nil
}

func chooseRunMode(console *ui.Console) (workflow.RunMode, error) {
	for {
		value, err := console.AskDefault("Choose mode (scan = preview only, run = full writeback)", string(workflow.RunModeRun))
		if err != nil {
			return "", err
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", string(workflow.RunModeRun), "r":
			return workflow.RunModeRun, nil
		case string(workflow.RunModeScan), "s":
			return workflow.RunModeScan, nil
		default:
			console.Println("Please enter scan or run.")
		}
	}
}

func modeLabel(mode workflow.RunMode) string {
	switch mode {
	case workflow.RunModeScan:
		return "Scan mode"
	default:
		return "Run mode"
	}
}

func runValidate(args []string, console *ui.Console) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "project root")
	profile := fs.String("profile", "", "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	effective, secrets, err := loadEffectiveConfig(absRoot, *profile)
	if err != nil {
		return err
	}
	if err := config.ValidateEffective(effective); err != nil {
		return err
	}
	client, err := provider.NewClient(effective.Provider, secrets)
	if err != nil {
		return err
	}
	if err := validateProviderConnectivity(context.Background(), client, effective.RoleModels); err != nil {
		return err
	}
	console.Println("Configuration validation passed.")
	return nil
}

func runShowConfig(args []string, console *ui.Console) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "project root")
	profile := fs.String("profile", "", "profile name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	effective, _, err := loadEffectiveConfig(absRoot, *profile)
	if err != nil {
		return err
	}
	return printEffectiveConfig(console, effective)
}

func loadEffectiveConfig(root, profileName string) (model.EffectiveConfig, secret.Store, error) {
	globalPath, err := config.GlobalConfigPath()
	if err != nil {
		return model.EffectiveConfig{}, nil, err
	}
	globalCfg, err := config.LoadGlobal(globalPath)
	if err != nil {
		return model.EffectiveConfig{}, nil, err
	}
	projectCfg, err := config.LoadProject(config.ProjectConfigPath(root))
	if err != nil {
		return model.EffectiveConfig{}, nil, err
	}
	if profileName != "" {
		projectCfg.Profile = profileName
	}
	effective, err := config.Merge(globalCfg, projectCfg, root)
	if err != nil {
		return model.EffectiveConfig{}, nil, err
	}
	return effective, secret.CompositeStore{}, nil
}

func printEffectiveConfig(console *ui.Console, effective model.EffectiveConfig) error {
	payload := map[string]any{
		"provider":       effective.Provider,
		"role_models":    effective.RoleModels,
		"profile":        effective.Profile,
		"run_output_dir": effective.RunOutputDir,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	console.Println(string(data))
	return nil
}

func printUsage(out io.Writer) {
	_, _ = fmt.Fprintln(out, "agent-engine")
	_, _ = fmt.Fprintln(out, "Usage:")
	_, _ = fmt.Fprintln(out, "  agent-engine init --root <project>")
	_, _ = fmt.Fprintln(out, "  agent-engine run --root <project> [--profile name] [--goal text]")
	_, _ = fmt.Fprintln(out, "  agent-engine validate --root <project>")
	_, _ = fmt.Fprintln(out, "  agent-engine config --root <project>")
}

func validateProviderConnectivity(ctx context.Context, client provider.Client, models model.RoleModels) error {
	if client == nil {
		return fmt.Errorf("provider client is required")
	}
	checks := []struct {
		name  string
		model string
	}{
		{name: "executor", model: models.Executor},
		{name: "judge", model: models.Judge},
		{name: "optimizer", model: models.Optimizer},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.model) == "" {
			return fmt.Errorf("%s model is empty", check.name)
		}
		if err := client.ProbeJSON(ctx, check.model); err != nil {
			return fmt.Errorf("%s model probe failed: %w", check.name, err)
		}
	}
	return nil
}
