package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// stackPRD has three stories, which is the smallest PRD that can show a story in
// the middle of a stack being skipped.
const stackPRD = `# Demo Project

## User Stories

### US-001: First story
**Status:** todo
**Priority:** 1
**Description:** Do the first thing.
- [ ] It works

### US-002: Second story
**Status:** todo
**Priority:** 2
**Description:** Do the second thing.
- [ ] It also works

### US-003: Third story
**Status:** todo
**Priority:** 3
**Description:** Do the third thing.
- [ ] It works too
`

// commitsAndFinishes is an agent that does what it is asked: one commit, then the
// completion sentinel.
func commitsAndFinishes(name string) fakeagent.Behaviour {
	return fakeagent.Behaviour{WriteFile: name, FileBody: name, Commit: true, Done: true}
}

// startPerStoryRun starts a run that gives each story its own branch.
//
// The stack driver is pinned to manual so nothing depends on whether the
// `gh stack` extension happens to be installed on the machine running the tests;
// it is also the driver a user without the extension gets, so pinning it is
// coverage rather than a compromise.
func startPerStoryRun(t *testing.T, behaviours ...fakeagent.Behaviour) (*Session, string, string) {
	t.Helper()

	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", stackPRD)

	s := newTestSessionWith(t, fakeagent.New(behaviours...))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	cfg := s.LoopConfig()
	cfg.Git.StackDriver = config.StackManual
	if err := s.SaveLoopConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.recordLayout("main", LayoutBranchPerStory); err != nil {
		t.Fatal(err)
	}

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return s, root, runID
}

// freshSession opens the same project again through a session that performed no
// run. Everything it can say about the branches came off the disk, which is the
// property publishing depends on.
func freshSession(t *testing.T, root string) *Session {
	t.Helper()
	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	return s
}

// recordedBranches reads a PRD's branch record through a session that did not
// perform the run.
func recordedBranches(t *testing.T, root, prd string) []StoryBranch {
	t.Helper()
	state, err := freshSession(t, root).PRDGitFor(prd)
	if err != nil {
		t.Fatalf("reading the record in a fresh session: %v", err)
	}
	return state.StoryBranches()
}

// storyBranch is the branch a story is expected to have been given.
func storyBranch(storyID, title string) string {
	return branchName(config.DefaultBranchTemplate, "main", storyID, title)
}

// waitForRecordedBranches waits until a run has written n branches, so a test can
// interrupt it at a known point rather than at a guessed moment.
func waitForRecordedBranches(t *testing.T, s *Session, prd string, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, err := s.PRDGitFor(prd)
		if err == nil && len(state.StoryBranches()) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never recorded %d branch(es)", prd, n)
}

// The record has to say what each story's branch was cut from, in the order the
// run cut them: that ordering is the only thing that says what "below" means once
// the run's in-memory stack is gone.
func TestARunRecordsEachStoryBranchWithItsBase(t *testing.T) {
	s, root, runID := startPerStoryRun(t,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	if snap := waitFor(t, s, runID); snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}

	first := storyBranch("US-001", "First story")
	second := storyBranch("US-002", "Second story")
	third := storyBranch("US-003", "Third story")

	want := []StoryBranch{
		{StoryID: "US-001", Branch: first, Base: "main"},
		{StoryID: "US-002", Branch: second, Base: first},
		{StoryID: "US-003", Branch: third, Base: second},
	}
	got := recordedBranches(t, root, "main")
	if len(got) != len(want) {
		t.Fatalf("recorded %+v, want one entry per story", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("branch %d = %+v, want %+v", i, got[i], w)
		}
	}

	// A record naming a branch that was never created would be worse than no
	// record at all, so it is checked against git rather than against itself.
	for _, b := range got {
		if exists, _ := branchExists(context.Background(), root, b.Branch); !exists {
			t.Errorf("recorded branch %q does not exist in the repository", b.Branch)
		}
	}
	if !state(t, root).BasesAreKnown() {
		t.Error("a run this version performed records every base")
	}
}

// state reads a PRD's git record through a session that performed no run.
func state(t *testing.T, root string) PRDGitState {
	t.Helper()
	got, err := freshSession(t, root).PRDGitFor("main")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A story that committed nothing has a branch identical to the one below it.
// Publishing it would open an empty pull request, and basing the story above on
// it would bury the commits that are actually there.
func TestAStoryWithNoCommitIsRecordedAsHavingNothingToPublish(t *testing.T) {
	s, root, runID := startPerStoryRun(t,
		commitsAndFinishes("a.txt"),
		// Says done, commits nothing: the docs-only story the engine accepts.
		fakeagent.Behaviour{Done: true},
		commitsAndFinishes("c.txt"))

	if snap := waitFor(t, s, runID); snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}

	got := recordedBranches(t, root, "main")
	if len(got) != 3 {
		t.Fatalf("recorded %+v, want one entry per story", got)
	}

	first := storyBranch("US-001", "First story")
	if got[1].StoryID != "US-002" || got[1].HasSomethingToPublish() {
		t.Errorf("US-002 = %+v, want it recorded as having nothing to publish", got[1])
	}
	if got[2].Base != first {
		t.Errorf("US-003 base = %q, want %q — the empty branch must not become a base",
			got[2].Base, first)
	}
	if !got[0].HasSomethingToPublish() || !got[2].HasSomethingToPublish() {
		t.Errorf("the stories that did commit are publishable: %+v, %+v", got[0], got[2])
	}
}

// A run that is stopped has still created branches, and they are what the user is
// left holding. The record is written as each one is created for this reason.
func TestARunStoppedPartWayKeepsTheBranchesItCreated(t *testing.T) {
	s, root, runID := startPerStoryRun(t, fakeagent.Behaviour{Silence: 30 * time.Second})

	waitForState(t, s, runID, StateRunning)
	waitForRecordedBranches(t, s, "main", 1)
	if err := s.Stop(runID); err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, s, runID); snap.State != StateStopped {
		t.Fatalf("state = %s, want stopped", snap.State)
	}

	got := recordedBranches(t, root, "main")
	if len(got) != 1 {
		t.Fatalf("recorded %+v, want only the story the run reached", got)
	}
	want := StoryBranch{StoryID: "US-001", Branch: storyBranch("US-001", "First story"), Base: "main"}
	if got[0] != want {
		t.Errorf("branch = %+v, want %+v", got[0], want)
	}
}

// The run branch is the thing publishing pushes under single-branch layout, so it
// and its base have to be on disk too — including on the worktree path, which
// never goes through ensureRunBranch.
func TestAWorktreeRunRecordsItsBranchAndBase(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", stackPRD)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{Silence: 30 * time.Second}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main", WorkDir: ""})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, runID, StateRunning)
	defer stopRun(t, s, runID)

	state, err := freshSession(t, root).PRDGitFor("main")
	if err != nil {
		t.Fatal(err)
	}
	if state.Branch == "" {
		t.Fatal("the run branch was not recorded")
	}
	if state.Base != "main" {
		t.Errorf("run branch base = %q, want main", state.Base)
	}
}

// ------------------------------------------------------- the record on its own --

// The order is the record. Re-recording a story — which a resumed run does for
// every story it walks again — must not rearrange a stack git has already built.
func TestRecordingAStoryBranchAgainKeepsItsPlaceInTheOrder(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", stackPRD)

	for _, entry := range []StoryBranch{
		{StoryID: "US-001", Branch: "loop/a", Base: "main"},
		{StoryID: "US-002", Branch: "loop/b", Base: "loop/a"},
		{StoryID: "US-003", Branch: "loop/c", Base: "loop/b"},
	} {
		if err := s.recordStoryBranch("checkout", entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.recordStoryBranch("checkout",
		StoryBranch{StoryID: "US-001", Branch: "loop/renamed", Base: "main"}); err != nil {
		t.Fatal(err)
	}

	got := recordedBranches(t, root, "checkout")
	wantOrder := []string{"US-001", "US-002", "US-003"}
	if len(got) != len(wantOrder) {
		t.Fatalf("recorded %+v, want %d entries", got, len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].StoryID != id {
			t.Fatalf("entry %d = %s, want %s — re-recording reordered the stack", i, got[i].StoryID, id)
		}
	}
	if got[0].Branch != "loop/renamed" {
		t.Errorf("US-001 branch = %q, want the re-recorded one", got[0].Branch)
	}
}

// writeLegacySidecar writes the sidecar as a Loop from before branch order and
// bases existed wrote it: a story-to-branch map, and nothing about what anything
// was based on.
func writeLegacySidecar(t *testing.T, root, prd string, stories map[string]string) {
	t.Helper()

	raw, err := json.MarshalIndent(map[string]any{
		"version": 1,
		"git": map[string]any{
			"layout":  string(LayoutBranchPerStory),
			"branch":  "chief/" + prd,
			"stories": stories,
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".chief", "prds", prd, prdMetaFile)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// An older sidecar must open. Refusing it would make a PRD that ran under a
// previous version of Loop unreadable, and guessing bases it never recorded would
// be worse than saying they are unknown.
func TestALegacySidecarWithNoBasesReportsThemAsUnknown(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", stackPRD)
	writeLegacySidecar(t, root, "checkout", map[string]string{
		"US-002": "loop/b", "US-001": "loop/a",
	})

	state, err := freshSession(t, root).PRDGitFor("checkout")
	if err != nil {
		t.Fatalf("an older sidecar must be read, not refused: %v", err)
	}
	if state.BasesAreKnown() {
		t.Error("bases the record never held must be reported as unknown")
	}

	got := state.StoryBranches()
	if len(got) != 2 {
		t.Fatalf("branches = %+v, want both stories", got)
	}
	if got[0].StoryID != "US-001" || got[1].StoryID != "US-002" {
		t.Errorf("branches = %+v, want them ordered by story ID", got)
	}
	for _, b := range got {
		if b.BaseIsKnown() {
			t.Errorf("%s claims a base of %q that was never recorded", b.StoryID, b.Base)
		}
		if !b.HasSomethingToPublish() {
			t.Errorf("%s has a branch, so it has something to publish", b.StoryID)
		}
	}
	if got := state.BranchFor("US-001"); got != "loop/a" {
		t.Errorf("BranchFor(US-001) = %q, want loop/a", got)
	}
}

// A run against an older sidecar must not hide what that sidecar already knew.
func TestANewRecordingKeepsTheBranchesAnOlderSidecarHeld(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", stackPRD)
	writeLegacySidecar(t, root, "checkout", map[string]string{"US-001": "loop/a"})

	if err := s.recordStoryBranch("checkout",
		StoryBranch{StoryID: "US-002", Branch: "loop/b", Base: "loop/a"}); err != nil {
		t.Fatal(err)
	}

	got := recordedBranches(t, root, "checkout")
	if len(got) != 2 {
		t.Fatalf("branches = %+v, want the migrated entry and the new one", got)
	}
	if got[0].StoryID != "US-001" || got[0].Branch != "loop/a" {
		t.Errorf("entry 0 = %+v, want US-001 on loop/a", got[0])
	}
	if got[1].Base != "loop/a" {
		t.Errorf("US-002 base = %q, want loop/a", got[1].Base)
	}
	if state, _ := s.PRDGitFor("checkout"); state.BasesAreKnown() {
		t.Error("a migrated entry still has no base, so the bases are not all known")
	}
}
