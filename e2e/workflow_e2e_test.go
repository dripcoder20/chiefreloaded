//go:build e2e

package e2e

// End-to-end coverage for the per-PRD workflow: which agent implements a PRD,
// and whether it stacks a pull request per story.
//
// The sidecar is written directly rather than through an API, because that file
// is the persisted contract these tests are about.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkflow writes a PRD's sidecar directly. That file is the persisted
// contract this story defines, so writing it here is the test asserting the
// on-disk representation rather than going through an API that could change.
func writeWorkflow(t *testing.T, root, prd string, workflow map[string]any) {
	t.Helper()

	raw, err := json.MarshalIndent(map[string]any{
		"version":  1,
		"workflow": workflow,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".chief", "prds", prd, "loop.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// workflowJSON is what `loopctl workflow -json` emits.
type workflowJSON struct {
	Workflow struct {
		ImplementationAgent string `json:"implementationAgent"`
	} `json:"workflow"`
	ResolvedAgent string `json:"resolvedAgent"`
	ResolveError  string `json:"resolveError"`
	Git           struct {
		Layout string `json:"layout"`
		Branch string `json:"branch"`
		Base   string `json:"base"`
	} `json:"git"`
	Branches   []storyBranchJSON `json:"branches"`
	BasesKnown bool              `json:"basesKnown"`
}

// storyBranchJSON is one story's recorded branch as another process reads it.
type storyBranchJSON struct {
	StoryID  string `json:"storyId"`
	Branch   string `json:"branch"`
	Base     string `json:"base"`
	NoCommit bool   `json:"noCommit"`
}

func readWorkflow(t *testing.T, root, prd string) workflowJSON {
	t.Helper()
	out, err := runCtl(t, root, "workflow", prd, "-json")
	if err != nil {
		t.Fatalf("loopctl workflow: %v\n%s", err, out)
	}
	var got workflowJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoding workflow output: %v\n%s", err, out)
	}
	return got
}

// A PRD with no sidecar — every PRD written before the sidecar existed — must
// still open and report the configured defaults.
func TestWorkflowDefaultsForAPRDWithNoSidecar(t *testing.T) {
	root := newProject(t)

	got := readWorkflow(t, root, "main")
	if got.ResolveError != "" {
		t.Errorf("resolving the agent must succeed with no sidecar: %s", got.ResolveError)
	}
}

// The chosen agents are saved with the PRD and survive a fresh process, which
// is the whole point of persisting them.
func TestDistinctPhaseAgentsPersistAcrossProcesses(t *testing.T) {
	root := newProject(t)

	// A project configured to author with one agent and implement with another.
	config := "agents:\n  authoring: claude\n  implementation: codex\n"
	if err := os.WriteFile(filepath.Join(root, ".chief", "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, root, "main", map[string]any{"implementationAgent": "codex"})

	got := readWorkflow(t, root, "main")
	if got.Workflow.ImplementationAgent != "codex" {
		t.Errorf("implementation agent = %q, want codex", got.Workflow.ImplementationAgent)
	}
}

// The point of writing the branch record to disk is that a process which
// performed none of the run can reconstruct what the run did. This is that
// process: the run has exited by the time loopctl is invoked again.
func TestARunsBranchRecordIsReadableByAnotherProcess(t *testing.T) {
	root := newProject(t)

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	got := readWorkflow(t, root, "main")
	if got.Git.Layout != "one-branch" {
		t.Errorf("layout = %q, want one-branch", got.Git.Layout)
	}
	if got.Git.Branch != "chief/main" {
		t.Errorf("run branch = %q, want chief/main", got.Git.Branch)
	}
	if got.Git.Base != "main" {
		t.Errorf("run branch base = %q, want the branch it was cut from", got.Git.Base)
	}
}

// A run's whole effect is local. Given a remote it could really push to, a
// complete run must still leave it empty and its own branch present.
func TestAFullRunAgainstARemotePublishesNothing(t *testing.T) {
	root := newProject(t)
	remote := addRemote(t, root)

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	branch := readWorkflow(t, root, "main").Git.Branch
	if branch == "" {
		t.Fatal("the run recorded no branch")
	}
	if !localBranchExists(t, root, branch) {
		t.Errorf("branch %q is not in the repository the run was for", branch)
	}
	if refs := remoteBranches(t, remote); len(refs) != 0 {
		t.Errorf("the remote holds %v; a run must push nothing", refs)
	}
}

// addRemote gives root a remote that pushes really do succeed against, so a test
// asserting nothing was pushed is testing restraint rather than absence.
func addRemote(t *testing.T, root string) string {
	t.Helper()
	remote := t.TempDir()
	gitIn(t, remote, "init", "-q", "--bare", "-b", "main")
	gitIn(t, root, "remote", "add", "origin", remote)
	return remote
}

func remoteBranches(t *testing.T, remote string) []string {
	t.Helper()
	out := gitIn(t, remote, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	return strings.Fields(out)
}

func localBranchExists(t *testing.T, root, branch string) bool {
	t.Helper()
	return strings.Contains(gitIn(t, root, "for-each-ref", "--format=%(refname:short)", "refs/heads"), branch)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// A stack the run recorded must come back in the order it was created, with the
// story that committed nothing marked as having nothing to publish. The sidecar is
// written directly here: it is the persisted contract, and asserting on it from a
// separate process is what proves the record stands on its own.
func TestARecordedStackIsReadBackInOrderByAnotherProcess(t *testing.T) {
	root := newProject(t)
	writeGitRecord(t, root, "main", map[string]any{
		"layout": "branch-per-story",
		"branches": []map[string]any{
			{"storyId": "US-001", "branch": "loop/main/us-001", "base": "main"},
			{"storyId": "US-002", "branch": "loop/main/us-002", "base": "loop/main/us-001", "noCommit": true},
			{"storyId": "US-003", "branch": "loop/main/us-003", "base": "loop/main/us-001"},
		},
	})

	got := readWorkflow(t, root, "main")
	if !got.BasesKnown {
		t.Error("every branch in this record has a base")
	}
	want := []storyBranchJSON{
		{StoryID: "US-001", Branch: "loop/main/us-001", Base: "main"},
		{StoryID: "US-002", Branch: "loop/main/us-002", Base: "loop/main/us-001", NoCommit: true},
		{StoryID: "US-003", Branch: "loop/main/us-003", Base: "loop/main/us-001"},
	}
	if len(got.Branches) != len(want) {
		t.Fatalf("branches = %+v, want %d in creation order", got.Branches, len(want))
	}
	for i := range want {
		if got.Branches[i] != want[i] {
			t.Errorf("branch %d = %+v, want %+v", i, got.Branches[i], want[i])
		}
	}
}

// A sidecar from before bases were recorded must open, and must say that it cannot
// answer what each branch was based on rather than inventing an answer.
func TestALegacyBranchRecordReportsUnknownBasesToAnotherProcess(t *testing.T) {
	root := newProject(t)
	writeGitRecord(t, root, "main", map[string]any{
		"layout":  "branch-per-story",
		"stories": map[string]string{"US-002": "loop/main/us-002", "US-001": "loop/main/us-001"},
	})

	got := readWorkflow(t, root, "main")
	if got.BasesKnown {
		t.Error("a record with no bases must not claim to know them")
	}
	if len(got.Branches) != 2 {
		t.Fatalf("branches = %+v, want both stories read from the older shape", got.Branches)
	}
	for _, b := range got.Branches {
		if b.Base != "" {
			t.Errorf("%s claims a base of %q that was never recorded", b.StoryID, b.Base)
		}
	}
}

// writeGitRecord writes the `git` half of a PRD's sidecar directly, which is the
// persisted contract these tests are about.
func writeGitRecord(t *testing.T, root, prd string, git map[string]any) {
	t.Helper()

	raw, err := json.MarshalIndent(map[string]any{"version": 1, "git": git}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".chief", "prds", prd, "loop.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A saved agent that is no longer installed must be refused, not replaced: the
// PRD was configured to be implemented by a particular agent.
func TestAMissingSavedAgentIsReportedRatherThanSubstituted(t *testing.T) {
	root := newProject(t)
	writeWorkflow(t, root, "main", map[string]any{"implementationAgent": "nonexistent-agent"})

	got := readWorkflow(t, root, "main")
	if got.ResolveError == "" {
		t.Fatalf("expected the missing agent to be reported, got resolved %q", got.ResolvedAgent)
	}
	if !strings.Contains(got.ResolveError, "nonexistent-agent") {
		t.Errorf("the error should name the missing agent: %s", got.ResolveError)
	}
}

// ----------------------------------------------------------- publishing --

// publishJSON is what `loopctl publish -json` emits.
type publishJSON struct {
	PRD     string   `json:"prd"`
	Branch  string   `json:"branch"`
	Base    string   `json:"base"`
	Stories []string `json:"stories"`
	Updated bool     `json:"updated"`
	PR      *struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	} `json:"pr"`
}

// Publishing is a separate process from the run, which is the point: the branch
// record and the stored descriptions are all it has, and they came off the disk.
func TestPublishingAFinishedPRDOpensOnePullRequestFromAnotherProcess(t *testing.T) {
	root := newProject(t)
	remote := addRemote(t, root)
	gh := scriptGitHub(t)

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	first := publish(t, root)
	if first.Branch != "chief/main" {
		t.Errorf("published %q, want the PRD's run branch", first.Branch)
	}
	if first.Base != "main" {
		t.Errorf("base = %q, want the trunk", first.Base)
	}
	if first.PR == nil || first.PR.URL == "" {
		t.Fatalf("report = %+v, want a pull request with a link", first)
	}
	if refs := remoteBranches(t, remote); len(refs) != 1 || refs[0] != first.Branch {
		t.Errorf("the remote holds %v, want only %q", refs, first.Branch)
	}

	// Pressing the control again updates the pull request rather than opening a
	// second one — the property a duplicate would make unrecoverable.
	second := publish(t, root)
	if !second.Updated {
		t.Error("the second publish must report an update, not a new pull request")
	}
	if got := creations(t, gh); len(got) != 1 {
		t.Errorf("gh pr create ran %d time(s) over two publishes: %v", len(got), got)
	}
}

func publish(t *testing.T, root string) publishJSON {
	t.Helper()
	out, err := runCtl(t, root, "publish", "main", "-json")
	if err != nil {
		t.Fatalf("loopctl publish: %v\n%s", err, out)
	}
	var got publishJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoding publish output: %v\n%s", err, out)
	}
	return got
}

// scriptGitHub puts a gh on PATH that behaves like the real one for the calls
// publishing makes, and records every pull request it is asked to create. It
// keeps one per head branch, which is what a stack needs; a second create for the
// same branch fails, exactly as GitHub does.
func scriptGitHub(t *testing.T) string {
	t.Helper()

	bin := t.TempDir()
	state := t.TempDir()
	script := strings.ReplaceAll(`#!/bin/sh
D=@STATE@
prev=""; head=""; base=""
for arg in "$@"; do
  case "$prev" in
    --head) head="$arg" ;;
    --base) base="$arg" ;;
  esac
  prev="$arg"
done
slug=$(printf '%s' "$head" | tr '/' '-')

if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  if [ -n "$slug" ] && [ -f "$D/head-$slug" ]; then
    printf '[{"number":%s,"url":"https://github.com/acme/app/pull/%s","state":"OPEN","isDraft":false,"baseRefName":"%s","headRefName":"%s"}]\n' \
      "$(cat $D/number-$slug)" "$(cat $D/number-$slug)" "$(cat $D/base-$slug)" "$(cat $D/head-$slug)"
  else
    printf '[]\n'
  fi
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  if [ -f "$D/head-$slug" ]; then echo "a pull request already exists" >&2; exit 1; fi
  cat > /dev/null
  number=$(( $(cat "$D/counter" 2>/dev/null || printf '6') + 1 ))
  printf '%s' "$number" > "$D/counter"
  printf '%s' "$number" > "$D/number-$slug"
  printf '%s' "$head" > "$D/head-$slug"
  printf '%s' "$base" > "$D/base-$slug"
  printf '%s\n' "$*" >> "$D/creations"
  exit 0
fi

if [ "$1" = "api" ]; then cat > /dev/null; exit 0; fi

echo "unexpected gh invocation: $*" >&2
exit 1
`, "@STATE@", state)

	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

// creations is every `gh pr create` the scripted gh was asked to perform.
func creations(t *testing.T, state string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(state, "creations"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// stackJSON is what `loopctl publish -stack -json` emits.
type stackJSON struct {
	PRD     string `json:"prd"`
	Stories []struct {
		StoryID     string `json:"storyId"`
		Branch      string `json:"branch"`
		Base        string `json:"base"`
		Skipped     string `json:"skipped"`
		AlreadyOpen bool   `json:"alreadyOpen"`
		PR          *struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
		} `json:"pr"`
	} `json:"stories"`
	Failed string `json:"failed"`
}

// A run that gave each story its own branch publishes as a stack: three pull
// requests, each based on the branch below it, opened by a process that performed
// none of the run.
func TestPublishingAPerStoryPRDOpensAStackFromAnotherProcess(t *testing.T) {
	root := newProject(t)
	remote := addRemote(t, root)
	gh := scriptGitHub(t)
	// Recorded before the run, which is where a run gets its layout from.
	writeGitRecord(t, root, "main", map[string]any{"layout": "branch-per-story"})

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	got := publishStack(t, root)
	if len(got.Stories) != 3 {
		t.Fatalf("stories = %+v, want one entry per story", got.Stories)
	}

	base := "main"
	for _, story := range got.Stories {
		if story.PR == nil || story.PR.URL == "" {
			t.Fatalf("%s = %+v, want a pull request with a link", story.StoryID, story)
		}
		if story.Base != base {
			t.Errorf("%s is based on %q, want the branch below it %q", story.StoryID, story.Base, base)
		}
		base = story.Branch
	}
	if created := creations(t, gh); len(created) != 3 {
		t.Errorf("gh pr create ran %d time(s), want one per story: %v", len(created), created)
	}
	if refs := remoteBranches(t, remote); len(refs) != 3 {
		t.Errorf("the remote holds %v, want one branch per story", refs)
	}

	// A second pass has nothing left to do. It reports the same three pull requests
	// rather than opening three more, and says of each that it was already open.
	again := publishStack(t, root)
	if created := creations(t, gh); len(created) != 3 {
		t.Errorf("gh pr create ran %d time(s) over two passes: %v", len(created), created)
	}
	if again.Failed != "" {
		t.Errorf("failed = %q, want a retry with nothing to do to report no failure", again.Failed)
	}
	for i, story := range again.Stories {
		if !story.AlreadyOpen {
			t.Errorf("%s = %+v, want it reported as already open", story.StoryID, story)
		}
		if story.PR == nil || story.PR.Number != got.Stories[i].PR.Number {
			t.Errorf("%s = %+v, want the same pull request as %+v",
				story.StoryID, story.PR, got.Stories[i].PR)
		}
	}
}

func publishStack(t *testing.T, root string) stackJSON {
	t.Helper()
	out, err := runCtl(t, root, "publish", "main", "-stack", "-json")
	if err != nil {
		t.Fatalf("loopctl publish -stack: %v\n%s", err, out)
	}
	var got stackJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decoding stack output: %v\n%s", err, out)
	}
	return got
}
