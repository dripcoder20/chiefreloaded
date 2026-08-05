package session

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dripcoder/loop/internal/chief/prd"
	"github.com/dripcoder/loop/internal/fakeagent"
)

const runPRD = `# Demo Project

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
`

// startRun opens a git-backed project with the given PRD and a scripted agent.
func startRun(t *testing.T, body string, behaviours ...fakeagent.Behaviour) (*Session, string, string) {
	t.Helper()

	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", body)

	s := newTestSessionWith(t, fakeagent.New(behaviours...))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return s, root, runID
}

// stopRun ends a run and waits for it to unwind, for tests that leave one going.
//
// Stop only asks: it kills the agent and returns, while the run's goroutine is
// still finishing — writing its journal, recording its branch, persisting usage.
// t.TempDir's cleanup runs the moment the test returns, so without the wait
// those writes race the directory removal and intermittently recreate part of
// it, failing the test with "directory not empty" rather than anything to do
// with what it was testing.
func stopRun(t *testing.T, s *Session, runID string) {
	t.Helper()
	_ = s.Stop(runID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.WaitForRun(ctx, runID); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return // the run was never live, or is already gone; nothing to wait for
	}
}

func waitFor(t *testing.T, s *Session, runID string) RunSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	snap, err := s.WaitForRun(ctx, runID)
	if err != nil {
		t.Fatalf("waiting for run: %v (state %s)", err, snap.State)
	}
	return snap
}

func TestRunCompletesEveryStory(t *testing.T) {
	s, root, runID := startRun(t, runPRD, fakeagent.Behaviour{
		Text: "working", WriteFile: "out.txt", FileBody: "x", Commit: true, Done: true,
	})

	snap := waitFor(t, s, runID)
	if snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err: %+v)", snap.State, snap.Error)
	}

	detail, err := s.PRD("main")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range detail.Stories {
		if st.Status != StatusDone {
			t.Errorf("story %s = %s, want done", st.ID, st.Status)
		}
	}

	// One commit per story, as chief promises.
	out, err := exec.Command("git", "-C", root, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"US-001", "US-002"} {
		if !strings.Contains(string(out), id) {
			t.Errorf("no commit for %s:\n%s", id, out)
		}
	}
}

// The core reason RunStory exists: an agent that claims success without
// committing must not silently get the story marked done as if it had worked.
// It is accepted as a no-op, but recorded as one.
func TestStoryWithoutCommitIsRecordedAsNoOp(t *testing.T) {
	s, _, runID := startRun(t, runPRD, fakeagent.Behaviour{Text: "nothing to do", Done: true})

	snap := waitFor(t, s, runID)
	if snap.State != StateComplete {
		t.Fatalf("state = %s, want complete", snap.State)
	}
	assertPublished(t, s, EvStorySkipped)
}

// An agent that neither commits nor claims completion has simply failed. The
// story must stay incomplete rather than advance.
func TestStoryThatDoesNothingIsRetriedThenFails(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{Text: "thinking"}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main", AttemptBudget: minimumAttemptBudget})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitFor(t, s, runID)

	if snap.State != StatePaused {
		t.Errorf("state = %s, want paused once the budget is spent", snap.State)
	}
	if snap.Attempt < minimumAttemptBudget {
		t.Errorf("attempts = %d, want the full budget of %d to be spent retrying",
			snap.Attempt, minimumAttemptBudget)
	}

	detail, err := s.PRD("main")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Stories[0].Status == StatusDone {
		t.Error("a story with no commit and no completion signal was marked done")
	}
}

// chief leaves a story wedged as in-progress whenever a run ends any way other
// than cleanly, and never clears it on startup either.
func TestInProgressIsClearedWhenARunEnds(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{Text: "no commit"}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main", AttemptBudget: minimumAttemptBudget})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, runID)

	doc, err := prd.LoadPRD(s.PRDs()[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range doc.UserStories {
		if st.InProgress {
			t.Errorf("story %s left in-progress after the run ended", st.ID)
		}
	}
}

// A commit whose subject does not follow the convention is still a commit. The
// subject is a convention, not a contract.
func TestCommitWithWrongSubjectIsAccepted(t *testing.T) {
	s, _, runID := startRun(t, runPRD, fakeagent.Behaviour{
		WriteFile: "a.txt", FileBody: "a", Commit: true,
		CommitSubject: "chore: whatever I felt like", Done: true,
	})

	snap := waitFor(t, s, runID)
	if snap.State != StateComplete {
		t.Fatalf("state = %s, want complete — an unconventional subject must not block", snap.State)
	}
}

// Regression for chief's FindCommitForStory, which greps all history: re-running
// a completed story matches an ancestor commit and reports success having done
// nothing. The headBefore..HEAD range check is what prevents that.
func TestVerificationIgnoresCommitsFromEarlierRuns(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)

	// A commit that would match US-001's expected subject, made before the run.
	writeFile(t, root, "old.txt", "old")
	gitCommit(t, root, "feat: US-001 - First story")
	headBefore := revParseHead(context.Background(), root)

	check := verifyCommit(context.Background(), root, headBefore, "US-001", "First story")
	if check.Verdict != VerdictNoCommit {
		t.Errorf("verdict = %s, want no-commit; a pre-existing commit must not count", check.Verdict)
	}
	if check.Matched != "" {
		t.Errorf("matched %s from before the attempt", check.Matched)
	}
}

func TestVerificationReportsDirtyPaths(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	headBefore := revParseHead(context.Background(), root)
	writeFile(t, root, "untracked.txt", "x")

	check := verifyCommit(context.Background(), root, headBefore, "US-001", "First story")
	if len(check.Dirty) == 0 {
		t.Error("uncommitted work should be reported so it is not silently carried forward")
	}
}

func TestStopEndsTheRun(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	// Long enough that Stop lands mid-attempt.
	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{Silence: 30 * time.Second}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, s, runID, StateRunning)
	if err := s.Stop(runID); err != nil {
		t.Fatal(err)
	}

	snap := waitFor(t, s, runID)
	if snap.State != StateStopped {
		t.Errorf("state = %s, want stopped", snap.State)
	}
}

func TestStartRejectsASecondRunForTheSamePRD(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{Silence: 10 * time.Second}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopRun(t, s, runID)

	if _, err := s.Start(context.Background(), StartRequest{PRD: "main"}); err == nil {
		t.Error("a second concurrent run for the same PRD should be refused")
	}
}

func TestStartRejectsUnknownAndUnparseablePRDs(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	s := newTestSessionWith(t, fakeagent.New())
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), StartRequest{PRD: "nope"}); err == nil {
		t.Error("expected an error for an unknown PRD")
	}
}

func TestAttemptBudgetDefaultsToRemainingPlusFive(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "main", runPRD)
	path := discoverPRDs(root)[0].Path

	if got, want := attemptBudgetFor(path), 2+minimumAttemptBudget; got != want {
		t.Errorf("budget = %d, want %d", got, want)
	}
	if err := prd.SetStoryStatus(path, "US-001", "done"); err != nil {
		t.Fatal(err)
	}
	if got, want := attemptBudgetFor(path), 1+minimumAttemptBudget; got != want {
		t.Errorf("budget after one story done = %d, want %d", got, want)
	}
}

func TestAgentEventsReachTheSessionStream(t *testing.T) {
	s, _, runID := startRun(t, runPRD, fakeagent.Behaviour{
		Text: "let me look at that",
		Tools: []fakeagent.ToolCall{
			{Name: "Read", Input: map[string]any{"file_path": "main.go"}, Result: "package main"},
		},
		WriteFile: "x.txt", FileBody: "x", Commit: true, Done: true,
	})
	waitFor(t, s, runID)

	assertPublished(t, s, EvAgentText)
	assertPublished(t, s, EvAgentTool)
	assertPublished(t, s, EvStoryDone)
	assertPublished(t, s, EvRunComplete)
}

// ------------------------------------------------------------------ helpers --

func newTestSessionWith(t *testing.T, p *fakeagent.Provider) *Session {
	t.Helper()
	s, err := New(Options{
		Probe: func(context.Context) Environment {
			return Environment{Git: Tool{Name: "git", Available: true}, StackMode: "none"}
		},
		Provider: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Most tests init a repo on main, which is protected, so every Start would
	// otherwise park waiting for a branch-safety answer. Taking the recommended
	// option is what an unattended run does; TestStartAsksBeforeUsingAProtectedBranch
	// covers the asking itself.
	s.AutoAnswer(true)
	t.Cleanup(func() { s.bus.stop() })
	return s
}

func waitForState(t *testing.T, s *Session, runID string, want LoopState) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range s.Runs() {
			if r.ID == runID && r.State == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %s", runID, want)
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "printf '%s' \"$1\" > \"$2\"", "sh", body, name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write %s: %v\n%s", name, err, out)
	}
}

func gitCommit(t *testing.T, dir, subject string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", subject}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
