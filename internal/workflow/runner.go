package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent-engine/internal/artifact"
	"agent-engine/internal/model"
	"agent-engine/internal/project"
	"agent-engine/internal/provider"
	"agent-engine/internal/secret"
)

type Prompter interface {
	Ask(prompt string) (string, error)
	AskDefault(prompt, defaultValue string) (string, error)
	Confirm(prompt string, defaultValue bool) (bool, error)
	Println(args ...any)
	Errorln(args ...any)
}

type RunMode string

const (
	RunModeRun  RunMode = "run"
	RunModeScan RunMode = "scan"
)

type Options struct {
	ProjectRoot string
	ProfileName string
	Goal        *model.GoalInput
	DryRun      bool
	Mode        RunMode
}

type Runner struct {
	Effective   model.EffectiveConfig
	Prompter    Prompter
	Provider    provider.Client
	Secrets     secret.Store
	ProjectRoot string
}

type result struct {
	workspace    string
	runID        string
	runDir       string
	artifacts    *artifact.Store
	runContext   model.RunContext
	trace        []model.TraceEvent
	profile      model.ProfileDefinition
	goal         model.GoalInput
	plan         model.PlanDoc
	judgment     model.JudgmentDoc
	optimization model.OptimizationDoc
	decision     model.FinalDecision
	execResult   model.ExecutionResult
	verifyResult model.ExecutionResult
}

func (r *result) summary(projectRoot string) model.RunSummary {
	return model.RunSummary{
		RunID:       r.runID,
		ProjectRoot: projectRoot,
		Profile:     r.profile.Name,
		Goal:        r.goal,
		Accepted:    r.decision.Accept,
		Workspace:   r.workspace,
		RunDir:      r.runDir,
	}
}

func (r *result) addTrace(stage string, started time.Time, fields map[string]string) {
	r.trace = append(r.trace, model.TraceEvent{
		Stage:     stage,
		Model:     "",
		StartedAt: started,
		EndedAt:   time.Now(),
		Fields:    fields,
	})
	r.runContext.Stages = append(r.runContext.Stages, model.StageRecord{
		Stage:     stage,
		StartedAt: started,
		EndedAt:   time.Now(),
		Fields:    fields,
	})
	r.runContext.UpdatedAt = time.Now()
	_ = r.syncArtifacts()
}

func (r *result) addStage(stage, modelName string, started time.Time, fields map[string]string) {
	r.trace = append(r.trace, model.TraceEvent{
		Stage:     stage,
		Model:     modelName,
		StartedAt: started,
		EndedAt:   time.Now(),
		Fields:    fields,
	})
	r.runContext.Stages = append(r.runContext.Stages, model.StageRecord{
		Stage:     stage,
		Model:     modelName,
		StartedAt: started,
		EndedAt:   time.Now(),
		Fields:    fields,
	})
	r.runContext.UpdatedAt = time.Now()
	_ = r.syncArtifacts()
}

func (r *result) syncArtifacts() error {
	if r.artifacts == nil {
		return nil
	}
	return r.artifacts.WriteJSON("run-context.json", r.runContext)
}

func NewRunner(effective model.EffectiveConfig, prompter Prompter, client provider.Client, secrets secret.Store, projectRoot string) *Runner {
	return &Runner{
		Effective:   effective,
		Prompter:    prompter,
		Provider:    client,
		Secrets:     secrets,
		ProjectRoot: projectRoot,
	}
}

func (r *Runner) Run(ctx context.Context, opts Options) (model.RunSummary, error) {
	if opts.ProjectRoot == "" {
		opts.ProjectRoot = r.ProjectRoot
	}
	if opts.ProjectRoot == "" {
		return model.RunSummary{}, errors.New("project root is required")
	}
	if r.Provider == nil {
		return model.RunSummary{}, errors.New("LLM provider is required")
	}
	if r.Prompter == nil {
		return model.RunSummary{}, errors.New("prompter is required")
	}
	if err := r.Provider.HealthCheck(ctx); err != nil {
		return model.RunSummary{}, fmt.Errorf("provider health check failed: %w", err)
	}
	profileName := r.Effective.Profile.Name
	if profileName == "" {
		profileName = opts.ProfileName
	}
	r.announce("Starting the code optimization workflow")
	r.announce("Project root: %s", opts.ProjectRoot)
	r.announce("Run mode: profile=%s dry-run=%t", profileName, opts.DryRun)

	runID := newRunID()
	runDir := filepath.Join(r.Effective.RunOutputDir, runID)
	artifacts, err := artifact.New(runDir)
	if err != nil {
		return model.RunSummary{}, err
	}

	state := &result{
		runID:     runID,
		runDir:    runDir,
		artifacts: artifacts,
		trace:     make([]model.TraceEvent, 0, 8),
		profile:   r.Effective.Profile,
	}
	state.runContext = model.RunContext{
		RunID:       runID,
		ProjectRoot: opts.ProjectRoot,
		Profile:     r.Effective.Profile.Name,
		RunDir:      runDir,
		Provider:    r.Provider.Name(),
		RoleModels:  r.Effective.RoleModels,
		StartedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	defer func() {
		if artifacts != nil {
			_ = artifacts.WriteJSON("trace.json", state.trace)
			_ = state.syncArtifacts()
		}
	}()

	if err := artifacts.WriteJSON("effective-config.json", r.Effective); err != nil {
		return model.RunSummary{}, err
	}
	if err := state.syncArtifacts(); err != nil {
		return model.RunSummary{}, err
	}
	r.announce("Run directory created: %s", runDir)

	workspace, err := os.MkdirTemp("", "agent-engine-workspace-*")
	if err != nil {
		return model.RunSummary{}, err
	}
	state.workspace = workspace
	state.runContext.Workspace = workspace
	state.runContext.DryRun = opts.DryRun
	defer os.RemoveAll(workspace)
	if err := project.CopyDir(opts.ProjectRoot, workspace); err != nil {
		return model.RunSummary{}, err
	}
	r.announce("Isolated workspace ready: %s", workspace)

	if err := artifacts.WriteText("workspace.txt", workspace); err != nil {
		return model.RunSummary{}, err
	}
	if err := state.syncArtifacts(); err != nil {
		return model.RunSummary{}, err
	}

	goal, err := r.resolveGoal(ctx, opts, artifacts)
	if err != nil {
		return model.RunSummary{}, err
	}
	state.goal = goal
	state.runContext.Goal = goal
	if err := artifacts.WriteJSON("goal.json", goal); err != nil {
		return model.RunSummary{}, err
	}
	if err := state.syncArtifacts(); err != nil {
		return model.RunSummary{}, err
	}
	r.announce("Goal confirmed: %s", goal.Direction)

	var execResult model.ExecutionResult
	if opts.Mode == RunModeScan {
		r.announce("Scan mode selected: skipping the live command and keeping the review/rollback loop")
		execResult = r.staticScanResult(workspace)
		state.addStage("scan", "static-scan", time.Now(), map[string]string{
			"files": fmt.Sprintf("%d", countLines(execResult.Stdout)),
		})
		if err := artifacts.WriteJSON("execution.json", execResult); err != nil {
			return model.RunSummary{}, err
		}
		r.printCommandResult("Static scan", execResult)
	} else {
		r.announce("Running baseline command: %s", strings.Join(r.Effective.Profile.Executor.Command, " "))
		execResult, err = r.runCommand(ctx, workspace, r.Effective.Profile.Executor)
		if err != nil {
			return model.RunSummary{}, err
		}
		state.addTrace("execute", time.Now().Add(-execResult.Duration), map[string]string{
			"command":   strings.Join(execResult.Command, " "),
			"exit_code": fmt.Sprintf("%d", execResult.ExitCode),
		})
		if err := artifacts.WriteJSON("execution.json", execResult); err != nil {
			return model.RunSummary{}, err
		}
		r.printCommandResult("Baseline command", execResult)
	}
	state.execResult = execResult

	r.announce("Generating judgment and preview")
	var judgment model.JudgmentDoc
	for {
		judgment, err = r.generateJudgment(ctx, state, goal, execResult)
		if err != nil {
			return model.RunSummary{}, err
		}
		state.judgment = judgment
		if err := artifacts.WriteJSON("judgment.json", judgment); err != nil {
			return model.RunSummary{}, err
		}

		plan, err := r.generatePlan(ctx, state, goal, judgment, workspace)
		if err != nil {
			return model.RunSummary{}, err
		}
		state.plan = plan
		if err := artifacts.WriteJSON("plan.json", plan); err != nil {
			return model.RunSummary{}, err
		}

		if r.Prompter != nil {
			r.Prompter.Println("Judgment summary:", judgment.Summary)
			r.Prompter.Println("Preview summary:", plan.Summary)
		}
		r.announce("Judgment ready: %s", judgment.Summary)
		r.announce("Preview ready: %s", plan.Summary)

		if opts.DryRun {
			r.announce("Dry run complete; no files were changed")
			return state.summary(opts.ProjectRoot), nil
		}

		r.announce("Waiting for your confirmation to continue")
		action, err := r.Prompter.Ask("Enter c=continue, m=edit goal and regenerate, x=cancel [x]: ")
		if err != nil {
			return model.RunSummary{}, err
		}
		state.addTrace("confirm", time.Now(), map[string]string{
			"action": strings.ToLower(strings.TrimSpace(action)),
		})
		switch strings.ToLower(strings.TrimSpace(action)) {
		case "c", "continue", "y", "yes":
			goto optimize
		case "m", "modify":
			r.announce("You chose to revise the goal")
			goal, err = r.resolveGoal(ctx, opts, artifacts)
			if err != nil {
				return model.RunSummary{}, err
			}
			state.goal = goal
			state.runContext.Goal = goal
			_ = state.syncArtifacts()
			continue
		default:
			return state.summary(opts.ProjectRoot), nil
		}
	}

optimize:
	r.announce("Generating optimization plan")
	optimization, touched, initialHashes, err := r.generateOptimization(ctx, state, goal, judgment, state.plan, workspace)
	if err != nil {
		return model.RunSummary{}, err
	}
	state.optimization = optimization
	if err := artifacts.WriteJSON("optimization.json", optimization); err != nil {
		return model.RunSummary{}, err
	}
	if len(optimization.Edits) == 0 {
		state.decision = model.FinalDecision{
			Accept: false,
			Reason: "optimizer returned no edits",
		}
		state.addStage("optimize", r.Effective.RoleModels.Optimizer, time.Now(), map[string]string{
			"edits": "0",
			"note":  "no code changes proposed",
		})
		if err := artifacts.WriteJSON("decision.json", state.decision); err != nil {
			return model.RunSummary{}, err
		}
		if err := artifacts.WriteText("status.txt", "no_edits"); err != nil {
			return model.RunSummary{}, err
		}
		r.announce("No actionable code changes were produced; stopping before writeback")
		return state.summary(opts.ProjectRoot), nil
	}
	r.announce("Optimization plan ready; creating backups before applying changes")

	workspaceBackup := filepath.Join(runDir, "workspace-backup")
	if err := project.BackupFiles(workspace, workspaceBackup, touched); err != nil {
		return model.RunSummary{}, err
	}
	r.announce("Backed up %d impacted files", len(touched))

	if err := project.ApplyEdits(workspace, optimization.Edits); err != nil {
		if restoreErr := project.RestoreFiles(workspaceBackup, workspace, touched); restoreErr != nil {
			return model.RunSummary{}, fmt.Errorf("apply edits failed: %v; restore failed: %w", err, restoreErr)
		}
		return model.RunSummary{}, err
	}
	r.announce("Applied edits in the isolated workspace")

	var verifyResult model.ExecutionResult
	if opts.Mode == RunModeScan {
		r.announce("Using a static rescan for verification in scan mode")
		verifyResult = r.staticScanResult(workspace)
		state.addStage("verify", "static-scan", time.Now(), map[string]string{
			"files": fmt.Sprintf("%d", countLines(verifyResult.Stdout)),
		})
		if err := artifacts.WriteJSON("verification.json", verifyResult); err != nil {
			return model.RunSummary{}, err
		}
		r.printCommandResult("Static rescan", verifyResult)
	} else {
		r.announce("Running verification command: %s", strings.Join(r.Effective.Profile.Verify.Command, " "))
		verifyResult, err = r.runCommand(ctx, workspace, r.Effective.Profile.Verify)
		if err != nil {
			return model.RunSummary{}, err
		}
		if err := artifacts.WriteJSON("verification.json", verifyResult); err != nil {
			return model.RunSummary{}, err
		}
		state.addTrace("verify", time.Now().Add(-verifyResult.Duration), map[string]string{
			"command":   strings.Join(verifyResult.Command, " "),
			"exit_code": fmt.Sprintf("%d", verifyResult.ExitCode),
		})
		r.printCommandResult("Verification command", verifyResult)
	}
	state.verifyResult = verifyResult

	r.announce("Generating final decision")
	decision, err := r.generateDecision(ctx, state, goal, judgment, verifyResult)
	if err != nil {
		return model.RunSummary{}, err
	}
	state.decision = decision
	if err := artifacts.WriteJSON("decision.json", decision); err != nil {
		return model.RunSummary{}, err
	}
	if opts.Mode == RunModeScan {
		accepted, err := r.promptScanReview(artifacts, workspace, optimization, decision)
		if err != nil {
			return model.RunSummary{}, err
		}
		decision.Accept = accepted
		if accepted {
			if decision.Reason == "" {
				decision.Reason = "scan mode accepted by user"
			} else {
				decision.Reason = decision.Reason + "; scan mode accepted by user"
			}
		} else if decision.Reason == "" {
			decision.Reason = "scan mode rolled back by user"
		} else {
			decision.Reason = decision.Reason + "; scan mode rolled back by user"
		}
		state.decision = decision
		if err := artifacts.WriteJSON("decision.json", decision); err != nil {
			return model.RunSummary{}, err
		}
	}
	if !decision.Accept {
		r.announce("Final decision rejected; rolling back changes")
		if restoreErr := project.RestoreFiles(workspaceBackup, workspace, touched); restoreErr != nil {
			return model.RunSummary{}, fmt.Errorf("rollback failed: %w", restoreErr)
		}
		state.addTrace("rollback", time.Now(), map[string]string{
			"reason": decision.Reason,
		})
		if err := artifacts.WriteText("status.txt", "rolled_back"); err != nil {
			return model.RunSummary{}, err
		}
		r.announce("Rollback complete")
		return state.summary(opts.ProjectRoot), nil
	}

	r.announce("Final decision accepted; writing changes back to the original project")
	originalBackup := filepath.Join(runDir, "original-backup")
	if err := project.BackupFiles(opts.ProjectRoot, originalBackup, touched); err != nil {
		return model.RunSummary{}, err
	}
	if err := verifyOriginalUnchanged(opts.ProjectRoot, initialHashes); err != nil {
		return model.RunSummary{}, err
	}
	if err := project.SyncEdits(workspace, opts.ProjectRoot, optimization.Edits); err != nil {
		return model.RunSummary{}, err
	}
	state.addTrace("writeback", time.Now(), map[string]string{
		"files": fmt.Sprintf("%d", len(optimization.Edits)),
	})
	if err := artifacts.WriteText("status.txt", "accepted"); err != nil {
		return model.RunSummary{}, err
	}
	r.announce("Writeback complete: %d files", len(optimization.Edits))

	return state.summary(opts.ProjectRoot), nil
}

func (r *Runner) announce(format string, args ...any) {
	if r.Prompter == nil {
		return
	}
	r.Prompter.Println(fmt.Sprintf(format, args...))
}

func (r *Runner) printCommandResult(label string, result model.ExecutionResult) {
	if r.Prompter == nil {
		return
	}
	r.Prompter.Println(fmt.Sprintf("%s complete: exit=%d, duration=%s, cwd=%s", label, result.ExitCode, result.Duration.Round(time.Millisecond), result.WorkingDir))
	if stdout := strings.TrimSpace(result.Stdout); stdout != "" {
		r.Prompter.Println(fmt.Sprintf("%s stdout: %s", label, truncate(stdout, 2000)))
	}
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		r.Prompter.Println(fmt.Sprintf("%s stderr: %s", label, truncate(stderr, 2000)))
	}
}

func (r *Runner) promptScanReview(store *artifact.Store, workspace string, optimization model.OptimizationDoc, decision model.FinalDecision) (bool, error) {
	review := renderScanReview(workspace, optimization, decision)
	if r.Prompter != nil {
		r.Prompter.Println("Scan mode review has been written to the isolated workspace:", workspace)
		if len(optimization.Edits) > 0 {
			r.Prompter.Println("Changed files:", strings.Join(plannedPathsFromEdits(optimization.Edits), ", "))
		}
		r.Prompter.Println("Review the workspace changes first, then decide whether to accept or roll back.")
		r.Prompter.Println("Change summary:", review)
	}
	if store != nil {
		_ = store.WriteText("scan-review.txt", review)
	}
	for {
		action, err := r.Prompter.Ask("Enter a=accept, r=roll back [r]: ")
		if err != nil {
			return false, err
		}
		action = strings.TrimSpace(action)
		if action == "" {
			action = "r"
		}
		switch strings.ToLower(action) {
		case "a", "accept", "adopt", "y", "yes":
			r.announce("Scan mode: user accepted the changes")
			return true, nil
		case "r", "rollback", "n", "no":
			r.announce("Scan mode: user chose to roll back the changes")
			return false, nil
		default:
			r.announce("Please enter a or r")
		}
	}
}

func renderScanReview(workspace string, optimization model.OptimizationDoc, decision model.FinalDecision) string {
	var builder strings.Builder
	builder.WriteString("workspace: ")
	builder.WriteString(workspace)
	builder.WriteString("\n")
	builder.WriteString("decision: ")
	if decision.Accept {
		builder.WriteString("accept")
	} else {
		builder.WriteString("reject")
	}
	builder.WriteString("\n")
	builder.WriteString("files:\n")
	if len(optimization.Edits) == 0 {
		builder.WriteString("- none\n")
		return builder.String()
	}
	for _, edit := range optimization.Edits {
		builder.WriteString("- ")
		builder.WriteString(edit.Path)
		builder.WriteString(" (")
		builder.WriteString(edit.Action)
		builder.WriteString(")\n")
	}
	return builder.String()
}

func (r *Runner) staticScanResult(workspace string) model.ExecutionResult {
	files := listWorkspaceFiles(workspace, 80, r.Effective.Profile.SensitivePaths)
	return model.ExecutionResult{
		Command:    []string{"static-scan"},
		ExitCode:   0,
		Stdout:     strings.Join(files, "\n"),
		WorkingDir: workspace,
	}
}

func countLines(input string) int {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0
	}
	return strings.Count(input, "\n") + 1
}

func (r *Runner) resolveGoal(ctx context.Context, opts Options, artifacts *artifact.Store) (model.GoalInput, error) {
	goal := model.GoalInput{}
	if opts.Goal != nil && !opts.Goal.Empty() {
		goal = *opts.Goal
	} else if r.Effective.Project.GoalTemplate != nil && !r.Effective.Project.GoalTemplate.Empty() {
		goal = *r.Effective.Project.GoalTemplate
	}

	if goal.Direction == "" {
		direction, err := r.Prompter.Ask("Enter the optimization goal (for example: simplify logic, improve performance): ")
		if err != nil {
			return model.GoalInput{}, err
		}
		goal.Direction = direction
	}
	if goal.Direction == "" {
		return model.GoalInput{}, errors.New("optimization goal is required")
	}

	if goal.Constraints == "" {
		value, err := r.Prompter.AskDefault("Constraints (optional)", "")
		if err != nil {
			return model.GoalInput{}, err
		}
		goal.Constraints = value
	}
	if goal.SuccessCriteria == "" {
		value, err := r.Prompter.AskDefault("Success criteria (optional)", "")
		if err != nil {
			return model.GoalInput{}, err
		}
		goal.SuccessCriteria = value
	}
	if goal.RiskPreference == "" {
		value, err := r.Prompter.AskDefault("Risk preference (conservative/balanced/aggressive, optional)", "conservative")
		if err != nil {
			return model.GoalInput{}, err
		}
		goal.RiskPreference = value
	}
	if goal.Notes == "" {
		value, err := r.Prompter.AskDefault("Notes (optional)", "")
		if err != nil {
			return model.GoalInput{}, err
		}
		goal.Notes = value
	}
	_ = artifacts.WriteJSON("goal-input.json", goal)
	return goal, nil
}

func (r *Runner) runCommand(ctx context.Context, cwd string, spec model.CommandSpec) (model.ExecutionResult, error) {
	if len(spec.Command) == 0 {
		return model.ExecutionResult{}, errors.New("command is empty")
	}
	command := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	command.Dir = cwd
	if spec.WorkingDir != "" {
		command.Dir = filepath.Join(cwd, spec.WorkingDir)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	start := time.Now()
	err := command.Run()
	duration := time.Since(start)
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return model.ExecutionResult{}, err
		}
	}
	return model.ExecutionResult{
		Command:    spec.Command,
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		Duration:   duration,
		WorkingDir: command.Dir,
	}, nil
}

func writeLLMExchange(store *artifact.Store, stage, systemPrompt, userPrompt string, out any, fn func() error) error {
	if store != nil {
		_ = store.WriteText(stage+".prompt.txt", renderPrompt(systemPrompt, userPrompt))
	}
	if err := fn(); err != nil {
		if store != nil {
			_ = store.WriteText(stage+".response.txt", err.Error())
		}
		return err
	}
	if store != nil {
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		_ = store.WriteText(stage+".response.json", string(data))
	}
	return nil
}

func renderPrompt(systemPrompt, userPrompt string) string {
	var builder strings.Builder
	builder.WriteString("SYSTEM:\n")
	builder.WriteString(systemPrompt)
	builder.WriteString("\n\nUSER:\n")
	builder.WriteString(userPrompt)
	builder.WriteString("\n")
	return builder.String()
}

func (r *Runner) generateJudgment(ctx context.Context, state *result, goal model.GoalInput, execResult model.ExecutionResult) (model.JudgmentDoc, error) {
	var judgment model.JudgmentDoc
	started := time.Now()
	systemPrompt := "You are the code optimization judgment assistant. Given the user's goal and execution result, output JSON with fields summary, findings, risks, recommendation, require_revision, accepted, suggested_goal. Prefer suggested_goal as an object with direction, constraints, success_criteria, risk_preference, and notes. If no structured suggestion is available, omit the field."
	userPrompt := fmt.Sprintf("Goal: %s\n\nExecution result:\nstdout:\n%s\nstderr:\n%s\nexit_code:%d\n", renderGoalInput(goal), truncate(execResult.Stdout, 6000), truncate(execResult.Stderr, 4000), execResult.ExitCode)
	if err := writeLLMExchange(state.artifacts, "judgment", systemPrompt, userPrompt, &judgment, func() error {
		return r.Provider.CompleteJSON(ctx, r.Effective.RoleModels.Judge, systemPrompt, userPrompt, &judgment)
	}); err != nil {
		state.addStage("judge", r.Effective.RoleModels.Judge, started, map[string]string{
			"error":             err.Error(),
			"prompt_artifact":   "judgment.prompt.txt",
			"response_artifact": "judgment.response.txt",
		})
		return model.JudgmentDoc{}, err
	}
	state.addStage("judge", r.Effective.RoleModels.Judge, started, map[string]string{
		"accepted":          fmt.Sprintf("%t", judgment.Accepted),
		"prompt_artifact":   "judgment.prompt.txt",
		"response_artifact": "judgment.response.json",
	})
	return judgment, nil
}

func (r *Runner) generatePlan(ctx context.Context, state *result, goal model.GoalInput, judgment model.JudgmentDoc, workspace string) (model.PlanDoc, error) {
	var plan model.PlanDoc
	started := time.Now()
	files := listWorkspaceFiles(workspace, 40, r.Effective.Profile.SensitivePaths)
	sourceTargets := collectSourceCandidates(workspace, 8, r.Effective.Profile.SensitivePaths)
	systemPrompt := "You are the optimization preview assistant. Given the goal and judgment, output JSON with fields summary, changes, notes. changes should be a list of file path, action, and reason entries. Do not modify files."
	userPrompt := fmt.Sprintf("Goal: %s\nJudgment summary:\n%s\nWorkspace files: %s\n", renderGoalInput(goal), renderOptimizationJudgment(judgment), strings.Join(files, ", "))
	if len(sourceTargets) > 0 {
		userPrompt += "Candidate source excerpts:\n"
		userPrompt += snapshotFiles(workspace, sourceTargets)
		userPrompt += "Prefer returning concrete file paths from the candidate excerpts when suggesting repeated-logic cleanup.\n"
	}
	if err := writeLLMExchange(state.artifacts, "preview", systemPrompt, userPrompt, &plan, func() error {
		return r.Provider.CompleteJSON(ctx, r.Effective.RoleModels.Judge, systemPrompt, userPrompt, &plan)
	}); err != nil {
		state.addStage("preview", r.Effective.RoleModels.Judge, started, map[string]string{
			"error":             err.Error(),
			"prompt_artifact":   "preview.prompt.txt",
			"response_artifact": "preview.response.txt",
		})
		return model.PlanDoc{}, err
	}
	plan, skippedPreviewPaths := filterPlannedChanges(plan, r.Effective.Profile.SensitivePaths)
	if len(plan.Changes) == 0 {
		fallback := fallbackPlannedChanges(sourceTargets)
		if len(fallback) > 0 {
			r.announce("Preview returned no concrete file paths; using %d generic source candidates", len(fallback))
			plan.Changes = fallback
			plan.Notes = append(plan.Notes, "Generic source candidates were auto-selected because the preview response did not include concrete file paths.")
		}
	}
	if len(skippedPreviewPaths) > 0 {
		r.announce("Preview output included %d sensitive paths; they were ignored", len(skippedPreviewPaths))
		plan.Notes = append(plan.Notes, fmt.Sprintf("Ignored sensitive paths: %s", strings.Join(skippedPreviewPaths, ", ")))
	}
	state.addStage("preview", r.Effective.RoleModels.Judge, started, map[string]string{
		"changes":           fmt.Sprintf("%d", len(plan.Changes)),
		"skipped":           fmt.Sprintf("%d", len(skippedPreviewPaths)),
		"prompt_artifact":   "preview.prompt.txt",
		"response_artifact": "preview.response.json",
	})
	return plan, nil
}

func (r *Runner) generateOptimization(ctx context.Context, state *result, goal model.GoalInput, judgment model.JudgmentDoc, plan model.PlanDoc, workspace string) (model.OptimizationDoc, []string, map[string]string, error) {
	started := time.Now()
	candidateSpecs := plannedPathSpecs(plan)
	candidatePaths, skippedCandidatePaths := resolveCandidatePaths(workspace, candidateSpecs, r.Effective.Profile.SensitivePaths)
	if len(skippedCandidatePaths) > 0 {
		r.announce("Candidate edits included %d sensitive paths; they were ignored", len(skippedCandidatePaths))
	}

	fileContents := snapshotFiles(workspace, candidatePaths)
	optimization, err := r.requestOptimization(ctx, state, goal, judgment, plan, candidateSpecs, candidatePaths, fileContents, false, "optimization", started)
	if err != nil {
		return model.OptimizationDoc{}, nil, nil, err
	}
	retried := false
	if len(optimization.Edits) == 0 {
		retried = true
		r.announce("Optimizer returned no executable edits; retrying and requiring at least one actionable change")
		optimization, err = r.requestOptimization(ctx, state, goal, judgment, plan, candidateSpecs, candidatePaths, fileContents, true, "optimization-retry", time.Now())
		if err != nil {
			return model.OptimizationDoc{}, nil, nil, err
		}
	}

	var skippedEditPaths []string
	optimization.Edits, skippedEditPaths = filterAllowedEdits(optimization.Edits, r.Effective.Profile.SensitivePaths)
	if len(skippedEditPaths) > 0 {
		r.announce("Optimization output included %d sensitive paths; they were ignored", len(skippedEditPaths))
	}
	var skippedNoopPaths []string
	optimization.Edits, skippedNoopPaths = filterSubstantiveEdits(workspace, optimization.Edits)
	if len(skippedNoopPaths) > 0 {
		r.announce("Optimization output included %d no-op edits; they were ignored", len(skippedNoopPaths))
	}
	semanticRetried := false
	if len(optimization.Edits) == 0 {
		semanticRetried = true
		r.announce("Optimization output was still a no-op; retrying and asking for visible structural changes")
		optimization, err = r.requestOptimization(ctx, state, goal, judgment, plan, candidateSpecs, candidatePaths, fileContents, true, "optimization-semantic-retry", time.Now())
		if err != nil {
			return model.OptimizationDoc{}, nil, nil, err
		}
		optimization.Edits, skippedEditPaths = filterAllowedEdits(optimization.Edits, r.Effective.Profile.SensitivePaths)
		if len(skippedEditPaths) > 0 {
			r.announce("Optimization output included %d sensitive paths; they were ignored", len(skippedEditPaths))
		}
		optimization.Edits, skippedNoopPaths = filterSubstantiveEdits(workspace, optimization.Edits)
		if len(skippedNoopPaths) > 0 {
			r.announce("Optimization output included %d no-op edits; they were ignored", len(skippedNoopPaths))
		}
	}
	paths := plannedPathsFromEdits(optimization.Edits)
	initialHashes := hashFiles(workspace, paths)
	state.addStage("optimize", r.Effective.RoleModels.Optimizer, started, map[string]string{
		"edits":             fmt.Sprintf("%d", len(optimization.Edits)),
		"attempts":          fmt.Sprintf("%d", 1+boolToInt(retried)+boolToInt(semanticRetried)),
		"retry":             fmt.Sprintf("%t", retried || semanticRetried),
		"skipped":           fmt.Sprintf("%d", len(skippedCandidatePaths)+len(skippedEditPaths)+len(skippedNoopPaths)),
		"prompt_artifact":   "optimization.prompt.txt",
		"response_artifact": semanticResponseArtifact(retried, semanticRetried),
	})
	return optimization, paths, initialHashes, nil
}

func (r *Runner) requestOptimization(ctx context.Context, state *result, goal model.GoalInput, judgment model.JudgmentDoc, plan model.PlanDoc, candidateSpecs []string, candidatePaths []string, fileContents string, retry bool, stage string, started time.Time) (model.OptimizationDoc, error) {
	var optimization model.OptimizationDoc
	systemPrompt := optimizationSystemPrompt(retry)
	userPrompt := optimizationUserPrompt(goal, judgment, plan, candidateSpecs, candidatePaths, fileContents, retry)
	if err := writeLLMExchange(state.artifacts, stage, systemPrompt, userPrompt, &optimization, func() error {
		return r.Provider.CompleteJSON(ctx, r.Effective.RoleModels.Optimizer, systemPrompt, userPrompt, &optimization)
	}); err != nil {
		state.addStage(stage, r.Effective.RoleModels.Optimizer, time.Now(), map[string]string{
			"error":             err.Error(),
			"prompt_artifact":   stage + ".prompt.txt",
			"response_artifact": stage + ".response.txt",
		})
		return model.OptimizationDoc{}, err
	}
	state.addStage(stage, r.Effective.RoleModels.Optimizer, started, map[string]string{
		"edits":             fmt.Sprintf("%d", len(optimization.Edits)),
		"prompt_artifact":   stage + ".prompt.txt",
		"response_artifact": stage + ".response.json",
		"retry":             fmt.Sprintf("%t", retry),
	})
	return optimization, nil
}

func (r *Runner) generateDecision(ctx context.Context, state *result, goal model.GoalInput, judgment model.JudgmentDoc, verifyResult model.ExecutionResult) (model.FinalDecision, error) {
	var decision model.FinalDecision
	started := time.Now()
	systemPrompt := "You are the final decision assistant. Given the user's goal, judgment result, and verification output, output JSON with fields accept and reason."
	userPrompt := fmt.Sprintf("Goal: %s\nJudgment:\n%s\nVerification output:\nstdout:\n%s\nstderr:\n%s\nexit_code:%d\n", renderGoalInput(goal), renderOptimizationJudgment(judgment), truncate(verifyResult.Stdout, 6000), truncate(verifyResult.Stderr, 4000), verifyResult.ExitCode)
	if err := writeLLMExchange(state.artifacts, "decision", systemPrompt, userPrompt, &decision, func() error {
		return r.Provider.CompleteJSON(ctx, r.Effective.RoleModels.Judge, systemPrompt, userPrompt, &decision)
	}); err != nil {
		state.addStage("decision", r.Effective.RoleModels.Judge, started, map[string]string{
			"error":             err.Error(),
			"prompt_artifact":   "decision.prompt.txt",
			"response_artifact": "decision.response.txt",
		})
		return model.FinalDecision{}, err
	}
	state.addStage("decision", r.Effective.RoleModels.Judge, started, map[string]string{
		"accept":            fmt.Sprintf("%t", decision.Accept),
		"prompt_artifact":   "decision.prompt.txt",
		"response_artifact": "decision.response.json",
	})
	return decision, nil
}

func (r *Runner) summary(projectRoot string) model.RunSummary {
	return model.RunSummary{
		RunID:       "",
		ProjectRoot: projectRoot,
		Profile:     r.Effective.Profile.Name,
		Goal:        model.GoalInput{},
		Accepted:    true,
		Workspace:   "",
		RunDir:      "",
	}
}

func plannedPaths(plan model.PlanDoc) []string {
	paths := make([]string, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		if change.Path != "" {
			paths = append(paths, filepath.Clean(change.Path))
		}
	}
	return unique(paths)
}

func plannedPathSpecs(plan model.PlanDoc) []string {
	specs := make([]string, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		specs = append(specs, splitPathSpecs(change.Path)...)
	}
	return unique(specs)
}

func plannedPathsFromEdits(edits []model.Edit) []string {
	paths := make([]string, 0, len(edits))
	for _, edit := range edits {
		if edit.Path != "" {
			paths = append(paths, filepath.Clean(edit.Path))
		}
	}
	return unique(paths)
}

func optimizationSystemPrompt(retry bool) string {
	base := "You are the code optimizer. You are in the implementation phase, not the preview phase. Given the user's goal, judgment result, candidate edit scope, and file contents, output JSON with fields summary and edits. edits must contain at least 1 item unless the workspace truly has no safe changes available; if nothing can be changed, summary must clearly explain the blocker and the smallest unlocking step, but do not turn the result into more analysis or preview advice. Every edit must make a real code change, not just formatting, comment tweaks, variable renames, content rewrites that keep the file identical, or any semantic no-op. edit.action may only be write, modify, replace, or delete. Do not output extra text."
	if retry {
		base += " The previous round returned empty edits or no-op changes, so this round must provide the smallest visible structural change and must not repeat the preview conclusion."
	}
	return base
}

func optimizationUserPrompt(goal model.GoalInput, judgment model.JudgmentDoc, plan model.PlanDoc, candidateSpecs []string, candidatePaths []string, fileContents string, retry bool) string {
	var builder strings.Builder
	builder.WriteString("Goal: ")
	builder.WriteString(renderGoalInput(goal))
	builder.WriteString("\nJudgment summary:\n")
	builder.WriteString(renderOptimizationJudgment(judgment))
	builder.WriteString("\nPlanned changes:\n")
	builder.WriteString(renderOptimizationTargets(plan))
	builder.WriteString("\nResolved file paths:\n")
	builder.WriteString(renderPathList(candidatePaths))
	if len(candidateSpecs) > 0 {
		builder.WriteString("\nOriginal path specs:\n")
		builder.WriteString(renderSpecList(candidateSpecs))
	}
	builder.WriteString("\nFile contents:\n")
	builder.WriteString(fileContents)
	if retry {
		builder.WriteString("\nThe previous round did not produce executable edits with real change. This round must output at least 1 directly applicable modification that changes the code structure or control flow.")
	}
	return builder.String()
}

func renderGoalInput(goal model.GoalInput) string {
	var builder strings.Builder
	builder.WriteString("{")
	builder.WriteString("direction:")
	builder.WriteString(goal.Direction)
	builder.WriteString(", constraints:")
	builder.WriteString(goal.Constraints)
	builder.WriteString(", success_criteria:")
	builder.WriteString(goal.SuccessCriteria)
	builder.WriteString(", risk_preference:")
	builder.WriteString(goal.RiskPreference)
	builder.WriteString(", notes:")
	builder.WriteString(goal.Notes)
	builder.WriteString("}")
	return builder.String()
}

func renderOptimizationJudgment(judgment model.JudgmentDoc) string {
	var builder strings.Builder
	builder.WriteString("summary: ")
	builder.WriteString(judgment.Summary)
	builder.WriteString("\nfindings:\n")
	for _, finding := range judgment.Findings {
		builder.WriteString("- ")
		builder.WriteString(finding)
		builder.WriteString("\n")
	}
	builder.WriteString("risks:\n")
	for _, risk := range judgment.Risks {
		builder.WriteString("- ")
		builder.WriteString(risk)
		builder.WriteString("\n")
	}
	builder.WriteString("accepted: ")
	builder.WriteString(fmt.Sprintf("%t", judgment.Accepted))
	builder.WriteString("\nrequire_revision: ")
	builder.WriteString(fmt.Sprintf("%t", judgment.RequireRevision))
	if judgment.SuggestedGoal != nil && !judgment.SuggestedGoal.Empty() {
		builder.WriteString("\nsuggested_goal: ")
		builder.WriteString(renderGoalInput(*judgment.SuggestedGoal))
	}
	return builder.String()
}

func renderOptimizationTargets(plan model.PlanDoc) string {
	if len(plan.Changes) == 0 {
		return "- none\n"
	}
	var builder strings.Builder
	for _, change := range plan.Changes {
		builder.WriteString("- ")
		builder.WriteString(change.Path)
		if change.Reason != "" {
			builder.WriteString(" - ")
			builder.WriteString(change.Reason)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func renderPathList(paths []string) string {
	if len(paths) == 0 {
		return "- none\n"
	}
	var builder strings.Builder
	for _, rel := range paths {
		builder.WriteString("- ")
		builder.WriteString(rel)
		builder.WriteString("\n")
	}
	return builder.String()
}

func collectSourceCandidates(root string, limit int, sensitivePatterns []string) []string {
	files := listWorkspaceFiles(root, 100000, sensitivePatterns)
	code := make([]string, 0, len(files))
	tests := make([]string, 0)
	for _, rel := range files {
		if !isSourceCandidatePath(rel) {
			continue
		}
		if isTestLikePath(rel) {
			tests = append(tests, rel)
			continue
		}
		code = append(code, rel)
	}
	sort.Strings(code)
	sort.Strings(tests)
	candidates := append(code, tests...)
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func isSourceCandidatePath(rel string) bool {
	path := strings.ToLower(filepath.ToSlash(rel))
	if path == "" {
		return false
	}
	if strings.Contains(path, "/testdata/") || strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/vendor/") || strings.Contains(path, "/node_modules/") || strings.Contains(path, "/generated/") || strings.Contains(path, "/gen-go/") {
		return false
	}
	if strings.HasPrefix(path, ".agent-engine/") {
		return false
	}
	if strings.HasSuffix(path, ".pb.go") || strings.HasSuffix(path, ".pb.gw.go") {
		return false
	}
	switch {
	case strings.HasSuffix(path, ".go"),
		strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".jsx"),
		strings.HasSuffix(path, ".ts"),
		strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".py"),
		strings.HasSuffix(path, ".java"),
		strings.HasSuffix(path, ".kt"),
		strings.HasSuffix(path, ".kts"),
		strings.HasSuffix(path, ".rs"),
		strings.HasSuffix(path, ".cs"),
		strings.HasSuffix(path, ".rb"),
		strings.HasSuffix(path, ".php"),
		strings.HasSuffix(path, ".c"),
		strings.HasSuffix(path, ".cc"),
		strings.HasSuffix(path, ".cpp"),
		strings.HasSuffix(path, ".h"),
		strings.HasSuffix(path, ".hpp"),
		strings.HasSuffix(path, ".m"),
		strings.HasSuffix(path, ".mm"),
		strings.HasSuffix(path, ".swift"),
		strings.HasSuffix(path, ".sh"),
		strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".toml"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".xml"),
		strings.HasSuffix(path, ".proto"),
		strings.HasSuffix(path, ".sql"):
		return true
	case filepath.Base(path) == "dockerfile",
		filepath.Base(path) == "makefile",
		filepath.Base(path) == "procfile":
		return true
	default:
		return false
	}
}

func isTestLikePath(rel string) bool {
	path := strings.ToLower(filepath.ToSlash(rel))
	return strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/test/") || strings.Contains(path, "/tests/")
}

func fallbackPlannedChanges(paths []string) []model.PlannedChange {
	changes := make([]model.PlannedChange, 0, len(paths))
	for _, rel := range unique(paths) {
		changes = append(changes, model.PlannedChange{
			Path:   rel,
			Action: "review",
			Reason: "Auto-selected generic source candidate because the preview response did not include a concrete file path.",
		})
	}
	return changes
}

func renderSpecList(specs []string) string {
	if len(specs) == 0 {
		return "- none\n"
	}
	var builder strings.Builder
	for _, spec := range specs {
		builder.WriteString("- ")
		builder.WriteString(spec)
		builder.WriteString("\n")
	}
	return builder.String()
}

func filterPlannedChanges(plan model.PlanDoc, sensitivePatterns []string) (model.PlanDoc, []string) {
	filtered := model.PlanDoc{
		Summary: plan.Summary,
		Notes:   append([]string(nil), plan.Notes...),
	}
	skipped := make([]string, 0)
	for _, change := range plan.Changes {
		path := filepath.Clean(change.Path)
		if path == "." || path == "" || project.SensitivePath(path, sensitivePatterns) {
			if path != "" && path != "." {
				skipped = append(skipped, path)
			}
			continue
		}
		change.Path = path
		filtered.Changes = append(filtered.Changes, change)
	}
	return filtered, unique(skipped)
}

func resolveCandidatePaths(root string, specs []string, sensitivePatterns []string) ([]string, []string) {
	files := listWorkspaceFiles(root, 100000, sensitivePatterns)
	allowed := make([]string, 0, len(files))
	skipped := make([]string, 0)
	seen := map[string]struct{}{}
	for _, spec := range unique(specs) {
		for _, rel := range expandPathSpec(root, spec, files) {
			if project.SensitivePath(rel, sensitivePatterns) {
				skipped = append(skipped, rel)
				continue
			}
			if _, ok := seen[rel]; ok {
				continue
			}
			seen[rel] = struct{}{}
			allowed = append(allowed, rel)
		}
	}
	return allowed, unique(skipped)
}

func filterAllowedEdits(edits []model.Edit, sensitivePatterns []string) ([]model.Edit, []string) {
	allowed := make([]model.Edit, 0, len(edits))
	skipped := make([]string, 0)
	for _, edit := range edits {
		path := filepath.Clean(edit.Path)
		if path == "." || path == "" || project.SensitivePath(path, sensitivePatterns) {
			if path != "" && path != "." {
				skipped = append(skipped, path)
			}
			continue
		}
		edit.Path = path
		allowed = append(allowed, edit)
	}
	return allowed, unique(skipped)
}

func filterSubstantiveEdits(root string, edits []model.Edit) ([]model.Edit, []string) {
	allowed := make([]model.Edit, 0, len(edits))
	skipped := make([]string, 0)
	for _, edit := range edits {
		if !isSubstantiveEdit(root, edit) {
			if edit.Path != "" {
				skipped = append(skipped, filepath.Clean(edit.Path))
			}
			continue
		}
		allowed = append(allowed, edit)
	}
	return allowed, unique(skipped)
}

func isSubstantiveEdit(root string, edit model.Edit) bool {
	switch strings.ToLower(edit.Action) {
	case "", "write", "modify", "replace":
	default:
		return true
	}
	target := filepath.Join(root, filepath.Clean(edit.Path))
	current, err := os.ReadFile(target)
	if err != nil {
		return true
	}
	if filepath.Ext(edit.Path) == ".go" {
		same, err := semanticEqualGoSource(string(current), edit.Content)
		if err == nil {
			return !same
		}
	}
	return normalizeText(string(current)) != normalizeText(edit.Content)
}

func semanticEqualGoSource(current, proposed string) (bool, error) {
	currentFingerprint, err := goSourceFingerprint(current)
	if err != nil {
		return false, err
	}
	proposedFingerprint, err := goSourceFingerprint(proposed)
	if err != nil {
		return false, err
	}
	return currentFingerprint == proposedFingerprint, nil
}

func goSourceFingerprint(src string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return fmt.Sprintf("%x", sum[:]), nil
}

func normalizeText(input string) string {
	fields := strings.Fields(input)
	return strings.Join(fields, " ")
}

func semanticResponseArtifact(retried, semanticRetried bool) string {
	switch {
	case semanticRetried:
		return "optimization-semantic-retry.response.json"
	case retried:
		return "optimization-retry.response.json"
	default:
		return "optimization.response.json"
	}
}

func splitPathSpecs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '\n', '\r', '\t', ';':
			return true
		default:
			return false
		}
	})
	specs := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		specs = append(specs, filepath.Clean(field))
	}
	return specs
}

func expandPathSpec(root, spec string, files []string) []string {
	spec = filepath.ToSlash(strings.TrimSpace(spec))
	if spec == "" || spec == "." {
		return nil
	}
	if !strings.ContainsAny(spec, "*?[") && !strings.Contains(spec, "**") {
		full := filepath.Join(root, spec)
		info, err := os.Stat(full)
		if err != nil {
			return nil
		}
		if info.IsDir() {
			matches := make([]string, 0)
			prefix := strings.TrimSuffix(filepath.ToSlash(spec), "/") + "/"
			for _, rel := range files {
				if rel == filepath.ToSlash(spec) || strings.HasPrefix(rel, prefix) {
					matches = append(matches, rel)
				}
			}
			return unique(matches)
		}
		return []string{filepath.ToSlash(spec)}
	}

	matches := make([]string, 0)
	for _, rel := range files {
		if matchPathSpec(spec, rel) {
			matches = append(matches, rel)
		}
	}
	return unique(matches)
}

func matchPathSpec(pattern, rel string) bool {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	pats := strings.Split(pattern, "/")
	parts := strings.Split(rel, "/")
	return matchPathSegments(pats, parts)
}

func matchPathSegments(patternParts, pathParts []string) bool {
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}
	if patternParts[0] == "**" {
		for i := 0; i <= len(pathParts); i++ {
			if matchPathSegments(patternParts[1:], pathParts[i:]) {
				return true
			}
		}
		return false
	}
	if len(pathParts) == 0 {
		return false
	}
	ok, err := path.Match(patternParts[0], pathParts[0])
	if err != nil || !ok {
		return false
	}
	return matchPathSegments(patternParts[1:], pathParts[1:])
}

func hashFiles(root string, paths []string) map[string]string {
	hashes := make(map[string]string, len(paths))
	for _, rel := range paths {
		full := filepath.Join(root, rel)
		hash, err := project.HashFile(full)
		if err == nil {
			hashes[rel] = hash
		}
	}
	return hashes
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func unique(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func snapshotFiles(root string, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, rel := range paths {
		full := filepath.Join(root, rel)
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		builder.WriteString("FILE: ")
		builder.WriteString(rel)
		builder.WriteString("\n")
		builder.WriteString(truncate(string(data), 4000))
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func truncate(input string, limit int) string {
	if limit <= 0 || len(input) <= limit {
		return input
	}
	return input[:limit] + "\n...<truncated>..."
}

func listWorkspaceFiles(root string, limit int, sensitivePatterns []string) []string {
	items := make([]string, 0, limit)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, ".agent-engine") {
			return nil
		}
		if project.SensitivePath(rel, sensitivePatterns) {
			return nil
		}
		items = append(items, filepath.ToSlash(rel))
		if len(items) >= limit {
			return errors.New("limit reached")
		}
		return nil
	})
	return items
}

func verifyOriginalUnchanged(root string, hashes map[string]string) error {
	for rel, expected := range hashes {
		current, err := project.HashFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		if current != expected {
			return fmt.Errorf("source file changed during workflow: %s", rel)
		}
	}
	return nil
}

func ensureAllowedPaths(paths []string, sensitivePatterns []string) error {
	for _, rel := range paths {
		if project.SensitivePath(rel, sensitivePatterns) {
			return fmt.Errorf("sensitive path is not allowed: %s", rel)
		}
	}
	return nil
}

func newRunID() string {
	return fmt.Sprintf("%s-%04x", time.Now().UTC().Format("20060102T150405Z"), rand.Intn(0xffff))
}
