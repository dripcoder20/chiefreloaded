package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dripcoder/loop/internal/tracker"
)

// twoStoryPRD is a document with the structure the reference writer targets:
// a description, then acceptance criteria, per story.
const twoStoryPRD = `# PRD: Checkout

## 3. User Stories

### US-001: First story
**Status:** todo
**Priority:** 1
**Description:** As a user, I want the first thing.

**Acceptance Criteria:**
- [ ] It works

### US-002: Second story
**Status:** todo
**Priority:** 2
**Description:** As a user, I want the second thing.

**Acceptance Criteria:**
- [ ] It also works
`

// fakeTracker records what it was asked to create and can be told to fail.
type fakeTracker struct {
	mu       sync.Mutex
	created  []tracker.Issue
	failOn   map[string]error
	nextID   int
	notReady string
}

func (f *fakeTracker) Name() string { return "Fake Tracker" }

func (f *fakeTracker) Available(context.Context, string) (bool, string) {
	if f.notReady != "" {
		return false, f.notReady
	}
	return true, ""
}

func (f *fakeTracker) Create(_ context.Context, _ string, issue tracker.Issue) (tracker.Created, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for prefix, err := range f.failOn {
		if strings.HasPrefix(issue.Title, prefix) {
			return tracker.Created{}, err
		}
	}
	f.created = append(f.created, issue)
	f.nextID++
	return tracker.Created{
		Identifier: "FAKE-" + string(rune('0'+f.nextID)),
		URL:        "https://tracker.example/issue/" + string(rune('0'+f.nextID)),
	}, nil
}

func (f *fakeTracker) titles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.created))
	for _, i := range f.created {
		out = append(out, i.Title)
	}
	return out
}

// openPublishingProject opens a project whose PRD publishes to a fake tracker.
func openPublishingProject(t *testing.T, fake *fakeTracker) (*Session, string) {
	t.Helper()
	s, err := New(Options{
		Clock: fixedClock(),
		Probe: func(context.Context) Environment { return Environment{} },
		Tracker: func(IssueDestination) (tracker.Client, error) {
			return fake, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AutoAnswer(true)
	t.Cleanup(func() { s.bus.stop() })

	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", twoStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePRDWorkflow("checkout", PRDWorkflow{IssueDestination: IssueGitHub}); err != nil {
		t.Fatal(err)
	}
	return s, root
}

// branchList reports the repository's local branches.
func branchList(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

func readPRD(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".chief", "prds", "checkout", "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// --- workflow metadata ------------------------------------------------------

// Every PRD written before the sidecar existed has none; that is the norm, not
// an error.
func TestPRDWorkflowDefaultsWhenNoSidecarExists(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", twoStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	w, err := s.PRDWorkflowFor("checkout")
	if err != nil {
		t.Fatalf("a PRD with no sidecar must open cleanly: %v", err)
	}
	if w.StackPerStory {
		t.Error("stacked PRs must be off by default")
	}
	if w.IssueDestination != IssueNone {
		t.Errorf("issue destination = %q, want none by default", w.IssueDestination)
	}
}

func TestPRDWorkflowRoundTrips(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", twoStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	want := PRDWorkflow{
		ImplementationAgent: "codex",
		StackPerStory:       true,
		IssueDestination:    IssueLinear,
	}
	if err := s.SavePRDWorkflow("checkout", want); err != nil {
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

// Saving workflow settings configures the later run; it must not start one or
// create any branch or pull request.
func TestSavePRDWorkflowStartsNothing(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", twoStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	if err := s.SavePRDWorkflow("checkout", PRDWorkflow{StackPerStory: true}); err != nil {
		t.Fatal(err)
	}
	if runs := s.Runs(); len(runs) != 0 {
		t.Errorf("got %d runs, want none started", len(runs))
	}
	if branches := branchList(t, root); len(branches) != 1 {
		t.Errorf("branches = %v, want only the initial one", branches)
	}
}

// A saved agent that is no longer installed must be refused, not silently
// replaced: running a different agent than the PRD was configured for is worse
// than saying so.
func TestResolveImplementationAgentRefusesAMissingSavedAgent(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", twoStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePRDWorkflow("checkout", PRDWorkflow{ImplementationAgent: "nonexistent"}); err != nil {
		t.Fatal(err)
	}

	agent, err := s.ResolveImplementationAgent("checkout")
	if err == nil {
		t.Fatalf("expected a refusal, got agent %q", agent)
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q should name the missing agent", err)
	}
}

func TestResolveImplementationAgentFallsBackToTheConfiguredDefault(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", twoStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	got, err := s.ResolveImplementationAgent("checkout")
	if err != nil {
		t.Fatal(err)
	}
	if got != s.DefaultImplementationAgent() {
		t.Errorf("agent = %q, want the configured default %q", got, s.DefaultImplementationAgent())
	}
}

// --- publishing -------------------------------------------------------------

func TestPublishCreatesOneIssuePerStoryAndRecordsIt(t *testing.T) {
	fake := &fakeTracker{}
	s, root := openPublishingProject(t, fake)

	report, err := s.PublishIssues(t.Context(), "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("got %d results, want one per story", len(report.Results))
	}
	if len(report.Failed) != 0 {
		t.Errorf("failed = %v, want none", report.Failed)
	}
	if titles := fake.titles(); len(titles) != 2 {
		t.Fatalf("created %v, want exactly one issue per story", titles)
	}

	// The story's own title and id identify the issue, and the criteria travel
	// with it so the tracker item stands alone.
	if got := fake.created[0].Title; !strings.Contains(got, "US-001") {
		t.Errorf("title %q should carry the story id", got)
	}
	if got := fake.created[0].Body; !strings.Contains(got, "It works") {
		t.Errorf("body %q should include the acceptance criteria", got)
	}

	doc := readPRD(t, root)
	if strings.Count(doc, "**External Issue:**") != 2 {
		t.Errorf("PRD should carry one reference per story:\n%s", doc)
	}
	refs, err := s.PublishedIssues("checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Errorf("recorded %d references, want 2", len(refs))
	}
}

// The reference must go under the story's description and above its acceptance
// criteria, on the story it belongs to.
func TestPublishWritesTheReferenceToTheMatchingStory(t *testing.T) {
	fake := &fakeTracker{}
	s, root := openPublishingProject(t, fake)
	if _, err := s.PublishIssues(t.Context(), "checkout"); err != nil {
		t.Fatal(err)
	}

	doc := readPRD(t, root)
	first := strings.Index(doc, "### US-001")
	second := strings.Index(doc, "### US-002")
	criteria := strings.Index(doc, "**Acceptance Criteria:**")
	ref := strings.Index(doc, "**External Issue:**")

	if ref < first || ref > second {
		t.Errorf("the first reference must sit inside US-001's block:\n%s", doc)
	}
	if ref > criteria {
		t.Errorf("the reference must precede the acceptance criteria:\n%s", doc)
	}
}

// A retry after a partial failure must create issues only for the stories that
// still have none — a duplicate issue cannot be un-created.
func TestPublishRetryCreatesNoDuplicates(t *testing.T) {
	fake := &fakeTracker{failOn: map[string]error{"US-002": errors.New("rate limited")}}
	s, root := openPublishingProject(t, fake)

	first, err := s.PublishIssues(t.Context(), "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Failed) != 1 || first.Failed[0] != "US-002" {
		t.Fatalf("failed = %v, want exactly US-002", first.Failed)
	}
	if len(fake.titles()) != 1 {
		t.Fatalf("created %v, want only the story that succeeded", fake.titles())
	}

	// The transient failure clears and the user retries.
	fake.failOn = nil
	second, err := s.PublishIssues(t.Context(), "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Failed) != 0 {
		t.Errorf("failed = %v, want none after the retry", second.Failed)
	}
	if titles := fake.titles(); len(titles) != 2 {
		t.Fatalf("created %v, want one issue per story and no duplicate", titles)
	}
	// US-001 already had an issue, so the retry must have skipped it.
	for _, r := range second.Results {
		if r.StoryID == "US-001" && !r.Skipped {
			t.Error("US-001 already had an issue; the retry must not create another")
		}
	}
	if n := strings.Count(readPRD(t, root), "**External Issue:**"); n != 2 {
		t.Errorf("PRD carries %d references, want exactly 2", n)
	}
}

// A tracker outage must never cost the user the generated PRD.
func TestPublishFailurePreservesThePRD(t *testing.T) {
	fake := &fakeTracker{failOn: map[string]error{"US-": errors.New("tracker is down")}}
	s, root := openPublishingProject(t, fake)
	before := readPRD(t, root)

	report, err := s.PublishIssues(t.Context(), "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failed) != 2 {
		t.Errorf("failed = %v, want both stories", report.Failed)
	}
	if readPRD(t, root) != before {
		t.Error("a total publishing failure must leave the PRD untouched")
	}
	if _, err := s.PRD("checkout"); err != nil {
		t.Errorf("the PRD must still parse after a failed publish: %v", err)
	}
}

// Selecting "do not publish" must perform no tracker write at all.
func TestPublishRefusedWhenNoDestinationIsSelected(t *testing.T) {
	fake := &fakeTracker{}
	s, _ := openPublishingProject(t, fake)
	if err := s.SavePRDWorkflow("checkout", PRDWorkflow{IssueDestination: IssueNone}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PublishIssues(t.Context(), "checkout"); err == nil {
		t.Error("expected publishing to be refused with no destination selected")
	}
	if titles := fake.titles(); len(titles) != 0 {
		t.Errorf("created %v, want no tracker writes at all", titles)
	}
}

// An unconfigured destination is still listed so the UI can explain the setup
// rather than silently omitting the option.
func TestIssueDestinationsExplainMissingConfiguration(t *testing.T) {
	fake := &fakeTracker{notReady: "set LINEAR_API_KEY to a Linear personal API key"}
	s, _ := openPublishingProject(t, fake)

	statuses := s.IssueDestinations(t.Context())
	if len(statuses) != 2 {
		t.Fatalf("got %d destinations, want both listed", len(statuses))
	}
	for _, st := range statuses {
		if st.Available {
			t.Errorf("%s should be unavailable", st.Name)
		}
		if st.Reason == "" {
			t.Errorf("%s must say what configuration is missing", st.Name)
		}
	}
}

// --- reference insertion ----------------------------------------------------

func TestInsertIssueReferenceIsIdempotent(t *testing.T) {
	ref := IssueRef{Destination: IssueLinear, Identifier: "DEV-1", URL: "https://example/1"}
	once, ok := insertIssueReference(twoStoryPRD, "US-001", ref)
	if !ok {
		t.Fatal("expected the story to be found")
	}

	// Re-publishing replaces the line rather than stacking a second one.
	newer := IssueRef{Destination: IssueLinear, Identifier: "DEV-9", URL: "https://example/9"}
	twice, ok := insertIssueReference(once, "US-001", newer)
	if !ok {
		t.Fatal("expected the story to be found again")
	}
	if n := strings.Count(twice, "**External Issue:**"); n != 1 {
		t.Errorf("got %d reference lines, want exactly 1:\n%s", n, twice)
	}
	if !strings.Contains(twice, "DEV-9") || strings.Contains(twice, "DEV-1") {
		t.Errorf("the newer reference should have replaced the older:\n%s", twice)
	}
}

func TestInsertIssueReferenceReportsAnUnknownStory(t *testing.T) {
	ref := IssueRef{Identifier: "DEV-1", URL: "https://example/1"}
	if _, ok := insertIssueReference(twoStoryPRD, "US-404", ref); ok {
		t.Error("expected an unknown story to be reported rather than written anywhere")
	}
}

// Writing a reference must never touch a story it does not belong to.
func TestInsertIssueReferenceLeavesOtherStoriesAlone(t *testing.T) {
	ref := IssueRef{Identifier: "DEV-1", URL: "https://example/1"}
	got, ok := insertIssueReference(twoStoryPRD, "US-002", ref)
	if !ok {
		t.Fatal("expected the story to be found")
	}
	at := strings.Index(got, "**External Issue:**")
	if at < strings.Index(got, "### US-002") {
		t.Errorf("the reference landed outside US-002's block:\n%s", got)
	}
	if !strings.Contains(got, "As a user, I want the first thing.") {
		t.Error("US-001's content must be untouched")
	}
}
