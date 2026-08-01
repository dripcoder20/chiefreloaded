package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Tests run against real directories and, where git is involved, real
// repositories. Faking the filesystem or git here would produce tests that keep
// passing while the product is broken, and git is cheap.

func writePRD(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, ".chief", "prds", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "prd.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const samplePRD = `# Checkout Rework

## Overview
Replace the legacy checkout flow.

## User Stories

### US-001: Add Stripe webhook
**Status:** done
**Priority:** 1
**Description:** Receive and verify Stripe events.
- [x] Signature is verified
- [x] Replays are rejected

### US-002: Persist orders
**Status:** in-progress
**Priority:** 2
**Description:** Write orders to Postgres.
- [ ] Orders survive a restart

### US-003: Send receipts
**Status:** todo
**Priority:** 3
- [ ] Receipt email is sent
`

func TestOpenProjectOnPlainDirectory(t *testing.T) {
	root := t.TempDir()

	// A directory with no .chief/ is the first-run case, not an error. Treating
	// it as one would force every caller to distinguish "new" from "broken".
	p, err := openProject(root)
	if err != nil {
		t.Fatalf("openProject on an empty directory: %v", err)
	}
	if p.HasChiefDir {
		t.Error("HasChiefDir should be false with no .chief/")
	}
	if p.IsGitRepo {
		t.Error("IsGitRepo should be false outside a repository")
	}
	if p.Root != root {
		t.Errorf("Root = %q, want %q", p.Root, root)
	}
}

func TestOpenProjectRejectsNonDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openProject(f); err == nil {
		t.Error("expected an error when opening a file as a project")
	}
}

func TestOpenProjectReadsGitState(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	p, err := openProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsGitRepo {
		t.Fatal("IsGitRepo should be true inside a repository")
	}
	if p.Branch == "" {
		t.Error("Branch should be populated after an initial commit")
	}
	if p.ChiefIgnored {
		t.Error("ChiefIgnored should be false before .chief is added to .gitignore")
	}
}

func TestDiscoverPRDsCountsStoriesByStatus(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "checkout", samplePRD)

	prds := discoverPRDs(root)
	if len(prds) != 1 {
		t.Fatalf("found %d PRDs, want 1", len(prds))
	}

	got := prds[0]
	if got.Name != "checkout" {
		t.Errorf("Name = %q, want %q", got.Name, "checkout")
	}
	if got.Title != "Checkout Rework" {
		t.Errorf("Title = %q, want the PRD's project heading", got.Title)
	}
	if got.Total != 3 || got.Completed != 1 || got.InProgress != 1 {
		t.Errorf("counts = total %d / done %d / in-progress %d, want 3 / 1 / 1",
			got.Total, got.Completed, got.InProgress)
	}
}

func TestDiscoverPRDsSortsByName(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"zebra", "alpha", "middle"} {
		writePRD(t, root, n, samplePRD)
	}

	prds := discoverPRDs(root)
	want := []string{"alpha", "middle", "zebra"}
	if len(prds) != len(want) {
		t.Fatalf("found %d PRDs, want %d", len(prds), len(want))
	}
	for i, w := range want {
		if prds[i].Name != w {
			t.Errorf("position %d = %q, want %q", i, prds[i].Name, w)
		}
	}
}

// Users who have run chief since before 0.7 have a PRD at .chief/prd.md.
// Ignoring it would look like data loss.
func TestDiscoverPRDsFindsLegacyLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".chief"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".chief", "prd.md"), []byte(samplePRD), 0o644); err != nil {
		t.Fatal(err)
	}

	prds := discoverPRDs(root)
	if len(prds) != 1 {
		t.Fatalf("found %d PRDs, want the legacy one", len(prds))
	}
	if prds[0].Name != "main" {
		t.Errorf("legacy PRD name = %q, want %q", prds[0].Name, "main")
	}
	if !prds[0].Legacy {
		t.Error("legacy PRD should be flagged so the UI can offer to migrate it")
	}
}

// A migrated project has both files. Listing the PRD twice would be worse than
// either behaviour on its own.
func TestLegacyPRDIsHiddenWhenMainExists(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "main", samplePRD)
	if err := os.WriteFile(filepath.Join(root, ".chief", "prd.md"), []byte(samplePRD), 0o644); err != nil {
		t.Fatal(err)
	}

	prds := discoverPRDs(root)
	if len(prds) != 1 {
		t.Fatalf("found %d PRDs, want 1 — the legacy file must not double up with main", len(prds))
	}
	if prds[0].Legacy {
		t.Error("the current-layout main should win over the legacy file")
	}
}

// Hiding a broken PRD would leave the user no way to open and repair it.
func TestUnparseablePRDIsListedWithAnError(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".chief", "prds", "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory where prd.md should be: readable as an entry, not as a file.
	if err := os.MkdirAll(filepath.Join(dir, "prd.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	prds := discoverPRDs(root)
	if len(prds) != 1 {
		t.Fatalf("found %d PRDs, want the broken one to still be listed", len(prds))
	}
	if prds[0].ParseError == "" {
		t.Error("a PRD that cannot be read must carry a ParseError")
	}
}

func TestLoadPRDDetailReturnsStories(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "checkout", samplePRD)

	detail, err := loadPRDDetail(root, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Stories) != 3 {
		t.Fatalf("got %d stories, want 3", len(detail.Stories))
	}

	want := []Status{StatusDone, StatusInProgress, StatusTodo}
	for i, w := range want {
		if detail.Stories[i].Status != w {
			t.Errorf("story %d status = %q, want %q", i, detail.Stories[i].Status, w)
		}
	}
}

// chief's SetStoryStatus(id, "done") ticks every acceptance-criteria box as a
// side effect. A completed story's checklist therefore records that write and
// nothing else, and the UI must not present it as verified.
func TestCompletedStoryCriteriaAreMarkedUnreliable(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "checkout", samplePRD)

	detail, err := loadPRDDetail(root, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range detail.Stories {
		want := s.Status != StatusDone
		if s.CriteriaAreAuthoritative != want {
			t.Errorf("story %s (%s): CriteriaAreAuthoritative = %v, want %v",
				s.ID, s.Status, s.CriteriaAreAuthoritative, want)
		}
	}
}

func TestLoadPRDDetailUnknownName(t *testing.T) {
	if _, err := loadPRDDetail(t.TempDir(), "nope"); err == nil {
		t.Error("expected an error for a PRD that does not exist")
	}
}

func TestSessionRequiresAnOpenProject(t *testing.T) {
	s := newTestSession(t)

	if _, err := s.PRD("anything"); err == nil {
		t.Error("PRD should fail with no project open")
	}
	if err := s.Rescan(context.Background()); err == nil {
		t.Error("Rescan should fail with no project open")
	}
	if s.Project() != nil {
		t.Error("Project should be nil before OpenProject")
	}
}

func TestOpenProjectPublishesAndPopulates(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "checkout", samplePRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	if got := s.PRDs(); len(got) != 1 || got[0].Name != "checkout" {
		t.Errorf("PRDs() = %+v, want the checkout PRD", got)
	}

	snap := s.Snapshot()
	if snap.Project == nil || snap.Project.Root != root {
		t.Error("snapshot should carry the opened project")
	}
	if snap.Seq == 0 {
		t.Error("snapshot Seq should advance once events have been published")
	}

	assertPublished(t, s, EvProjectOpened)
}

// Opening a second project must not leave the first one's PRDs behind.
func TestOpenProjectReplacesPreviousState(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writePRD(t, first, "one", samplePRD)
	writePRD(t, second, "two", samplePRD)

	s := newTestSession(t)
	ctx := context.Background()
	if _, err := s.OpenProject(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProject(ctx, second); err != nil {
		t.Fatal(err)
	}

	got := s.PRDs()
	if len(got) != 1 || got[0].Name != "two" {
		t.Errorf("PRDs() = %+v, want only the second project's PRD", got)
	}
}

func TestSaveConfigRoundTrips(t *testing.T) {
	root := t.TempDir()
	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	cfg := s.Config()
	cfg.Worktree.Setup = "npm ci"
	cfg.Agent.Provider = "codex"
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if got := s.Config(); got.Worktree.Setup != "npm ci" || got.Agent.Provider != "codex" {
		t.Errorf("in-memory config not updated: %+v", got)
	}

	// Re-open to prove it reached disk, not just the struct.
	fresh := newTestSession(t)
	if _, err := fresh.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got := fresh.Config(); got.Worktree.Setup != "npm ci" {
		t.Errorf("config did not persist to .chief/config.yaml: %+v", got)
	}
}

func TestProgressIsKeyedByStory(t *testing.T) {
	root := t.TempDir()
	path := writePRD(t, root, "checkout", samplePRD)

	journal := "## 2026-08-01 - US-001\nVerified the webhook signature.\n\n---\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "progress.md"), []byte(journal), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	got, err := s.Progress("checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(got["US-001"]) == 0 {
		t.Fatalf("no progress entries for US-001, got %+v", got)
	}
}

// ------------------------------------------------------------------ helpers --

func newTestSession(t *testing.T) *Session {
	t.Helper()
	s, err := New(Options{
		Clock: fixedClock(),
		// Stub the probe: the real one shells out to git, gh and every agent CLI,
		// which costs about a second per OpenProject and makes the suite depend on
		// what happens to be installed on the machine running it.
		Probe: func(context.Context) Environment {
			return Environment{
				Git:       Tool{Name: "git", Available: true},
				GH:        Tool{Name: "gh", Available: true},
				GHAuth:    true,
				GHStack:   Tool{Name: "gh-stack", Available: true},
				StackMode: "gh-stack",
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AutoAnswer(true)
	// stop, not Close: these tests do not drain the channel, and a graceful close
	// leaves the pump alive waiting for a reader that never arrives.
	t.Cleanup(func() { s.bus.stop() })
	return s
}

// assertPublished checks that kind reached the retained event history.
func assertPublished(t *testing.T, s *Session, kind EventKind) {
	t.Helper()
	evs, _ := s.Replay(0)
	for _, ev := range evs {
		if ev.Kind == kind {
			return
		}
	}
	t.Errorf("no %s event was published", kind)
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
