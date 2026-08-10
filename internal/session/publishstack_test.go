package session

import (
	"context"
	"strings"
	"testing"

	"github.com/dripcoder/loop/internal/fakeagent"
)

// A stack is an order, a set of bases and a set of links. These tests drive it
// through the same real repository, real remote and scripted gh as a single pull
// request, because a stack that opens three pull requests all based on the trunk
// passes every assertion a mocked driver could make.

// publishStack runs the stacked control.
func (p publishable) publishStack(t *testing.T, draft bool) StackReport {
	t.Helper()
	report, err := p.session.PublishStack(context.Background(),
		PublishRequest{PRD: "main", Draft: draft})
	if err != nil {
		t.Fatalf("publishing the stack: %v", err)
	}
	return report
}

// ------------------------------------------------------------ three stories --

// The shape of the whole feature: three stories, three pull requests, each based
// on the branch below it and the bottom one on the trunk.
func TestPublishingAThreeStoryStackOpensAPullRequestPerStory(t *testing.T) {
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	report := p.publishStack(t, false)

	first := storyBranch("US-001", "First story")
	second := storyBranch("US-002", "Second story")
	third := storyBranch("US-003", "Third story")

	if got := storyIDsOf(report); !equal(got, []string{"US-001", "US-002", "US-003"}) {
		t.Fatalf("stories = %v, want every story in the order the run reached them", got)
	}
	for _, entry := range report.Stories {
		if !entry.HasPullRequest() || entry.PR.URL == "" {
			t.Errorf("%s = %+v, want a pull request with a link", entry.StoryID, entry)
		}
	}

	// Bottom upwards, because a base has to exist on the remote before anything
	// can be based on it.
	if got := p.gh.createdOrder(); !equal(got, []string{slug(first), slug(second), slug(third)}) {
		t.Errorf("created in the order %v, want the stack published from the bottom up", got)
	}
	assertBase(t, p.gh, first, "main")
	assertBase(t, p.gh, second, first)
	assertBase(t, p.gh, third, second)

	if got := p.gh.creations(); len(got) != 3 {
		t.Errorf("gh pr create ran %d time(s), want one per story: %v", len(got), got)
	}
	for _, branch := range []string{first, second, third} {
		if !contains(remoteRefs(t, p.remote), branch) {
			t.Errorf("the remote holds %v, not %q", remoteRefs(t, p.remote), branch)
		}
	}
}

// Each pull request is that story's own: its title names it, and its description
// is the one composed when that story was verified.
func TestEachStackedPullRequestDescribesItsOwnStory(t *testing.T) {
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	p.publishStack(t, true)

	second := storyBranch("US-002", "Second story")
	if title := p.gh.titleFor(second); !strings.Contains(title, "US-002") {
		t.Errorf("title = %q, want it to name the story", title)
	}
	body := p.gh.bodyFor(second)
	if !strings.Contains(body, "Do the second thing") {
		t.Errorf("description = %q, want the story's own description", body)
	}
	if strings.Contains(body, "Do the third thing") {
		t.Errorf("description of US-002 describes US-003 as well:\n%s", body)
	}
	if !p.gh.draftFor(second) {
		t.Error("a draft stack opened a pull request that is not a draft")
	}
}

// AC-5: every created pull request is shown against its story. The link is
// recorded against the branch as it is created, so a fresh read of the PRD finds
// it — which is what the story list renders from.
func TestAStackedPullRequestIsShownAgainstItsStory(t *testing.T) {
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	p.publishStack(t, false)

	detail, err := freshSession(t, p.root).PRD("main")
	if err != nil {
		t.Fatal(err)
	}
	for _, story := range detail.Stories {
		if story.PR == nil || story.PR.URL == "" {
			t.Errorf("%s shows no pull request; a link the user cannot follow is not a link", story.ID)
		}
	}
}

// ---------------------------------------------------------- an empty story --

// A story that committed nothing has a branch at the same commit as the one
// below it. It gets no pull request, and the story above it is based on the
// nearest branch below that has one — not on the empty branch, whose pull request
// would have been empty and whose absence must not push US-003 onto the trunk.
func TestAStoryWithNoCommitContributesNoPullRequest(t *testing.T) {
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), fakeagent.Behaviour{Done: true}, commitsAndFinishes("c.txt"))

	report := p.publishStack(t, false)

	first := storyBranch("US-001", "First story")
	third := storyBranch("US-003", "Third story")

	skipped := entryFor(t, report, "US-002")
	if skipped.HasPullRequest() {
		t.Errorf("US-002 = %+v, want no pull request for a story that committed nothing", skipped)
	}
	if !strings.Contains(skipped.Skipped, "no commit") {
		t.Errorf("US-002 skipped = %q, want it to say why", skipped.Skipped)
	}
	if got := entryFor(t, report, "US-003").Base; got != first {
		t.Errorf("US-003 is based on %q, want the nearest branch below with a commit %q", got, first)
	}
	assertBase(t, p.gh, third, first)

	if got := p.gh.creations(); len(got) != 2 {
		t.Errorf("gh pr create ran %d time(s), want one per story that committed: %v", len(got), got)
	}
	if calls := strings.Join(p.gh.calls(), "\n"); strings.Contains(calls, storyBranch("US-002", "Second story")) {
		t.Errorf("gh was asked about the empty branch:\n%s", calls)
	}
}

// ------------------------------------------------------------- the refusals --

// A run that put everything on one branch produced no stack. The refusal says so,
// rather than opening one pull request and calling it a stack of one.
func TestPublishingAStackIsRefusedUnderASingleBranchLayout(t *testing.T) {
	p := runThenOpen(t, LayoutOneBranch,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	_, err := p.session.PublishStack(context.Background(), PublishRequest{PRD: "main"})
	if err == nil {
		t.Fatal("a single-branch PRD has no stack to publish; that must be refused")
	}
	if !strings.Contains(err.Error(), "one branch") {
		t.Errorf("refusal = %q, want it to say the layout does not allow it", err)
	}
	if calls := p.gh.calls(); len(calls) != 0 {
		t.Errorf("a refused stack reached gh: %v", calls)
	}
}

// The control has to say the same thing before it is pressed: the stacked item is
// offered under one layout and explained away under the other.
func TestTheStackedItemIsOfferedOnlyUnderAPerStoryLayout(t *testing.T) {
	if offer := offerUnder(t, LayoutBranchPerStory); !offer.Stacked {
		t.Errorf("offer = %+v, want the stacked item offered for a branch-per-story PRD", offer)
	}

	offer := offerUnder(t, LayoutOneBranch)
	if offer.Stacked {
		t.Errorf("offer = %+v, want no stacked item for a single-branch PRD", offer)
	}
	if !strings.Contains(offer.StackReason, "one branch") {
		t.Errorf("reason = %q, want it to state that the layout does not allow it", offer.StackReason)
	}
}

// offerUnder is the control a PRD with a committed story offers under a recorded
// layout. No run: the offer is read entirely off the disk, which is the point.
func offerUnder(t *testing.T, layout BranchLayout) PublishOffer {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	writePRD(t, root, "main", donePRD)
	if err := s.recordLayout("main", layout); err != nil {
		t.Fatal(err)
	}
	return s.PublishOfferFor("main")
}

// ------------------------------------------------------ the plan on its own --

// The bases are the stack, and they are computed from what each branch has on it
// rather than from what the record says it was cut from: a record written before
// bases existed names none, and a base can name a branch that turned out empty.
func TestStackLayersBaseEachStoryOnTheNearestBranchBelowWithACommit(t *testing.T) {
	git := PRDGitState{Branches: []StoryBranch{
		{StoryID: "US-001", Branch: "loop/a"},
		{StoryID: "US-002", Branch: "loop/b", NoCommit: true},
		{StoryID: "US-003", Branch: "loop/c"},
		{StoryID: "US-004", PullRequestBody: "no branch of its own"},
	}}
	detail := PRDDetail{Stories: []StorySnap{
		{ID: "US-001", Title: "First"}, {ID: "US-002", Title: "Second"},
		{ID: "US-003", Title: "Third"}, {ID: "US-004", Title: "Fourth"},
	}}

	layers := stackLayers(git, detail, "main")

	want := []struct {
		base    string
		skipped bool
	}{{base: "main"}, {skipped: true}, {base: "loop/a"}, {skipped: true}}
	if len(layers) != len(want) {
		t.Fatalf("got %d layers, want %d: %+v", len(layers), len(want), layers)
	}
	for i, w := range want {
		if (layers[i].skipped != "") != w.skipped {
			t.Errorf("layer %d skipped = %q, want skipped=%v", i, layers[i].skipped, w.skipped)
		}
		if !w.skipped && layers[i].base != w.base {
			t.Errorf("layer %d base = %q, want %q", i, layers[i].base, w.base)
		}
	}
}

// ------------------------------------------------------------------ helpers --

func assertBase(t *testing.T, gh *scriptedGH, branch, want string) {
	t.Helper()
	if got := gh.baseFor(branch); got != want {
		t.Errorf("%s was opened against %q, want %q", branch, got, want)
	}
}

func entryFor(t *testing.T, report StackReport, storyID string) StoryPublish {
	t.Helper()
	for _, entry := range report.Stories {
		if entry.StoryID == storyID {
			return entry
		}
	}
	t.Fatalf("%s is not in the report: %+v", storyID, report.Stories)
	return StoryPublish{}
}

func storyIDsOf(report StackReport) []string {
	out := make([]string, 0, len(report.Stories))
	for _, entry := range report.Stories {
		out = append(out, entry.StoryID)
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
