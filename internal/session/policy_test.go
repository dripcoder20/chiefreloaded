package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/fakeagent"
)

func perStoryConfig() config.LoopConfig {
	c := config.DefaultLoop()
	c.Git.Mode = config.GitModePerStory
	return *c
}

// Even on an ordinary branch with nothing unsafe about it, the run is about to
// commit somewhere. Asking makes that a decision rather than something the user
// discovers afterwards.
func TestAlwaysAsksWhereToCommit(t *testing.T) {
	p := Project{IsGitRepo: true, Branch: "feature/checkout"}
	q := branchSafetyQuestion(p, *config.DefaultLoop(), "checkout", askContext{})
	if q == nil {
		t.Fatal("expected to be asked where the run should commit")
	}
	if q.DefaultOption != optBranch {
		t.Errorf("default = %q, want a new branch", q.DefaultOption)
	}
	// Continuing on the current branch is a legitimate choice here — nothing is
	// unsafe — so it must not be dressed up as destructive.
	for _, o := range q.Options {
		if o.ID == optHere && o.Destructive {
			t.Error("continuing on an ordinary branch is not destructive")
		}
	}
}

// Both ways of isolating a run must be offered wherever the question appears;
// previously "create a branch" was only available on a protected branch.
func TestEveryCaseOffersBranchAndWorktree(t *testing.T) {
	cases := map[string]struct {
		project Project
		cfg     config.LoopConfig
		others  bool
	}{
		"ordinary branch":  {Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), false},
		"protected branch": {Project{IsGitRepo: true, Branch: "main"}, *config.DefaultLoop(), false},
		"directory in use": {Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q := branchSafetyQuestion(tc.project, tc.cfg, "checkout", askContext{othersInRoot: tc.others})
			if q == nil {
				t.Fatal("expected a question")
			}
			offered := map[string]bool{}
			for _, o := range q.Options {
				offered[o.ID] = true
			}
			for _, want := range []string{optBranch, optWorktree, optCancel} {
				if !offered[want] {
					t.Errorf("%s is not offered (got %v)", want, offered)
				}
			}
		})
	}
}

// The branch name is editable, and pre-filled with something sensible.
func TestTheSuggestedBranchNameIsOffered(t *testing.T) {
	p := Project{IsGitRepo: true, Branch: "feature/x"}
	q := branchSafetyQuestion(p, *config.DefaultLoop(), "checkout", askContext{})
	if len(q.Inputs) != 1 || q.Inputs[0].Key != "branch" {
		t.Fatalf("expected an editable branch input, got %+v", q.Inputs)
	}
	if q.Inputs[0].Value == "" {
		t.Error("the branch name should be pre-filled")
	}
}

func TestProtectedBranchAsksBeforeCommitting(t *testing.T) {
	for _, branch := range []string{"main", "master"} {
		t.Run(branch, func(t *testing.T) {
			p := Project{IsGitRepo: true, Branch: branch}
			q := branchSafetyQuestion(p, *config.DefaultLoop(), "checkout", askContext{})
			if q == nil {
				t.Fatalf("no question raised while on %s", branch)
			}
			if q.DefaultOption != optBranch {
				t.Errorf("default = %q, want a new branch to be recommended", q.DefaultOption)
			}
			// Continuing on main must be possible but never the default, and it
			// must be marked so the UI can style it as the risk it is.
			var here *Option
			for i := range q.Options {
				if q.Options[i].ID == optHere {
					here = &q.Options[i]
				}
			}
			if here == nil {
				t.Fatal("there should still be a way to continue on the current branch")
			}
			if !here.Destructive {
				t.Error("committing agent output onto a protected branch should be flagged destructive")
			}
		})
	}
}

// Two agents editing one working tree produce garbage neither intended.
func TestSecondRunInTheSameDirectoryAsks(t *testing.T) {
	p := Project{IsGitRepo: true, Branch: "feature/x"}
	q := branchSafetyQuestion(p, *config.DefaultLoop(), "checkout", askContext{othersInRoot: true})
	if q == nil {
		t.Fatal("no question raised with another PRD already running here")
	}
	if q.DefaultOption != optWorktree {
		t.Errorf("default = %q, want a worktree", q.DefaultOption)
	}
}

// The safety property that matters most: per-story mode switches branches
// between stories, so running it in the user's own checkout would move their
// working tree under them.
func TestPerStoryAlwaysAsksEvenOnAFeatureBranch(t *testing.T) {
	p := Project{IsGitRepo: true, Branch: "feature/checkout"}
	q := branchSafetyQuestion(p, perStoryConfig(), "checkout", askContext{layout: LayoutBranchPerStory, layoutOffered: true})
	if q == nil {
		t.Fatal("per-story mode must not start in the working checkout without asking")
	}
	if q.DefaultOption != optWorktree {
		t.Errorf("default = %q, want a worktree", q.DefaultOption)
	}
}

// With requireWorktree on — the default — there is no way to say "run here".
func TestPerStoryOffersNoEscapeHatchByDefault(t *testing.T) {
	p := Project{IsGitRepo: true, Branch: "feature/checkout"}
	q := branchSafetyQuestion(p, perStoryConfig(), "checkout", askContext{layout: LayoutBranchPerStory, layoutOffered: true})

	for _, o := range q.Options {
		if o.ID == optHere {
			t.Error("requireWorktree is on, so running in the checkout must not be offered")
		}
	}
}

func TestPerStoryOffersTheEscapeHatchWhenConfigured(t *testing.T) {
	cfg := perStoryConfig()
	cfg.Git.RequireWorktree = false

	q := branchSafetyQuestion(Project{IsGitRepo: true, Branch: "feature/x"}, cfg, "checkout", askContext{layout: LayoutBranchPerStory, layoutOffered: true})

	var found bool
	for _, o := range q.Options {
		if o.ID == optHere {
			found = true
			if !o.Destructive {
				t.Error("overriding requireWorktree should still be marked destructive")
			}
		}
	}
	if !found {
		t.Error("with requireWorktree off the override should be available")
	}
	if q.DefaultOption == optHere {
		t.Error("the override must never be the default")
	}
}

// Every question must offer a way out, or the UI can present a decision the user
// cannot decline.
func TestEveryBranchSafetyQuestionCanBeCancelled(t *testing.T) {
	cases := []struct {
		name   string
		p      Project
		cfg    config.LoopConfig
		others bool
		layout BranchLayout
	}{
		{"protected", Project{IsGitRepo: true, Branch: "main"}, *config.DefaultLoop(), false, LayoutOneBranch},
		{"in use", Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), true, LayoutOneBranch},
		{"per-story", Project{IsGitRepo: true, Branch: "feature/x"}, perStoryConfig(), false, LayoutBranchPerStory},
		{"ordinary", Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), false, LayoutOneBranch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := branchSafetyQuestion(tc.p, tc.cfg, "checkout", askContext{othersInRoot: tc.others, layout: tc.layout, layoutOffered: true})
			if q == nil {
				t.Fatal("expected a question")
			}

			var cancel, recommended bool
			for _, o := range q.Options {
				if o.ID == optCancel {
					cancel = true
				}
				if o.Recommended {
					recommended = true
				}
			}
			if !cancel {
				t.Error("no way to cancel")
			}
			if !recommended {
				t.Error("no option marked recommended, so the UI has nothing to highlight")
			}
			if !validOption(*q, q.DefaultOption) {
				t.Errorf("default %q is not one of the options", q.DefaultOption)
			}
		})
	}
}

// End to end: on main, with auto-answer taking the recommendation, the run must
// end up somewhere other than main.
func TestStartOnMainDoesNotCommitToMain(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{
		WriteFile: "a.txt", FileBody: "a", Commit: true, Done: true,
	}))
	s.AutoAnswer(true)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitFor(t, s, runID)
	if snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}

	// The recommendation on a protected branch is a new branch, so main must be
	// exactly where it was.
	if got := currentBranch(context.Background(), root); got == "main" {
		t.Error("the run committed on main; branch safety did not take effect")
	}
}

func TestWorktreeProvisioningRunsTheSetupCommand(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", runPRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	cfg := s.LoopConfig()
	cfg.Worktree.Setup = "printf hello > setup-ran.txt"
	if err := s.SaveLoopConfig(cfg); err != nil {
		t.Fatal(err)
	}

	path, err := s.provisionWorktree(context.Background(), root, "checkout", "chief/checkout", s.LoopConfig())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(path, "setup-ran.txt")); err != nil {
		t.Errorf("the setup command did not run in the worktree: %v", err)
	}
	assertPublished(t, s, EvStep)
}

// A failing setup command should be reported, not fatal: the worktree exists and
// is perfectly usable, and refusing to run because `npm ci` had a bad day would
// be worse than carrying on.
func TestFailingSetupDoesNotBlockTheWorktree(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", runPRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	cfg := s.LoopConfig()
	cfg.Worktree.Setup = "exit 3"
	if err := s.SaveLoopConfig(cfg); err != nil {
		t.Fatal(err)
	}

	path, err := s.provisionWorktree(context.Background(), root, "checkout", "chief/checkout", s.LoopConfig())
	if err != nil {
		t.Fatalf("a failing setup command should not fail provisioning: %v", err)
	}
	if path == "" {
		t.Error("the worktree path should still be returned")
	}
}

// Start must return at once even when a decision is outstanding. In the GUI it
// is a bound method, so blocking it would freeze the IPC call for as long as the
// dialog is on screen — and the frontend would have no run ID to attach the
// question to.
func TestStartAsksBeforeUsingAProtectedBranch(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root) // creates main, which is protected
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{
		WriteFile: "a.txt", FileBody: "a", Commit: true, Done: true,
	}))
	s.AutoAnswer(false)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatalf("Start should return immediately, not fail: %v", err)
	}

	q := awaitQuestion(t, s)
	if q.Kind != QBranchSafety {
		t.Errorf("question kind = %q, want branch-safety", q.Kind)
	}
	if q.RunID != runID {
		t.Errorf("question runId = %q, want %q so the UI can attach it", q.RunID, runID)
	}

	// Answering lets the run proceed.
	if err := s.Answer(q.ID, Answer{OptionID: optBranch}); err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, s, runID); snap.State != StateComplete {
		t.Errorf("state = %s, want complete once the question was answered", snap.State)
	}
}

func TestCancellingTheQuestionStopsTheRun(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New())
	s.AutoAnswer(false)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}

	q := awaitQuestion(t, s)
	if err := s.Answer(q.ID, Answer{OptionID: optCancel}); err != nil {
		t.Fatal(err)
	}

	snap := waitFor(t, s, runID)
	if snap.State == StateComplete || snap.State == StateRunning {
		t.Errorf("state = %s, want the run to have ended after cancelling", snap.State)
	}
}

func TestAnswerRejectsAnOptionThatIsNotOffered(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", runPRD)

	s := newTestSessionWith(t, fakeagent.New())
	s.AutoAnswer(false)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopRun(t, s, runID)

	q := awaitQuestion(t, s)
	if err := s.Answer(q.ID, Answer{OptionID: "definitely-not-an-option"}); err == nil {
		t.Error("an option that was not offered should be rejected")
	}
	// Rejecting the answer must not also lose the question, or the run is stuck
	// with nothing on screen.
	if len(s.PendingQuestions()) != 1 {
		t.Error("the question should still be pending after a rejected answer")
	}
}

func awaitQuestion(t *testing.T, s *Session) Question {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if qs := s.PendingQuestions(); len(qs) > 0 {
			return qs[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no question was raised")
	return Question{}
}

// Resuming a PRD is the ordinary case: the branch from the previous run still
// exists, and refusing to start because of that would make a second run
// impossible. The worktree path has always adopted an existing worktree.
func TestRunBranchIsAdoptedWhenItAlreadyExists(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	if err := s.ensureRunBranch(t.Context(), root, "main", "chief/main"); err != nil {
		t.Fatalf("creating the branch: %v", err)
	}
	if got := currentBranch(t.Context(), root); got != "chief/main" {
		t.Fatalf("on %q, want chief/main", got)
	}

	// Move away, then ask for it again — as a second run would.
	if err := gitRun(t.Context(), root, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureRunBranch(t.Context(), root, "main", "chief/main"); err != nil {
		t.Fatalf("adopting the existing branch: %v", err)
	}
	if got := currentBranch(t.Context(), root); got != "chief/main" {
		t.Errorf("on %q, want chief/main", got)
	}
}

// Already being on the branch is a no-op, not an error.
func TestRunBranchIsANoOpWhenAlreadyThere(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := s.ensureRunBranch(t.Context(), root, "main", "chief/main"); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if got := currentBranch(t.Context(), root); got != "chief/main" {
		t.Errorf("on %q, want chief/main", got)
	}
}
