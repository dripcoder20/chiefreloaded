package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// A run's whole effect is local. These tests are the guard on that: they give a
// run everything it would need to publish — a remote it can really push to, a gh
// it can really call — and then assert it used neither.
//
// Asserting against a repository with no remote would prove only that there was
// nowhere to push.

// localOnlyRun is a run that had every opportunity to reach GitHub.
type localOnlyRun struct {
	session *Session
	root    string
	remote  string
	// ghCalls is every invocation of gh the run made, in order.
	ghCalls func() []string
	runID   string
}

// startRunAgainstARemote starts a run in a repository with a working remote and a
// gh on PATH, under the given layout.
//
// The stack driver is pinned to manual for the same reason startPerStoryRun pins
// it: the machine running the tests may or may not have the gh-stack extension,
// and manual is what a user without it gets. Manual is also the driver that
// pushes with plain git, so a leak would show up on the remote even if gh were
// never called.
func startRunAgainstARemote(t *testing.T, layout BranchLayout, behaviours ...fakeagent.Behaviour) localOnlyRun {
	t.Helper()

	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", stackPRD)
	remote := addBareRemote(t, root)
	ghCalls := interceptGH(t)

	s := newTestSessionWith(t, fakeagent.New(behaviours...))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	cfg := s.LoopConfig()
	cfg.Git.StackDriver = config.StackManual
	if err := s.SaveLoopConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := s.recordLayout("main", layout); err != nil {
		t.Fatal(err)
	}

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return localOnlyRun{session: s, root: root, remote: remote, ghCalls: ghCalls, runID: runID}
}

// finish waits for the run to complete and reports what reached the remote.
func (lr localOnlyRun) finish(t *testing.T) {
	t.Helper()
	if snap := waitFor(t, lr.session, lr.runID); snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}
	if refs := remoteRefs(t, lr.remote); len(refs) != 0 {
		t.Errorf("the remote holds %v; a run must push nothing", refs)
	}
	if calls := lr.ghCalls(); len(calls) != 0 {
		t.Errorf("gh was invoked %v; a run must open no pull request", calls)
	}
}

// Every story gets its own branch, and every one of them stays at home.
func TestAPerStoryRunAgainstARemotePushesNothing(t *testing.T) {
	lr := startRunAgainstARemote(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))
	lr.finish(t)

	branches := recordedBranches(t, lr.root, "main")
	if len(branches) != 3 {
		t.Fatalf("recorded %+v, want one entry per story", branches)
	}

	// Removing the publishing events must not take the branch events with them:
	// they are how the user sees where their work is landing.
	announced := announcedBranches(lr.session)
	for _, b := range branches {
		assertLocalOnly(t, lr, b.Branch)
		if !announced[b.Branch] {
			t.Errorf("branch %q was created without being reported", b.Branch)
		}
	}
}

// announcedBranches is every branch the run named on the event stream.
func announcedBranches(s *Session) map[string]bool {
	out := map[string]bool{}
	evs, _ := s.Replay(0)
	for _, ev := range evs {
		if ev.Kind == EvGit && ev.Git != nil && ev.Git.Branch != "" {
			out[ev.Git.Branch] = true
		}
	}
	return out
}

// The single-branch layout has one branch rather than three, and the same rule
// applies to it.
func TestASingleBranchRunAgainstARemotePushesNothing(t *testing.T) {
	lr := startRunAgainstARemote(t, LayoutOneBranch,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))
	lr.finish(t)

	git := state(t, lr.root)
	if git.Branch == "" {
		t.Fatal("the run branch was not recorded")
	}
	assertLocalOnly(t, lr, git.Branch)

	// No story owns a branch under this layout, so nothing here may claim one.
	for _, b := range git.StoryBranches() {
		if b.HasBranch() {
			t.Errorf("%s claims branch %q under a single-branch layout", b.StoryID, b.Branch)
		}
	}
}

// assertLocalOnly is the property the whole story exists for: the branch is in
// the user's repository and nowhere else.
func assertLocalOnly(t *testing.T, lr localOnlyRun, branch string) {
	t.Helper()
	if exists, _ := branchExists(context.Background(), lr.root, branch); !exists {
		t.Errorf("branch %q is not in the repository the run was for", branch)
	}
	for _, ref := range remoteRefs(t, lr.remote) {
		if ref == branch {
			t.Errorf("branch %q reached the remote", branch)
		}
	}
}

// The description has to be composed while the story still says what was
// verified. SetStoryStatus(id, "done") ticks every acceptance criterion, so a
// description composed afterwards records that write instead — and says so, which
// is what this asserts against.
func TestAStoryDescriptionIsComposedBeforeItsCriteriaAreTicked(t *testing.T) {
	lr := startRunAgainstARemote(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))
	lr.finish(t)

	body := storedBody(t, lr.root, "US-001")
	if !strings.Contains(body, "when the story was verified") {
		t.Errorf("description does not record criteria as verified-time:\n%s", body)
	}
	if strings.Contains(body, "after the story was marked done") {
		t.Errorf("description was composed after the status write:\n%s", body)
	}
	for _, want := range []string{"Do the first thing.", "It works", "US-001"} {
		if !strings.Contains(body, want) {
			t.Errorf("description is missing %q:\n%s", want, body)
		}
	}
}

// The single-branch layout gives no story a branch, but every story's description
// still has to be captured — it is what the PRD's own pull request is built from.
func TestASingleBranchRunStoresADescriptionPerStory(t *testing.T) {
	lr := startRunAgainstARemote(t, LayoutOneBranch,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))
	lr.finish(t)

	for _, id := range []string{"US-001", "US-002", "US-003"} {
		if body := storedBody(t, lr.root, id); body == "" {
			t.Errorf("%s has no stored description", id)
		}
	}
}

// storedBody reads a story's stored description through a session that performed
// no run, which is the only way publishing will ever see it.
func storedBody(t *testing.T, root, storyID string) string {
	t.Helper()
	for _, b := range recordedBranches(t, root, "main") {
		if b.StoryID == storyID {
			return b.PullRequestBody
		}
	}
	return ""
}

// A description composed after the status write must not present the ticked boxes
// as evidence. Nothing does that any more, but the wording is the only signal a
// reader has, so it is worth pinning.
func TestADescriptionReadAfterTheStatusWriteSaysSo(t *testing.T) {
	body := prBody(prBodyDraft{
		Story: StorySnap{
			ID: "US-001", Criteria: []string{"It works"},
			CriteriaAreAuthoritative: false,
		},
	})
	if !strings.Contains(body, "after the story was marked done") {
		t.Errorf("a late description must disclaim its checklist:\n%s", body)
	}
}

// A story with no base recorded — the single-branch layout before a run branch
// exists — must not invent one in the trailer.
func TestADescriptionWithNoBaseNamesNone(t *testing.T) {
	body := prBody(prBodyDraft{Story: StorySnap{ID: "US-001"}})
	if strings.Contains(body, "Based on") {
		t.Errorf("a description with no base must not claim one:\n%s", body)
	}
	if !strings.Contains(body, "US-001 · prepared by Loop") {
		t.Errorf("trailer = %q, want the story and the tool", body)
	}
}

// ------------------------------------------------------------------ helpers --

// addBareRemote gives root a remote that pushes really do succeed against, so a
// test asserting nothing was pushed is testing restraint rather than absence.
func addBareRemote(t *testing.T, root string) string {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "-q", "--bare", "-b", "main")
	runGit(t, root, "remote", "add", "origin", remote)
	return remote
}

// remoteRefs is every branch on the remote.
func remoteRefs(t *testing.T, remote string) []string {
	t.Helper()
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname:short)", "refs/heads")
	cmd.Dir = remote
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing the remote's branches: %v", err)
	}
	return nonEmptyLines(string(out))
}

// interceptGH puts a gh on PATH that records how it was called and then fails.
// Anything reaching for GitHub is caught, and nothing gets there.
func interceptGH(t *testing.T) func() []string {
	t.Helper()

	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return func() []string {
		raw, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return nonEmptyLines(string(raw))
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
