package session

import (
	"testing"
)

// The branches a PRD has claimed are the only ones worth asking GitHub about.
// Everything else in the repository is somebody else's work.
func TestBranchesOf_collectsThePRDAndItsStories(t *testing.T) {
	git := PRDGitState{
		Branch:  "chief/checkout",
		Stories: map[string]string{"US-001": "loop/us-001-cart", "US-002": ""},
	}

	branches := branchesOf(git)

	if !branches["chief/checkout"] || !branches["loop/us-001-cart"] {
		t.Errorf("got %v, want both the PRD branch and US-001's", branches)
	}
	if len(branches) != 2 {
		t.Errorf("got %d branches, want 2 — an empty story branch is not a branch", len(branches))
	}
}

// A PRD that has never run has no branches, and must not send Loop to GitHub
// asking about nothing.
func TestRefreshPullRequests_asksNothingWithoutBranches(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", samplePRD)

	set, err := s.RefreshPullRequests(t.Context(), "checkout")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(set.ByBranch) != 0 || set.CheckedAt != 0 {
		t.Errorf("got %+v, want an empty set with no lookup recorded", set)
	}
}

// GitHub being out of reach is a normal state, not a failure: the branch is
// still the user's work and the cached link is still worth following.
func TestRefreshPullRequests_keepsTheCacheWhenGitHubIsUnreachable(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", samplePRD)

	if err := s.recordRunBranch("checkout", "chief/checkout", "main"); err != nil {
		t.Fatal(err)
	}
	cached := PRRef{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN", CheckedAt: 1000}
	if err := s.recordPullRequest("checkout", "chief/checkout", cached); err != nil {
		t.Fatal(err)
	}

	// The temp repository has no remote, so gh cannot answer.
	set, err := s.RefreshPullRequests(t.Context(), "checkout")
	if err != nil {
		t.Fatalf("a project GitHub cannot answer for is not an error: %v", err)
	}
	if set.Unavailable == "" {
		t.Error("want an explanation of why the live lookup did not run")
	}

	got, ok := set.ByBranch["chief/checkout"]
	if !ok {
		t.Fatal("the cached pull request should still be offered")
	}
	if got.Number != 7 {
		t.Errorf("got #%d, want the cached #7", got.Number)
	}
	if got.CheckedAt != 1000 {
		t.Errorf("CheckedAt = %d, want the cached 1000 — a cache must not date itself now", got.CheckedAt)
	}
}

// Branch names are recomputable only until the template changes or the user
// edits the suggested branch, so what a run used is recorded rather than derived.
func TestRecordedBranches_separatesThePRDFromItsStories(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", samplePRD)

	if err := s.recordRunBranch("checkout", "chief/checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := s.recordStoryBranch("checkout", StoryBranch{StoryID: "US-001", Branch: "loop/us-001-cart", Base: "chief/checkout"}); err != nil {
		t.Fatal(err)
	}

	git, err := s.PRDGitFor("checkout")
	if err != nil {
		t.Fatal(err)
	}
	if git.Branch != "chief/checkout" {
		t.Errorf("PRD branch = %q, want chief/checkout", git.Branch)
	}
	if got := git.BranchFor("US-001"); got != "loop/us-001-cart" {
		t.Errorf("US-001 branch = %q, want loop/us-001-cart", got)
	}
}

// Recording git state must not disturb the workflow settings sharing the file:
// they are written at different times by different parts of the application.
func TestRecordedBranches_leavesTheWorkflowIntact(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", samplePRD)

	want := PRDWorkflow{ImplementationAgent: "codex"}
	if err := s.SavePRDWorkflow("checkout", want); err != nil {
		t.Fatal(err)
	}
	if err := s.recordStoryBranch("checkout", StoryBranch{StoryID: "US-001", Branch: "loop/us-001", Base: "main"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.PRDWorkflowFor("checkout")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("workflow = %+v, want %+v", got, want)
	}
}

// The UI that renders a story's branch and pull request has existed for a while
// and showed nothing, because the detail it reads was never populated.
func TestPRD_carriesTheRecordedBranchAndPullRequest(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)
	writePRD(t, root, "checkout", samplePRD)
	if err := s.Rescan(t.Context()); err != nil {
		t.Fatal(err)
	}

	detail, err := s.PRD("checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Stories) == 0 {
		t.Fatal("the sample PRD should have stories")
	}
	first := detail.Stories[0].ID

	if err := s.recordStoryBranch("checkout", StoryBranch{StoryID: first, Branch: "loop/" + first, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	ref := PRRef{Number: 12, URL: "https://github.com/o/r/pull/12", State: "OPEN", CheckedAt: 42}
	if err := s.recordPullRequest("checkout", "loop/"+first, ref); err != nil {
		t.Fatal(err)
	}

	detail, err = s.PRD("checkout")
	if err != nil {
		t.Fatal(err)
	}
	story := detail.Stories[0]
	if story.Branch != "loop/"+first {
		t.Errorf("branch = %q, want loop/%s", story.Branch, first)
	}
	if story.PR == nil || story.PR.Number != 12 {
		t.Fatalf("PR = %+v, want #12", story.PR)
	}
	if story.PR.CheckedAt != 42 {
		t.Errorf("CheckedAt = %d, want the recorded 42", story.PR.CheckedAt)
	}
}
