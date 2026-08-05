package config

import "fmt"

// This file is Loop's, not chief's. It lives in the vendored package so Loop's
// settings ride in the same .chief/config.yaml the chief TUI reads.
//
// That sharing is deliberate and load-bearing. Users will keep running both
// tools against the same project, and chief unmarshals into a struct without
// KnownFields, so it ignores keys it does not recognise. Adding a `git:` block
// here is therefore invisible to chief while being first-class to us. Putting it
// in a separate file would have meant two config files describing one project.

// GitMode selects how Loop manages branches and pull requests for a run.
type GitMode string

const (
	// GitModeOff leaves git entirely alone: the agent commits, nothing else
	// happens. Whatever branch you are on is where the work lands.
	GitModeOff GitMode = "off"
	// GitModePerPRD is chief's behaviour — one branch for the whole PRD, with
	// an optional push and pull request once every story is done.
	GitModePerPRD GitMode = "per-prd"
	// GitModePerStory gives every story its own branch stacked on the previous
	// one, pushed with a draft PR the moment the story completes.
	GitModePerStory GitMode = "per-story"
)

// StackDriver selects how stacked pull requests are created.
type StackDriver string

const (
	// StackAuto uses gh-stack when it is installed and falls back to manual.
	StackAuto StackDriver = "auto"
	// StackGH drives the `gh stack` extension, producing a native GitHub Stack.
	StackGH StackDriver = "gh-stack"
	// StackManual chains PRs by hand with `gh pr create --draft --base <prev>`.
	// Same branch topology, no native stack object.
	StackManual StackDriver = "manual"
)

// GitConfig is Loop's per-project git behaviour.
type GitConfig struct {
	Mode        GitMode     `yaml:"mode"`
	StackDriver StackDriver `yaml:"stackDriver"`
	// BaseBranch is the trunk the bottom of the stack targets. Empty means the
	// repository's default branch.
	BaseBranch string `yaml:"baseBranch"`
	// BranchTemplate supports {prd}, {story} and {slug}.
	BranchTemplate string `yaml:"branchTemplate"`
	// Draft opens pull requests as drafts.
	Draft bool `yaml:"draft"`
	// RequireWorktree refuses per-story mode outside a git worktree. On by
	// default: switching branches per story inside the project root would yank
	// the user's own checkout out from under them mid-run.
	RequireWorktree bool `yaml:"requireWorktree"`
	// VerifyCommit checks the agent actually committed before marking a story
	// done and opening its pull request.
	VerifyCommit bool `yaml:"verifyCommit"`
}

// UsageConfig holds the thresholds Loop uses to warn about an agent approaching a
// context-window limit or exceeding an expected per-session cost. The warnings are
// purely informational — they never pause or stop a run — so these values only
// change what the UI highlights, never how a run behaves.
type UsageConfig struct {
	// ContextWarnPercent is the context-window utilization (0–100) at which the UI
	// shows a warning. ContextCriticalPercent is the higher point at which it shows
	// a critical state. Critical must be strictly greater than warning.
	ContextWarnPercent     float64 `yaml:"contextWarnPercent"`
	ContextCriticalPercent float64 `yaml:"contextCriticalPercent"`
	// CostWarnAmount is an optional per-session cost (in the run's reported
	// currency) above which the UI warns. Nil means no cost warning is configured.
	CostWarnAmount *float64 `yaml:"costWarnAmount,omitempty"`
}

// Default context-warning thresholds. 80% gives room to react before a limit and
// 95% flags that a limit is imminent.
const (
	DefaultContextWarnPercent     = 80
	DefaultContextCriticalPercent = 95
)

// DefaultBranchTemplate keeps every Loop branch under one prefix, namespaced by
// PRD so two PRDs cannot collide, and ending in a slug so `git branch` is
// readable.
const DefaultBranchTemplate = "loop/{prd}/{story}-{slug}"

// LoopConfig is Config plus Loop's own keys.
//
// It embeds Config rather than replacing it so chief's fields keep their exact
// YAML shape and a round-trip through Loop does not rewrite a user's file into
// something chief no longer understands.
type LoopConfig struct {
	Config `yaml:",inline"`
	Git    GitConfig    `yaml:"git"`
	Usage  UsageConfig  `yaml:"usage"`
	Agents AgentsConfig `yaml:"agents"`
}

// AgentsConfig holds the per-phase agent defaults.
//
// chief has one agent setting because it has one phase. Loop writes PRDs
// conversationally and then implements them, and the best agent for a long
// interactive conversation is not necessarily the best one for a long unattended
// run. Either field may be empty, which means "fall back to agent.provider" —
// that fallback is deliberate, so a project that has only ever set one agent
// keeps working and both phases agree until the user says otherwise.
type AgentsConfig struct {
	Authoring      string `yaml:"authoring,omitempty"`
	Implementation string `yaml:"implementation,omitempty"`
}

// AuthoringProvider resolves the agent for PRD authoring, falling back to the
// general default.
func (c *LoopConfig) AuthoringProvider() string {
	if c.Agents.Authoring != "" {
		return c.Agents.Authoring
	}
	return c.Agent.Provider
}

// ImplementationProvider resolves the agent for implementation runs, falling
// back to the general default.
func (c *LoopConfig) ImplementationProvider() string {
	if c.Agents.Implementation != "" {
		return c.Agents.Implementation
	}
	return c.Agent.Provider
}

// DefaultLoop returns the default Loop configuration.
//
// The default mode is per-prd, not per-story. Per-story is the headline feature,
// but silently turning one PRD into nine pull requests for someone who just
// installed the app would be a rude surprise; it is opt-in.
func DefaultLoop() *LoopConfig {
	return &LoopConfig{
		Config: *Default(),
		Git: GitConfig{
			Mode:            GitModePerPRD,
			StackDriver:     StackAuto,
			BranchTemplate:  DefaultBranchTemplate,
			Draft:           true,
			RequireWorktree: true,
			VerifyCommit:    true,
		},
		Usage: UsageConfig{
			ContextWarnPercent:     DefaultContextWarnPercent,
			ContextCriticalPercent: DefaultContextCriticalPercent,
		},
	}
}

// LoadLoop reads .chief/config.yaml including Loop's keys.
//
// A file written by chief has no `git:` block. Rather than treat that as "all
// defaults", it is migrated: mode becomes per-prd and the legacy onComplete
// push/createPR flags are honoured, so an existing project behaves exactly as it
// did before Loop was pointed at it.
func LoadLoop(baseDir string) (*LoopConfig, error) {
	base, err := Load(baseDir)
	if err != nil {
		return nil, err
	}

	cfg := DefaultLoop()
	cfg.Config = *base

	raw, err := loadRaw(baseDir)
	if err != nil {
		return nil, err
	}
	if raw != nil && raw.Git != nil {
		cfg.Git = *raw.Git
	}
	if raw != nil && raw.Usage != nil {
		cfg.Usage = *raw.Usage
	}
	if raw != nil && raw.Agents != nil {
		cfg.Agents = *raw.Agents
	}
	cfg.Normalise()
	return cfg, nil
}

// SaveLoop writes the config, Loop's keys included.
func SaveLoop(baseDir string, cfg *LoopConfig) error {
	c := *cfg
	c.Normalise()
	return saveRaw(baseDir, &c)
}

// Normalise fills in anything empty and clamps invalid combinations, so callers
// never have to reason about a half-configured struct.
func (c *LoopConfig) Normalise() {
	switch c.Git.Mode {
	case GitModeOff, GitModePerPRD, GitModePerStory:
	default:
		c.Git.Mode = GitModePerPRD
	}
	switch c.Git.StackDriver {
	case StackAuto, StackGH, StackManual:
	default:
		c.Git.StackDriver = StackAuto
	}
	if c.Git.BranchTemplate == "" {
		c.Git.BranchTemplate = DefaultBranchTemplate
	}
	// A zero percentage is an unset field (a legacy config, or a partially written
	// usage block), not a deliberate 0 — 0% would warn from the first token. Fill
	// it with the default rather than reject it, reserving validation for values
	// the user actually chose.
	if c.Usage.ContextWarnPercent == 0 {
		c.Usage.ContextWarnPercent = DefaultContextWarnPercent
	}
	if c.Usage.ContextCriticalPercent == 0 {
		c.Usage.ContextCriticalPercent = DefaultContextCriticalPercent
	}
}

// Validate reports the first invalid setting, so a bad value from the UI is
// refused rather than silently clamped into something the user did not ask for.
// Call it after Normalise, which has already filled unset fields with defaults.
func (c *LoopConfig) Validate() error {
	return c.Usage.validate()
}

// validate enforces the usage-warning invariants: both percentages sit within
// (0, 100], the critical threshold is strictly above the warning one, and the
// optional cost warning is non-negative.
func (u UsageConfig) validate() error {
	if u.ContextWarnPercent <= 0 || u.ContextWarnPercent >= 100 {
		return fmt.Errorf("context warning percent must be between 0 and 100, got %g", u.ContextWarnPercent)
	}
	if u.ContextCriticalPercent <= 0 || u.ContextCriticalPercent > 100 {
		return fmt.Errorf("context critical percent must be between 0 and 100, got %g", u.ContextCriticalPercent)
	}
	if u.ContextCriticalPercent <= u.ContextWarnPercent {
		return fmt.Errorf("context critical percent (%g) must be greater than warning percent (%g)", u.ContextCriticalPercent, u.ContextWarnPercent)
	}
	if u.CostWarnAmount != nil && *u.CostWarnAmount < 0 {
		return fmt.Errorf("cost warning amount cannot be negative, got %g", *u.CostWarnAmount)
	}
	return nil
}

// PerStory reports whether per-story stacked branches are enabled.
//
// Value receiver: it is a pure read, and callers routinely have a LoopConfig
// returned by value, which a pointer method cannot be called on.
func (c LoopConfig) PerStory() bool { return c.Git.Mode == GitModePerStory }
