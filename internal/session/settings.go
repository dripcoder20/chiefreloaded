package session

import "github.com/dripcoder/loop/internal/chief/config"

// Settings is the project configuration as the UI sees it.
//
// It exists rather than exposing config.LoopConfig directly because chief's
// structs carry only YAML tags. Without JSON tags the binding generator falls
// back to Go field names, so TypeScript would see `Worktree` and `Agent` beside
// our own lowercase keys — an inconsistency that would then be baked into every
// component that touches configuration.
//
// The indirection also means the vendored struct can be reshaped by an upstream
// sync without breaking the frontend: only the two conversions below move.
type Settings struct {
	Git      GitSettings      `json:"git"`
	Agent    AgentSettings    `json:"agent"`
	Worktree WorktreeSettings `json:"worktree"`
	// OnComplete is chief's per-PRD push and pull-request automation. Honoured
	// only when Git.Mode is "per-prd"; per-story supersedes it.
	OnComplete OnCompleteSettings `json:"onComplete"`
	// Usage is the thresholds for approaching-limit and unusual-spend warnings.
	Usage UsageSettings `json:"usage"`
}

// UsageSettings is the UI-facing shape of the usage-warning thresholds. Percents
// are 0–100; CostWarnAmount is optional (nil = no per-session cost warning).
type UsageSettings struct {
	ContextWarnPercent     float64  `json:"contextWarnPercent"`
	ContextCriticalPercent float64  `json:"contextCriticalPercent"`
	CostWarnAmount         *float64 `json:"costWarnAmount,omitempty"`
}

// GitSettings is Loop's branch and pull-request behaviour.
type GitSettings struct {
	// Mode is "off", "per-prd" or "per-story".
	Mode string `json:"mode"`
	// StackDriver is "auto", "gh-stack" or "manual".
	StackDriver string `json:"stackDriver"`
	// BaseBranch is the trunk the bottom of a stack targets. Empty means the
	// repository's default branch.
	BaseBranch string `json:"baseBranch"`
	// BranchTemplate supports {prd}, {story} and {slug}.
	BranchTemplate string `json:"branchTemplate"`
	Draft          bool   `json:"draft"`
	// RequireWorktree refuses per-story mode outside a worktree.
	RequireWorktree bool `json:"requireWorktree"`
	VerifyCommit    bool `json:"verifyCommit"`
}

// AgentSettings selects the coding agent CLI.
type AgentSettings struct {
	Provider string `json:"provider"`
	CLIPath  string `json:"cliPath"`
}

// WorktreeSettings configures newly created worktrees.
type WorktreeSettings struct {
	// Setup runs once after a worktree is created. Must be idempotent.
	Setup string `json:"setup"`
}

// OnCompleteSettings is chief's legacy per-PRD automation.
type OnCompleteSettings struct {
	Push     bool `json:"push"`
	CreatePR bool `json:"createPR"`
}

func settingsFrom(c config.LoopConfig) Settings {
	return Settings{
		Git: GitSettings{
			Mode:            string(c.Git.Mode),
			StackDriver:     string(c.Git.StackDriver),
			BaseBranch:      c.Git.BaseBranch,
			BranchTemplate:  c.Git.BranchTemplate,
			Draft:           c.Git.Draft,
			RequireWorktree: c.Git.RequireWorktree,
			VerifyCommit:    c.Git.VerifyCommit,
		},
		Agent:      AgentSettings{Provider: c.Agent.Provider, CLIPath: c.Agent.CLIPath},
		Worktree:   WorktreeSettings{Setup: c.Worktree.Setup},
		OnComplete: OnCompleteSettings{Push: c.OnComplete.Push, CreatePR: c.OnComplete.CreatePR},
		Usage: UsageSettings{
			ContextWarnPercent:     c.Usage.ContextWarnPercent,
			ContextCriticalPercent: c.Usage.ContextCriticalPercent,
			CostWarnAmount:         c.Usage.CostWarnAmount,
		},
	}
}

func (s Settings) toConfig() config.LoopConfig {
	c := config.DefaultLoop()
	c.Git.Mode = config.GitMode(s.Git.Mode)
	c.Git.StackDriver = config.StackDriver(s.Git.StackDriver)
	c.Git.BaseBranch = s.Git.BaseBranch
	c.Git.BranchTemplate = s.Git.BranchTemplate
	c.Git.Draft = s.Git.Draft
	c.Git.RequireWorktree = s.Git.RequireWorktree
	c.Git.VerifyCommit = s.Git.VerifyCommit
	c.Agent.Provider = s.Agent.Provider
	c.Agent.CLIPath = s.Agent.CLIPath
	c.Worktree.Setup = s.Worktree.Setup
	c.OnComplete.Push = s.OnComplete.Push
	c.OnComplete.CreatePR = s.OnComplete.CreatePR
	c.Usage.ContextWarnPercent = s.Usage.ContextWarnPercent
	c.Usage.ContextCriticalPercent = s.Usage.ContextCriticalPercent
	c.Usage.CostWarnAmount = s.Usage.CostWarnAmount
	// Normalise here so an invalid value from the UI cannot reach disk and put
	// the project into a state the next load has to guess its way out of.
	c.Normalise()
	return *c
}

// Settings returns the project configuration.
func (s *Session) Settings() Settings { return settingsFrom(s.LoopConfig()) }

// SaveSettings writes the project configuration.
func (s *Session) SaveSettings(v Settings) error { return s.SaveLoopConfig(v.toConfig()) }
