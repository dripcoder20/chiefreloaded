package config

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
	Git    GitConfig `yaml:"git"`
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
}

// PerStory reports whether per-story stacked branches are enabled.
//
// Value receiver: it is a pure read, and callers routinely have a LoopConfig
// returned by value, which a pointer method cannot be called on.
func (c LoopConfig) PerStory() bool { return c.Git.Mode == GitModePerStory }
