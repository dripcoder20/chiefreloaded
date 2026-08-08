package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// Publishing is the action a user takes once the run is over. These tests drive
// it against a real temporary repository with a real remote and a scripted gh, so
// the argv construction and the output parsing are exercised rather than mocked
// away — a publish that builds the wrong `gh pr create` line is exactly the bug a
// fake client would hide.

// ------------------------------------------------------------------ fixture --

// publishable is a PRD whose run is over, seen by a session that did not perform
// it. Everything publishing knows came off the disk, which is the property the
// whole feature rests on.
type publishable struct {
	session *Session
	root    string
	remote  string
	gh      *scriptedGH
}

// runThenOpen runs a PRD to completion and hands back a fresh session over the
// same repository.
func runThenOpen(t *testing.T, layout BranchLayout, behaviours ...fakeagent.Behaviour) publishable {
	t.Helper()

	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", stackPRD)
	remote := addBareRemote(t, root)
	gh := scriptGH(t)

	runner := newTestSessionWith(t, fakeagent.New(behaviours...))
	if _, err := runner.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	cfg := runner.LoopConfig()
	cfg.Git.StackDriver = config.StackManual
	if err := runner.SaveLoopConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := runner.recordLayout("main", layout); err != nil {
		t.Fatal(err)
	}

	runID, err := runner.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if snap := waitFor(t, runner, runID); snap.State != StateComplete {
		t.Fatalf("state = %s, want complete (err %+v)", snap.State, snap.Error)
	}
	if calls := gh.calls(); len(calls) != 0 {
		t.Fatalf("the run reached gh: %v", calls)
	}
	return publishable{session: freshSession(t, root), root: root, remote: remote, gh: gh}
}

// publish runs the control the PRD header offers.
func (p publishable) publish(t *testing.T, draft bool) PublishReport {
	t.Helper()
	report, err := p.session.PublishPullRequest(context.Background(),
		PublishRequest{PRD: "main", Draft: draft})
	if err != nil {
		t.Fatalf("publishing: %v", err)
	}
	return report
}

// ------------------------------------------------------------ single branch --

// One branch holds everything, so one pull request against the trunk carries the
// whole PRD.
func TestPublishingASingleBranchPRDOpensOnePullRequest(t *testing.T) {
	p := runThenOpen(t, LayoutOneBranch,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	report := p.publish(t, false)

	git := state(t, p.root)
	if report.Branch != git.Branch {
		t.Errorf("published %q, want the PRD's run branch %q", report.Branch, git.Branch)
	}
	if report.Base != "main" {
		t.Errorf("base = %q, want the trunk", report.Base)
	}
	if report.Updated {
		t.Error("the first publish opened a pull request; it must not report an update")
	}
	if report.PR == nil || report.PR.URL == "" {
		t.Fatalf("report = %+v, want a pull request with a link", report)
	}
	if got := p.gh.creations(); len(got) != 1 {
		t.Errorf("gh pr create ran %d time(s), want exactly one: %v", len(got), got)
	}
	// The branch has to actually be on the remote; a pull request for a branch
	// GitHub cannot see is not a pull request.
	if !contains(remoteRefs(t, p.remote), report.Branch) {
		t.Errorf("the remote holds %v, not %q", remoteRefs(t, p.remote), report.Branch)
	}
	assertStoriesDescribed(t, p.gh.bodyFor(report.Branch), []string{"US-001", "US-002", "US-003"})
	if title := p.gh.titleFor(report.Branch); !strings.Contains(title, "Demo Project") {
		t.Errorf("title = %q, want it to name the PRD", title)
	}
}

// assertStoriesDescribed checks the body says which stories are in the pull
// request. It is the only thing a reviewer has to go on.
func assertStoriesDescribed(t *testing.T, body string, ids []string) {
	t.Helper()
	for _, id := range ids {
		if !strings.Contains(body, id) {
			t.Errorf("the description does not mention %s:\n%s", id, body)
		}
	}
}

// Progress has to be visible while this runs — it pushes and calls gh, which
// takes long enough that silence reads as a broken control — and the pull request
// has to arrive with its link.
func TestPublishingReportsItsProgressAndTheResultingLink(t *testing.T) {
	p := runThenOpen(t, LayoutOneBranch, commitsAndFinishes("a.txt"),
		commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	report := p.publish(t, true)

	var running, opened *GitEvent
	evs, _ := p.session.Replay(0)
	for i := range evs {
		if evs[i].Kind != EvGit || evs[i].Git == nil {
			continue
		}
		if evs[i].Git.State == "running" {
			running = evs[i].Git
		}
		if evs[i].Git.State == "ok" && evs[i].Git.PRURL != "" {
			opened = evs[i].Git
		}
	}
	if running == nil {
		t.Error("publishing reported no progress while it ran")
	}
	if opened == nil {
		t.Fatal("publishing reported no pull request when it finished")
	}
	if opened.PRURL != report.PR.URL || opened.PRNumber != report.PR.Number {
		t.Errorf("event reported #%d %s, want #%d %s",
			opened.PRNumber, opened.PRURL, report.PR.Number, report.PR.URL)
	}
	if !p.gh.draftFor(report.Branch) {
		t.Error("Create draft pull request opened a pull request that is not a draft")
	}
}

// ---------------------------------------------------------- branch per story --

// Each branch was cut from the one below it, so the top of the stack already
// contains every commit. One pull request for it against the trunk carries them
// all — which is what this layout must still be able to offer.
func TestPublishingAPerStoryPRDOpensOnePullRequestForTheTopOfTheStack(t *testing.T) {
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	report := p.publish(t, false)

	top := storyBranch("US-003", "Third story")
	if report.Branch != top {
		t.Errorf("published %q, want the top of the stack %q", report.Branch, top)
	}
	if report.Base != "main" {
		t.Errorf("base = %q, want the trunk", report.Base)
	}
	// Contains all of them is the claim, so it is checked against git rather than
	// inferred from the stack's shape.
	for _, file := range []string{"a.txt", "b.txt", "c.txt"} {
		if !branchHasFile(t, p.root, report.Branch, file) {
			t.Errorf("%s is missing from %q, so the pull request does not carry every story",
				file, report.Branch)
		}
	}
	if got := p.gh.creations(); len(got) != 1 {
		t.Errorf("gh pr create ran %d time(s), want exactly one: %v", len(got), got)
	}
	assertStoriesDescribed(t, p.gh.bodyFor(report.Branch), []string{"US-001", "US-002", "US-003"})
}

// A story that committed nothing has a branch at the same commit as the one below
// it. Publishing that branch would open a pull request with no changes in it, so
// the top of the stack is the highest branch that actually has a commit.
func TestPublishingSkipsATopStoryThatCommittedNothing(t *testing.T) {
	p := runThenOpen(t, LayoutBranchPerStory,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), fakeagent.Behaviour{Done: true})

	report := p.publish(t, false)

	if want := storyBranch("US-002", "Second story"); report.Branch != want {
		t.Errorf("published %q, want %q — the empty branch is not the top", report.Branch, want)
	}
	if contains(report.Stories, "US-003") {
		t.Errorf("stories = %v, want the story with no commit left out", report.Stories)
	}
}

// ------------------------------------------------------------- second press --

// Pressing again is a retry, not a second pull request. A duplicate cannot be
// un-created, so what exists is consulted before anything is opened.
func TestPublishingTwiceUpdatesTheSamePullRequest(t *testing.T) {
	p := runThenOpen(t, LayoutOneBranch,
		commitsAndFinishes("a.txt"), commitsAndFinishes("b.txt"), commitsAndFinishes("c.txt"))

	first := p.publish(t, false)
	second := p.publish(t, false)

	if got := p.gh.creations(); len(got) != 1 {
		t.Errorf("gh pr create ran %d time(s) over two publishes: %v", len(got), got)
	}
	if first.Updated {
		t.Error("the first publish reported an update; nothing existed to update")
	}
	if !second.Updated {
		t.Error("the second publish must report that it updated the existing pull request")
	}
	if second.PR == nil || first.PR == nil || second.PR.Number != first.PR.Number {
		t.Errorf("second = %+v, want the same pull request as %+v", second.PR, first.PR)
	}
	if !p.gh.edited() {
		t.Error("the existing pull request was not updated with the current description")
	}
}

// ------------------------------------------------------------- the refusals --

// Publishing while a run is live would push branches the run is still committing
// to, publishing a state the user has not seen.
func TestPublishingIsRefusedWhileTheRunIsLive(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "main", stackPRD)
	addBareRemote(t, root)
	gh := scriptGH(t)

	s := newTestSessionWith(t, fakeagent.New(fakeagent.Behaviour{Silence: 30 * time.Second}))
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	// One story has to have committed, so the refusal under test is the live run
	// and not "nothing to publish".
	writePRD(t, root, "main", strings.Replace(stackPRD,
		"### US-001: First story\n**Status:** todo", "### US-001: First story\n**Status:** done", 1))

	runID, err := s.Start(context.Background(), StartRequest{PRD: "main"})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, s, runID, StateRunning)
	defer stopRun(t, s, runID)

	_, err = s.PublishPullRequest(context.Background(), PublishRequest{PRD: "main"})
	if err == nil {
		t.Fatal("publishing during a run must be refused")
	}
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("refusal = %q, want it to say the run is live", err)
	}
	if calls := gh.calls(); len(calls) != 0 {
		t.Errorf("a refused publish reached gh: %v", calls)
	}
}

// The control is absent, not disabled, wherever publishing cannot work — and each
// case says why, because "the button is missing" is not a diagnosis.
func TestThePullRequestControlIsAbsentWherePublishingIsImpossible(t *testing.T) {
	t.Run("a project that is not a git repository", func(t *testing.T) {
		s := newTestSession(t)
		root := t.TempDir()
		if _, err := s.OpenProject(context.Background(), root); err != nil {
			t.Fatal(err)
		}
		writePRD(t, root, "main", donePRD)
		assertNoOffer(t, s.PublishOfferFor("main"), "not a git repository")
	})

	t.Run("git mode off", func(t *testing.T) {
		s := newTestSession(t)
		root := t.TempDir()
		gitInit(t, root)
		if _, err := s.OpenProject(context.Background(), root); err != nil {
			t.Fatal(err)
		}
		writePRD(t, root, "main", donePRD)
		cfg := s.LoopConfig()
		cfg.Git.Mode = config.GitModeOff
		if err := s.SaveLoopConfig(cfg); err != nil {
			t.Fatal(err)
		}
		assertNoOffer(t, s.PublishOfferFor("main"), "git mode is off")
	})

	t.Run("no story has committed", func(t *testing.T) {
		s := newTestSession(t)
		root := t.TempDir()
		gitInit(t, root)
		if _, err := s.OpenProject(context.Background(), root); err != nil {
			t.Fatal(err)
		}
		writePRD(t, root, "main", stackPRD)
		assertNoOffer(t, s.PublishOfferFor("main"), "nothing to publish")
	})
}

// donePRD is a PRD whose first story has committed, which is what makes the
// control appear at all.
const donePRD = `# Demo Project

## User Stories

### US-001: First story
**Status:** done
**Priority:** 1
**Description:** Do the first thing.
- [x] It works
`

// A PRD with a committed story in a git repository is the case the control is
// for, so it has to be offered there.
func TestThePullRequestControlIsOfferedOnceAStoryHasCommitted(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	writePRD(t, root, "main", donePRD)

	offer := s.PublishOfferFor("main")
	if !offer.Available {
		t.Fatalf("offer = %+v, want the control available", offer)
	}
	if offer.Layout != LayoutOneBranch {
		t.Errorf("layout = %q, want the recorded default", offer.Layout)
	}
}

func assertNoOffer(t *testing.T, offer PublishOffer, reason string) {
	t.Helper()
	if offer.Available {
		t.Fatalf("offer = %+v, want the control absent", offer)
	}
	if !strings.Contains(offer.Reason, reason) {
		t.Errorf("reason = %q, want it to mention %q", offer.Reason, reason)
	}
}

// ------------------------------------------------------ the plan on its own --

// The branch a PRD publishes is read from the record, and the two layouts read it
// from different places.
func TestTheBranchToPublishComesFromTheRecordedLayout(t *testing.T) {
	stack := PRDGitState{
		Branch: "chief/main",
		Branches: []StoryBranch{
			{StoryID: "US-001", Branch: "loop/a", Base: "main"},
			{StoryID: "US-002", Branch: "loop/b", Base: "loop/a", NoCommit: true},
			{StoryID: "US-003", Branch: "loop/c", Base: "loop/b"},
		},
	}

	if got, err := publishableBranch(stack, LayoutOneBranch); err != nil || got != "chief/main" {
		t.Errorf("one-branch = (%q, %v), want the run branch", got, err)
	}
	if got, err := publishableBranch(stack, LayoutBranchPerStory); err != nil || got != "loop/c" {
		t.Errorf("per-story = (%q, %v), want the top of the stack", got, err)
	}
}

// A stack whose only branches committed nothing has nothing to publish, and
// saying so beats opening an empty pull request.
func TestAStackWithNothingCommittedNamesNoBranch(t *testing.T) {
	_, err := publishableBranch(PRDGitState{
		Branches: []StoryBranch{{StoryID: "US-001", Branch: "loop/a", NoCommit: true}},
	}, LayoutBranchPerStory)
	if err == nil {
		t.Fatal("a stack with no commit must not name a branch to publish")
	}
}

// ------------------------------------------------------------- scripted gh --

// scriptedGH is a gh on PATH that behaves like the real one for the calls
// publishing makes, and records every invocation.
//
// It keeps one pull request per head branch, which is what a stack needs and what
// GitHub does. A second `gh pr create` for the same branch fails, exactly as
// GitHub does — so a duplicate would show up as a failure rather than passing
// quietly.
type scriptedGH struct {
	dir string
	log string
}

func scriptGH(t *testing.T) *scriptedGH {
	t.Helper()

	bin := t.TempDir()
	state := t.TempDir()
	log := filepath.Join(state, "calls.log")

	script := strings.NewReplacer("@STATE@", state, "@LOG@", log).Replace(ghScript)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &scriptedGH{dir: state, log: log}
}

// ghScript answers the four calls publishing makes: listing a branch's pull
// request, creating one, patching its title and body, and nothing else — an
// unexpected invocation fails loudly rather than being absorbed.
//
// State is kept per head branch, keyed by the branch name with its slashes
// replaced, because a stack has one pull request per branch and they must not be
// able to see each other's numbers, bases or bodies.
const ghScript = `#!/bin/sh
printf '%s\n' "$*" >> @LOG@
D=@STATE@
prev=""
head=""
base=""
title=""
for arg in "$@"; do
  case "$prev" in
    --head) head="$arg" ;;
    --base) base="$arg" ;;
    --title) title="$arg" ;;
  esac
  prev="$arg"
done
slug=$(printf '%s' "$head" | tr '/' '-')

if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  if [ -n "$slug" ] && [ -f "$D/pr-$slug" ]; then
    cat "$D/pr-$slug"
  else
    printf '[]\n'
  fi
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  if [ -f "$D/fail-$slug" ]; then
    echo "could not reach github.com" >&2
    exit 1
  fi
  if [ -f "$D/pr-$slug" ]; then
    echo "a pull request for $head already exists" >&2
    exit 1
  fi
  number=$(( $(cat "$D/counter" 2>/dev/null || printf '6') + 1 ))
  printf '%s' "$number" > "$D/counter"
  case " $* " in
    *" --draft "*) draft=true ;;
    *) draft=false ;;
  esac
  cat > "$D/body-$slug"
  printf '%s' "$title" > "$D/title-$slug"
  printf '%s' "$base" > "$D/base-$slug"
  printf '%s' "$draft" > "$D/draft-$slug"
  printf '[{"number":%s,"url":"https://github.com/acme/app/pull/%s","state":"OPEN","isDraft":%s,"baseRefName":"%s","headRefName":"%s"}]\n' \
    "$number" "$number" "$draft" "$base" "$head" > "$D/pr-$slug"
  printf '%s\n' "$*" >> "$D/creations"
  printf '%s\n' "$slug" >> "$D/order"
  exit 0
fi

if [ "$1" = "api" ]; then
  cat > "$D/patch"
  exit 0
fi

echo "unexpected gh invocation: $*" >&2
exit 1
`

func (g *scriptedGH) calls() []string { return nonEmptyLines(readOrEmpty(g.log)) }

// slug is how the script keys a branch's state.
func slug(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

// bodyFor, baseFor and titleFor read what one branch's pull request was created
// with, which is the only way to check a stack layer by layer.
func (g *scriptedGH) bodyFor(branch string) string {
	return readOrEmpty(filepath.Join(g.dir, "body-"+slug(branch)))
}

func (g *scriptedGH) baseFor(branch string) string {
	return readOrEmpty(filepath.Join(g.dir, "base-"+slug(branch)))
}

func (g *scriptedGH) titleFor(branch string) string {
	return readOrEmpty(filepath.Join(g.dir, "title-"+slug(branch)))
}

// createdOrder is the branches pull requests were created for, in the order they
// were created. A stack is an order, so it is asserted as one.
func (g *scriptedGH) createdOrder() []string {
	return nonEmptyLines(readOrEmpty(filepath.Join(g.dir, "order")))
}

// creations is every `gh pr create`. Its length is the duplicate check.
func (g *scriptedGH) creations() []string {
	return nonEmptyLines(readOrEmpty(filepath.Join(g.dir, "creations")))
}

// edited reports whether the pull request's title and body were patched, which is
// what updating an existing one amounts to.
func (g *scriptedGH) edited() bool { return readOrEmpty(filepath.Join(g.dir, "patch")) != "" }

// failCreating makes `gh pr create` fail for one branch, the way an unreachable
// GitHub does: the branch is pushed, nothing is created, and the pull request that
// was not created is still absent from `gh pr list`. Undone by allowCreating,
// which is what a retry after the outage looks like.
func (g *scriptedGH) failCreating(t *testing.T, branch string) {
	t.Helper()
	if err := os.WriteFile(g.failPath(branch), []byte("fail"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (g *scriptedGH) allowCreating(t *testing.T, branch string) {
	t.Helper()
	if err := os.Remove(g.failPath(branch)); err != nil {
		t.Fatal(err)
	}
}

func (g *scriptedGH) failPath(branch string) string {
	return filepath.Join(g.dir, "fail-"+slug(branch))
}

// draftFor reports whether a branch's pull request was opened as a draft.
func (g *scriptedGH) draftFor(branch string) bool {
	return readOrEmpty(filepath.Join(g.dir, "draft-"+slug(branch))) == "true"
}

func readOrEmpty(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// branchHasFile reports whether a branch's tree contains a path.
func branchHasFile(t *testing.T, root, branch, file string) bool {
	t.Helper()
	return gitRun(context.Background(), root, "cat-file", "-e", branch+":"+file) == nil
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
