package session

import (
	"context"
	"strings"
	"testing"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// layoutInputOf finds the layout field on a question, failing if it is absent.
func layoutInputOf(t *testing.T, q *Question) Input {
	t.Helper()
	for _, in := range q.Inputs {
		if in.Key == inputLayout {
			return in
		}
	}
	t.Fatalf("no layout field on %q (inputs %+v)", q.Title, q.Inputs)
	return Input{}
}

// One decision, not two consecutive prompts: the layout rides on the question
// that already asks where the run should commit.
func TestTheLayoutChoiceRidesOnTheBranchSafetyQuestion(t *testing.T) {
	q := branchSafetyQuestion(
		Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), "checkout",
		askContext{layout: LayoutOneBranch, layoutOffered: true})
	if q == nil {
		t.Fatal("expected a question")
	}

	var hasBranch bool
	for _, in := range q.Inputs {
		if in.Key == inputBranch {
			hasBranch = true
		}
	}
	if !hasBranch {
		t.Error("the branch field is gone; the layout must be added to that decision, not replace it")
	}

	layout := layoutInputOf(t, q)
	want := map[string]bool{string(LayoutOneBranch): true, string(LayoutBranchPerStory): true}
	if len(layout.Choices) != len(want) {
		t.Fatalf("choices = %+v, want exactly %d", layout.Choices, len(want))
	}
	for _, c := range layout.Choices {
		if !want[c.Value] {
			t.Errorf("unexpected layout choice %q", c.Value)
		}
		if c.Label == "" {
			t.Errorf("choice %q has no label to render", c.Value)
		}
	}
	if layout.Value != string(LayoutOneBranch) {
		t.Errorf("preselected %q, want one branch for the whole PRD", layout.Value)
	}
}

// Nothing recorded means one branch, so a user who does not engage with the
// question never has one PRD turn into nine pull requests.
func TestTheLayoutDefaultsToOneBranch(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", runPRD)

	if got := s.layoutFor("checkout"); got != LayoutOneBranch {
		t.Errorf("layout = %q, want %q with nothing recorded", got, LayoutOneBranch)
	}
	if s.layoutIsSettled("checkout") {
		t.Error("a PRD with no recorded layout has nothing settled")
	}
}

// The choice is made once. A second run of the same PRD preselects what the
// first one chose rather than starting from the default again.
func TestARecordedLayoutIsPreselectedOnTheNextRun(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", runPRD)

	if err := s.recordLayout("checkout", LayoutBranchPerStory); err != nil {
		t.Fatal(err)
	}
	if got := s.layoutFor("checkout"); got != LayoutBranchPerStory {
		t.Fatalf("layout = %q, want the recorded %q", got, LayoutBranchPerStory)
	}

	q := branchSafetyQuestion(
		Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), "checkout",
		askContext{layout: s.layoutFor("checkout"), layoutOffered: true})
	if got := layoutInputOf(t, q).Value; got != string(LayoutBranchPerStory) {
		t.Errorf("preselected %q, want the recorded layout", got)
	}
}

// Once commits are arranged one way, rearranging them is not on offer: the
// record and the repository would disagree and nothing could publish that.
func TestTheLayoutIsSettledOnceAStoryHasCommitted(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", samplePRD) // US-001 is done

	if err := s.recordLayout("checkout", LayoutOneBranch); err != nil {
		t.Fatal(err)
	}
	if !s.layoutIsSettled("checkout") {
		t.Fatal("a PRD with a completed story under a recorded layout is settled")
	}

	ask := askContext{layout: LayoutOneBranch, layoutOffered: true, layoutSettled: true}
	layout := layoutInputOf(t, branchSafetyQuestion(
		Project{IsGitRepo: true, Branch: "feature/x"}, *config.DefaultLoop(), "checkout", ask))
	if !layout.ReadOnly {
		t.Error("a settled layout must be reported, not offered")
	}
	if len(layout.Choices) != 0 {
		t.Errorf("a settled layout offers no choices, got %+v", layout.Choices)
	}
	if layout.Hint == "" {
		t.Error("a settled layout should say why it cannot change")
	}

	if _, err := resolveLayout("checkout", ask, Answer{
		Inputs: map[string]string{inputLayout: string(LayoutBranchPerStory)},
	}); err == nil {
		t.Error("changing a settled layout must be refused")
	}
	got, err := resolveLayout("checkout", ask, Answer{
		Inputs: map[string]string{inputLayout: string(LayoutOneBranch)},
	})
	if err != nil || got != LayoutOneBranch {
		t.Errorf("answering with the settled layout = (%q, %v), want it accepted", got, err)
	}
}

// An answer carrying no layout means the preselected one, which is how
// auto-answering and every caller that does not care keep working.
func TestAnAnswerWithNoLayoutKeepsThePreselectedOne(t *testing.T) {
	ask := askContext{layout: LayoutBranchPerStory, layoutOffered: true}
	got, err := resolveLayout("checkout", ask, Answer{OptionID: optBranch})
	if err != nil {
		t.Fatal(err)
	}
	if got != LayoutBranchPerStory {
		t.Errorf("layout = %q, want the preselected %q", got, LayoutBranchPerStory)
	}
}

func TestAnUnknownLayoutIsRefused(t *testing.T) {
	ask := askContext{layout: LayoutOneBranch, layoutOffered: true}
	if _, err := resolveLayout("checkout", ask, Answer{
		Inputs: map[string]string{inputLayout: "every-story-its-own-repo"},
	}); err == nil {
		t.Error("a layout the engine cannot act on must fail loudly")
	}
}

// Git mode off leaves git alone: no layout choice, and no per-story branches
// whatever a previous run recorded.
func TestGitModeOffOffersNoLayoutAndCreatesNoBranches(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", runPRD)

	if err := s.recordLayout("checkout", LayoutBranchPerStory); err != nil {
		t.Fatal(err)
	}
	cfg := s.LoopConfig()
	cfg.Git.Mode = config.GitModeOff
	if err := s.SaveLoopConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if got := s.layoutFor("checkout"); got != LayoutOneBranch {
		t.Errorf("layout = %q with git mode off, want no per-story branches", got)
	}

	q := branchSafetyQuestion(
		Project{IsGitRepo: true, Branch: "feature/x"}, cfg, "checkout", askContext{layout: LayoutOneBranch})
	for _, in := range q.Inputs {
		if in.Key == inputLayout {
			t.Error("git mode off must present no layout choice")
		}
	}
}

// Nothing to branch and nothing to protect, so nothing is asked.
func TestANonGitProjectIsAskedNothing(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	writePRD(t, root, "checkout", runPRD)

	workDir, err := s.prepareWorkspace(t.Context(), StartRequest{PRD: "checkout"}, "run_1")
	if err != nil {
		t.Fatal(err)
	}
	if workDir != root {
		t.Errorf("workDir = %q, want the project root", workDir)
	}
	if qs := s.PendingQuestions(); len(qs) != 0 {
		t.Errorf("a directory that is not a git repository was asked %+v", qs)
	}
	if state, err := s.PRDGitFor("checkout"); err != nil || state.Layout != "" {
		t.Errorf("layout = %q (err %v), want nothing recorded outside a repository", state.Layout, err)
	}
}

// Per-story branches switch the checkout between stories, so the two halves of
// the answer are refused when they disagree — with the reason.
func TestPerStoryLayoutRefusesToRunOutsideAWorktree(t *testing.T) {
	cfg := *config.DefaultLoop()
	if !cfg.Git.RequireWorktree {
		t.Fatal("requireWorktree is expected to be on by default")
	}

	inWorktree := Answer{OptionID: optBranch, Inputs: map[string]string{inputWorktree: "true"}}

	err := requireWorktreeFor(LayoutBranchPerStory, cfg, Answer{OptionID: optBranch})
	if err == nil {
		t.Fatal("a branch per story inside the checkout must be refused")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("the refusal should say what to do instead: %v", err)
	}
	if err := requireWorktreeFor(LayoutBranchPerStory, cfg, inWorktree); err != nil {
		t.Errorf("a worktree is exactly what it asked for: %v", err)
	}
	if err := requireWorktreeFor(LayoutOneBranch, cfg, Answer{OptionID: optBranch}); err != nil {
		t.Errorf("one branch does not switch the checkout: %v", err)
	}
	// The toggle cannot rescue "run here": there is no worktree in that answer.
	here := Answer{OptionID: optHere, Inputs: map[string]string{inputWorktree: "true"}}
	if err := requireWorktreeFor(LayoutBranchPerStory, cfg, here); err == nil {
		t.Error("running here is refused for per-story whatever the toggle says")
	}

	cfg.Git.RequireWorktree = false
	if err := requireWorktreeFor(LayoutBranchPerStory, cfg, Answer{OptionID: optHere}); err != nil {
		t.Errorf("with requireWorktree off the override stands: %v", err)
	}
}

// End to end: the layout a run started under is on disk, so whatever publishes
// the work later knows how it was arranged.
func TestARunRecordsTheLayoutItStartedUnder(t *testing.T) {
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
	if snap := waitFor(t, s, runID); snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}

	state, err := s.PRDGitFor("main")
	if err != nil {
		t.Fatal(err)
	}
	if state.Layout != LayoutOneBranch {
		t.Errorf("recorded layout = %q, want %q", state.Layout, LayoutOneBranch)
	}
}
