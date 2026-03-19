package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Role string

const (
	RoleExecutor  Role = "executor"
	RoleJudge     Role = "judge"
	RoleOptimizer Role = "optimizer"
)

type GoalInput struct {
	Direction       string `json:"direction"`
	Constraints     string `json:"constraints,omitempty"`
	SuccessCriteria string `json:"success_criteria,omitempty"`
	RiskPreference  string `json:"risk_preference,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

func (g *GoalInput) UnmarshalJSON(data []byte) error {
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*g = GoalInput{}
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*g = GoalInput{Direction: text}
		return nil
	}

	var raw struct {
		Direction       json.RawMessage `json:"direction"`
		Constraints     json.RawMessage `json:"constraints"`
		SuccessCriteria json.RawMessage `json:"success_criteria"`
		RiskPreference  json.RawMessage `json:"risk_preference"`
		Notes           json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(data, &raw); err == nil {
		var err error
		if g.Direction, err = flexibleTextFromJSON(raw.Direction); err != nil {
			return fmt.Errorf("direction: %w", err)
		}
		if g.Constraints, err = flexibleTextFromJSON(raw.Constraints); err != nil {
			return fmt.Errorf("constraints: %w", err)
		}
		if g.SuccessCriteria, err = flexibleTextFromJSON(raw.SuccessCriteria); err != nil {
			return fmt.Errorf("success_criteria: %w", err)
		}
		if g.RiskPreference, err = flexibleTextFromJSON(raw.RiskPreference); err != nil {
			return fmt.Errorf("risk_preference: %w", err)
		}
		if g.Notes, err = flexibleTextFromJSON(raw.Notes); err != nil {
			return fmt.Errorf("notes: %w", err)
		}
		return nil
	}

	if text, err := flexibleTextFromJSON(data); err == nil {
		*g = GoalInput{Direction: text}
		return nil
	}

	return fmt.Errorf("unsupported goal value: %s", string(data))
}

func (g GoalInput) Empty() bool {
	return g.Direction == "" && g.Constraints == "" && g.SuccessCriteria == "" && g.RiskPreference == "" && g.Notes == ""
}

func flexibleTextFromJSON(data []byte) (string, error) {
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return "", nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		return text, nil
	}

	var texts []string
	if err := json.Unmarshal(data, &texts); err == nil {
		return strings.Join(texts, "\n"), nil
	}

	var values []any
	if err := json.Unmarshal(data, &values); err == nil {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			parts = append(parts, fmt.Sprint(value))
		}
		return strings.Join(parts, "\n"), nil
	}

	var value any
	if err := json.Unmarshal(data, &value); err == nil {
		return fmt.Sprint(value), nil
	}

	return "", fmt.Errorf("unsupported text value: %s", string(data))
}

type SecretRef struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Account string `json:"account,omitempty"`
}

type ProviderSettings struct {
	Name      string    `json:"name"`
	Endpoint  string    `json:"endpoint"`
	APIKeyRef SecretRef `json:"api_key_ref"`
}

type RoleModels struct {
	Executor  string `json:"executor"`
	Judge     string `json:"judge"`
	Optimizer string `json:"optimizer"`
}

type CommandSpec struct {
	Command    []string `json:"command"`
	WorkingDir string   `json:"working_dir,omitempty"`
}

type ProfileDefinition struct {
	Name           string      `json:"name"`
	Description    string      `json:"description,omitempty"`
	Executor       CommandSpec `json:"executor"`
	Verify         CommandSpec `json:"verify"`
	SensitivePaths []string    `json:"sensitive_paths,omitempty"`
}

type GlobalConfig struct {
	Version         int              `json:"version"`
	DefaultProvider string           `json:"default_provider"`
	Provider        ProviderSettings `json:"provider"`
	RoleModels      RoleModels       `json:"role_models"`
	RunOutputDir    string           `json:"run_output_dir"`
}

type ProjectConfig struct {
	Version      int                          `json:"version"`
	Profile      string                       `json:"profile"`
	RoleModels   *RoleModels                  `json:"role_models,omitempty"`
	Profiles     map[string]ProfileDefinition `json:"profiles,omitempty"`
	GoalTemplate *GoalInput                   `json:"goal_template,omitempty"`
}

type EffectiveConfig struct {
	Global       GlobalConfig
	Project      ProjectConfig
	Profile      ProfileDefinition
	RoleModels   RoleModels
	RunOutputDir string
	Provider     ProviderSettings
}

type ExecutionResult struct {
	Command    []string      `json:"command"`
	ExitCode   int           `json:"exit_code"`
	Stdout     string        `json:"stdout"`
	Stderr     string        `json:"stderr"`
	Duration   time.Duration `json:"duration"`
	WorkingDir string        `json:"working_dir"`
}

type PlannedChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

type PlanDoc struct {
	Summary string          `json:"summary"`
	Changes []PlannedChange `json:"changes"`
	Notes   []string        `json:"notes,omitempty"`
}

type Edit struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Content string `json:"content,omitempty"`
}

type OptimizationDoc struct {
	Summary string `json:"summary"`
	Edits   []Edit `json:"edits"`
}

type FlexibleString string

func (s *FlexibleString) UnmarshalJSON(data []byte) error {
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*s = ""
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = FlexibleString(text)
		return nil
	}

	var texts []string
	if err := json.Unmarshal(data, &texts); err == nil {
		*s = FlexibleString(strings.Join(texts, "\n"))
		return nil
	}

	var values []any
	if err := json.Unmarshal(data, &values); err == nil {
		parts := make([]string, 0, len(values))
		for _, value := range values {
			parts = append(parts, fmt.Sprint(value))
		}
		*s = FlexibleString(strings.Join(parts, "\n"))
		return nil
	}

	return fmt.Errorf("unsupported string value: %s", string(data))
}

func (s FlexibleString) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}

type JudgmentDoc struct {
	Summary         string         `json:"summary"`
	Findings        []string       `json:"findings"`
	Risks           []string       `json:"risks"`
	Recommendation  FlexibleString `json:"recommendation"`
	RequireRevision bool           `json:"require_revision"`
	Accepted        bool           `json:"accepted"`
	SuggestedGoal   *GoalInput     `json:"suggested_goal,omitempty"`
}

type FinalDecision struct {
	Accept bool   `json:"accept"`
	Reason string `json:"reason"`
}

type TraceEvent struct {
	Stage     string            `json:"stage"`
	Model     string            `json:"model,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Fields    map[string]string `json:"fields,omitempty"`
}

type StageRecord struct {
	Stage     string            `json:"stage"`
	Model     string            `json:"model,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Fields    map[string]string `json:"fields,omitempty"`
}

type RunContext struct {
	RunID       string        `json:"run_id"`
	ProjectRoot string        `json:"project_root"`
	Profile     string        `json:"profile"`
	Goal        GoalInput     `json:"goal"`
	DryRun      bool          `json:"dry_run"`
	RunDir      string        `json:"run_dir"`
	Workspace   string        `json:"workspace"`
	Provider    string        `json:"provider"`
	RoleModels  RoleModels    `json:"role_models"`
	StartedAt   time.Time     `json:"started_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Stages      []StageRecord `json:"stages,omitempty"`
}

type RunSummary struct {
	RunID       string    `json:"run_id"`
	ProjectRoot string    `json:"project_root"`
	Profile     string    `json:"profile"`
	Goal        GoalInput `json:"goal"`
	Accepted    bool      `json:"accepted"`
	Workspace   string    `json:"workspace"`
	RunDir      string    `json:"run_dir"`
}

func bytesTrimSpace(data []byte) []byte {
	start := 0
	for start < len(data) && isSpace(data[start]) {
		start++
	}
	end := len(data)
	for end > start && isSpace(data[end-1]) {
		end--
	}
	return data[start:end]
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t':
		return true
	default:
		return false
	}
}
