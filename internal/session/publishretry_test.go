package session

import (
	"context"
	"strings"
	"testing"
)

// Publishing a stack reaches the network once per story, so it can stop half way
// with some pull requests open and some not. These tests are about what happens
// next: what the failure says, what is on disk afterwards, and what pressing the
// control again does.
//
// The failure is a real one — a scripted gh that refuses to create a pull request
// for one branch, the way an unreachable GitHub does — because the property under
// test is that the pass keeps what it created, and a mocked driver that never
// created anything cannot show that.

// tryPublishStack publishes and hands back both halves of the answer. A partial
// failure returns a report and an error together, which is the shape the whole
// story rests on.
func (p publishable) tryPublishStack(t *testing.T) (StackReport, error) {
	t.Helper()
	return p.session.PublishStack(context.Background(), PublishRequest{PRD: "main", Draft: false})
}

// threeStoryStack is a finished per-story run of three committing stories, with
// the branches its stack is made of.
type threeStoryStack struct {
	publishable
	first, second, third string
}

func stackOfThree(t *testing.T) threeStoryStack {
	t.Helper()
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))
	return threeStoryStack{
		publishable: p,
		first:       storyBranch("US-001", "First story"),
		second:      storyBranch("US-002", "Second story"),
		third:       storyBranch("US-003", "Third story"),
	}
}

// ------------------------------------------------------------- the failure --

// The second of three stories fails. The first keeps its pull request, the second
// says why it has none, and the third says it was not attempted — and the run's
// record of what it put into git is untouched by any of it.
func TestAStackThatFailsHalfWayKeepsWhatItCreated(t *testing.T) {
	p := stackOfThree(t)
	p.gh.failCreating(t, p.second)

	report, err := p.tryPublishStack(t)

	if err == nil {
		t.Fatal("publishing must report the failure, not swallow it")
	}
	if !strings.Contains(report.Failed, "US-002") {
		t.Errorf("failed = %q, want it to name the story that failed", report.Failed)
	}

	// Per story: one created, one failed with the reason, one not attempted.
	if got := entryFor(t, report, "US-001"); !got.HasPullRequest() {
		t.Errorf("US-001 = %+v, want the pull request opened before the failure", got)
	}
	failed := entryFor(t, report, "US-002")
	if failed.HasPullRequest() {
		t.Errorf("US-002 = %+v, want no pull request for the story that failed", failed)
	}
	if !strings.Contains(failed.Error, "github.com") {
		t.Errorf("US-002 error = %q, want it to say why", failed.Error)
	}
	blocked := entryFor(t, report, "US-003")
	if blocked.HasPullRequest() {
		t.Errorf("US-003 = %+v, want nothing opened above the failure", blocked)
	}
	if !strings.Contains(blocked.Skipped, "below") {
		t.Errorf("US-003 skipped = %q, want it to say the branch below was not published", blocked.Skipped)
	}

	// Recorded as the failure happened, not after the pass: a fresh reader finds
	// the link for the story that succeeded.
	git := state(t, p.root)
	if _, ok := git.PullRequests[p.first]; !ok {
		t.Errorf("pull requests on disk = %+v, want the one opened before the failure", git.PullRequests)
	}
	if _, ok := git.PullRequests[p.second]; ok {
		t.Errorf("pull requests on disk = %+v, want nothing recorded for the story that failed", git.PullRequests)
	}

	// The failure must not cost the run its record of what it built.
	if got := storyIDsOfBranches(git); !equal(got, []string{"US-001", "US-002", "US-003"}) {
		t.Errorf("recorded branches = %v, want every branch the run created", got)
	}
	for _, b := range git.StoryBranches() {
		if !b.BaseIsKnown() {
			t.Errorf("%s lost its base: %+v", b.StoryID, b)
		}
	}
}

// -------------------------------------------------------------- the retry --

// Pressing again after a failure finishes the stack. The story that already has a
// pull request is reported back rather than attempted, and the two that do not get
// theirs — so three stories end up with three pull requests, not four.
func TestRetryingAfterAFailureOpensOnlyWhatIsMissing(t *testing.T) {
	p := stackOfThree(t)
	p.gh.failCreating(t, p.second)
	if _, err := p.tryPublishStack(t); err == nil {
		t.Fatal("the first pass was meant to fail")
	}
	p.gh.allowCreating(t, p.second)

	report, err := p.tryPublishStack(t)

	if err != nil {
		t.Fatalf("the retry did not finish the stack: %v", err)
	}
	if report.Failed != "" {
		t.Errorf("failed = %q, want a retry that completed to report nothing", report.Failed)
	}

	first := entryFor(t, report, "US-001")
	if !first.AlreadyOpen {
		t.Errorf("US-001 = %+v, want the pull request it already had reported, not reopened", first)
	}
	for _, id := range []string{"US-002", "US-003"} {
		entry := entryFor(t, report, id)
		if !entry.HasPullRequest() {
			t.Errorf("%s = %+v, want the retry to have opened its pull request", id, entry)
		}
		if entry.AlreadyOpen {
			t.Errorf("%s = %+v, want it reported as opened by this pass", id, entry)
		}
	}

	// One pull request per branch over both passes. A second for a branch that had
	// one is the failure this whole story exists to prevent.
	if got := p.gh.createdOrder(); !equal(got, []string{slug(p.first), slug(p.second), slug(p.third)}) {
		t.Errorf("created for %v, want one per branch with the retry continuing where it stopped", got)
	}
	// The stack still holds: the retry's layers are based on the branches below.
	assertBase(t, p.gh, p.second, p.first)
	assertBase(t, p.gh, p.third, p.second)
}

// A retry with nothing left to do reports the same three pull requests. It cannot
// be a no-op — the user pressed the control and is owed an answer — and it cannot
// open anything, because everything it would open is already there.
func TestRetryingACompleteStackReportsTheSamePullRequests(t *testing.T) {
	p := stackOfThree(t)

	first, err := p.tryPublishStack(t)
	if err != nil {
		t.Fatalf("publishing the stack: %v", err)
	}
	second, err := p.tryPublishStack(t)
	if err != nil {
		t.Fatalf("republishing a finished stack must not fail: %v", err)
	}

	if got := p.gh.creations(); len(got) != 3 {
		t.Errorf("gh pr create ran %d time(s) over two passes, want one per story: %v", len(got), got)
	}
	// Not even an edit: a story with an open pull request is left entirely alone.
	if p.gh.edited() {
		t.Error("a retry with nothing left to do rewrote an existing pull request")
	}
	for i, entry := range second.Stories {
		if !entry.AlreadyOpen {
			t.Errorf("%s = %+v, want every story reported as already open", entry.StoryID, entry)
		}
		if entry.PR == nil || first.Stories[i].PR == nil || entry.PR.Number != first.Stories[i].PR.Number {
			t.Errorf("%s = %+v, want the same pull request as %+v",
				entry.StoryID, entry.PR, first.Stories[i].PR)
		}
	}
}

// ------------------------------------------------------------------ helpers --

func storyIDsOfBranches(git PRDGitState) []string {
	out := make([]string, 0, len(git.StoryBranches()))
	for _, b := range git.StoryBranches() {
		out = append(out, b.StoryID)
	}
	return out
}
