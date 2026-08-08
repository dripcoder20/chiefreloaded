package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dripcoder/loop/internal/chief/config"
)

// Branch names must be a pure function of (PRD, story, title). That is what lets
// loop-state.json be a cache that can be deleted and rebuilt from git, rather
// than a source of truth whose loss orphans every branch.
func TestBranchNameIsDeterministic(t *testing.T) {
	const tmpl = config.DefaultBranchTemplate

	first := branchName(tmpl, "checkout", "US-003", "Add Stripe webhook")
	second := branchName(tmpl, "checkout", "US-003", "Add Stripe webhook")
	if first != second {
		t.Fatalf("same inputs gave %q then %q", first, second)
	}
	if want := "loop/checkout/us-003-add-stripe-webhook"; first != want {
		t.Errorf("branch = %q, want %q", first, want)
	}
}

func TestBranchNamesAreGitSafe(t *testing.T) {
	cases := []struct{ name, prd, story, title string }{
		{"punctuation", "checkout", "US-1", "Fix: the thing (again)!"},
		{"slashes", "checkout", "US-2", "handle a/b/c paths"},
		{"unicode", "checkout", "US-3", "Café — naïve résumé"},
		{"leading junk", "checkout", "US-4", "  ...leading dots"},
		{"empty title", "checkout", "US-5", ""},
		{"very long", "checkout", "US-6", strings.Repeat("long title ", 20)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := branchName(config.DefaultBranchTemplate, tc.prd, tc.story, tc.title)

			// git check-ref-format is the authority; asserting against it beats
			// guessing which characters are legal.
			if err := gitRun(context.Background(), t.TempDir(), "check-ref-format", "--branch", got); err != nil {
				t.Fatalf("git rejects branch %q: %v", got, err)
			}
			if strings.Contains(got, "//") {
				t.Errorf("branch %q contains an empty path component", got)
			}
			if strings.ContainsAny(got, " \t") {
				t.Errorf("branch %q contains whitespace", got)
			}
		})
	}
}

// Two PRDs may well have a US-001. The PRD name in the path is what keeps them
// apart.
func TestBranchNamesDoNotCollideAcrossPRDs(t *testing.T) {
	a := branchName(config.DefaultBranchTemplate, "checkout", "US-001", "Same title")
	b := branchName(config.DefaultBranchTemplate, "billing", "US-001", "Same title")
	if a == b {
		t.Errorf("PRDs collide on %q", a)
	}
}

func TestSlugifyTruncatesOnLength(t *testing.T) {
	got := slugify(strings.Repeat("alpha ", 40))
	if len(got) > maxSlugLength {
		t.Errorf("slug is %d chars, want at most %d", len(got), maxSlugLength)
	}
	if strings.HasSuffix(got, "-") {
		t.Errorf("slug %q ends in a hyphen", got)
	}
}

// One failed push must not cascade: every later PR would otherwise be created
// against a base that does not exist on the remote.
func TestResolveRemoteBaseFallsBackToTrunk(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	st := &stackState{
		cfg:   config.GitConfig{BaseBranch: "main"},
		trunk: "main",
		bases: map[string]string{},
	}

	// No remote at all, so no branch can be found on it.
	base, deviated := st.resolveRemoteBase(context.Background(), root, "loop/checkout/us-001-first")
	if !deviated {
		t.Error("expected a deviation when the base is missing from the remote")
	}
	if base != "main" {
		t.Errorf("base = %q, want the trunk", base)
	}
}

func TestResolveRemoteBaseLeavesTrunkAlone(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	st := &stackState{cfg: config.GitConfig{BaseBranch: "main"}, trunk: "main"}
	base, deviated := st.resolveRemoteBase(context.Background(), root, "main")
	if deviated || base != "main" {
		t.Errorf("trunk should never be reported as deviated; got %q deviated=%v", base, deviated)
	}
}

// ------------------------------------------------------------------ config --

// A config written by chief has no git block. Reading it must reproduce chief's
// behaviour exactly, not silently switch the project to per-story mode.
func TestConfigWithoutGitBlockMigratesToPerPRD(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "worktree:\n  setup: npm ci\nonComplete:\n  push: true\n  createPR: true\n")

	cfg, err := config.LoadLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.Mode != config.GitModePerPRD {
		t.Errorf("mode = %q, want per-prd for a chief-written config", cfg.Git.Mode)
	}
	if cfg.Worktree.Setup != "npm ci" {
		t.Errorf("chief's own keys were lost: %+v", cfg.Config)
	}
	if !cfg.OnComplete.Push {
		t.Error("legacy onComplete.push was dropped")
	}
}

// The file is shared with the chief TUI. Round-tripping through Loop must not
// destroy keys chief cares about, or a user running both tools loses settings.
func TestSavingPreservesChiefsKeys(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "worktree:\n  setup: make bootstrap\nagent:\n  provider: codex\n")

	cfg, err := config.LoadLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Git.Mode = config.GitModePerStory
	if err := config.SaveLoop(root, cfg); err != nil {
		t.Fatal(err)
	}

	// Reload through chief's own loader, which is what the TUI would use.
	back, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if back.Worktree.Setup != "make bootstrap" {
		t.Errorf("worktree.setup = %q, want it preserved", back.Worktree.Setup)
	}
	if back.Agent.Provider != "codex" {
		t.Errorf("agent.provider = %q, want it preserved", back.Agent.Provider)
	}

	loop, err := config.LoadLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if loop.Git.Mode != config.GitModePerStory {
		t.Errorf("git.mode = %q, want per-story to survive the round trip", loop.Git.Mode)
	}
}

// Per-story turns one PRD into N pull requests. Doing that to someone who just
// installed the app would be a rude surprise.
func TestPerStoryIsOptIn(t *testing.T) {
	if config.DefaultLoop().PerStory() {
		t.Error("per-story mode must not be the default")
	}
	if !config.DefaultLoop().Git.Draft {
		t.Error("pull requests should default to draft")
	}
	if !config.DefaultLoop().Git.RequireWorktree {
		t.Error("per-story mode should require a worktree by default")
	}
}

func TestNormaliseRepairsInvalidValues(t *testing.T) {
	cfg := &config.LoopConfig{Git: config.GitConfig{Mode: "nonsense", StackDriver: "nope"}}
	cfg.Normalise()

	if cfg.Git.Mode != config.GitModePerPRD {
		t.Errorf("mode = %q, want the safe default", cfg.Git.Mode)
	}
	if cfg.Git.StackDriver != config.StackAuto {
		t.Errorf("stackDriver = %q, want auto", cfg.Git.StackDriver)
	}
	if cfg.Git.BranchTemplate == "" {
		t.Error("an empty branch template must be filled in")
	}
}

// Stacking is skipped entirely when the mode is off, so nothing touches git.
func TestStackIsSkippedWhenModeIsOff(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if s.LoopConfig().PerStory() {
		t.Fatal("expected per-story to be off by default")
	}

	r := &run{prdName: "main", prdPath: s.PRDs()[0].Path, workDir: root, sess: s}
	err := s.stackAfterStory(r, storyDone{
		ID: "US-001", Title: "First story", Check: CommitCheck{Verdict: VerdictCommitted},
	})
	if err != nil {
		t.Errorf("stacking should be a no-op when disabled, got %v", err)
	}
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".chief"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".chief", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
