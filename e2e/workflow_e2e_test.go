//go:build e2e

package e2e

// End-to-end coverage for the per-PRD workflow: which agent implements it,
// and publishing its user stories as tracker issues.
//
// Publishing goes through a fake `gh` placed on PATH rather than a mocked
// client, so what is exercised is the real path — argv construction, output
// parsing, the sidecar write and the markdown rewrite — in a separate process
// from the test. The scripted binary records every invocation, which is how
// "exactly one issue per story" is checked rather than assumed.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH is a `gh` that answers the three things the GitHub client asks it:
// whether it is authenticated, whether there is a repository, and to create an
// issue. Every create appends to a log so duplicates are visible.
func fakeGH(t *testing.T, dir, logPath string) string {
	t.Helper()

	path := filepath.Join(dir, "gh")
	script := fmt.Sprintf(`#!/bin/sh
LOG=%q
case "$1 $2" in
  "auth status") exit 0 ;;
  "repo view") printf '{"name":"demo"}\n'; exit 0 ;;
esac
if [ "$1" = "issue" ] && [ "$2" = "create" ]; then
  title=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --title) title="$2"; shift 2 ;;
      --body) shift 2 ;;
      *) shift ;;
    esac
  done
  printf '%%s\n' "$title" >> "$LOG"
  n=$(wc -l < "$LOG" | tr -d ' ')
  printf 'https://github.com/acme/demo/issues/%%s\n' "$n"
  exit 0
fi
exit 1
`, logPath)

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

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

// runCtlWithPath runs loopctl with dir prepended to PATH, so the scripted gh is
// what the GitHub client finds.
func runCtlWithPath(t *testing.T, root, binDir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(loopctl, append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CHIEF_AGENT=claude",
		"CHIEF_AGENT_PATH="+fakeAgent(t, t.TempDir()),
	)

	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readPRDFile(t *testing.T, root, prd string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".chief", "prds", prd, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// workflowJSON is what `loopctl workflow -json` emits.
type workflowJSON struct {
	Workflow struct {
		ImplementationAgent string `json:"implementationAgent"`
		StackPerStory       bool   `json:"stackPerStory"`
		IssueDestination    string `json:"issueDestination"`
	} `json:"workflow"`
	ResolvedAgent string `json:"resolvedAgent"`
	ResolveError  string `json:"resolveError"`
	Issues        map[string]struct {
		Identifier string `json:"identifier"`
		URL        string `json:"url"`
	} `json:"issues"`
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
	if got.Workflow.StackPerStory {
		t.Error("stacked PRs must be off by default")
	}
	if got.Workflow.IssueDestination != "" {
		t.Errorf("issue destination = %q, want none", got.Workflow.IssueDestination)
	}
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
	writeWorkflow(t, root, "main", map[string]any{
		"implementationAgent": "codex",
		"stackPerStory":       true,
	})

	got := readWorkflow(t, root, "main")
	if got.Workflow.ImplementationAgent != "codex" {
		t.Errorf("implementation agent = %q, want codex", got.Workflow.ImplementationAgent)
	}
	if !got.Workflow.StackPerStory {
		t.Error("the stacked-PR choice did not survive")
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

// The headline publishing guarantee: exactly one issue per user story, each
// linked back into the story it came from.
func TestPublishCreatesOneIssuePerStoryAndLinksThePRD(t *testing.T) {
	root := newProject(t)
	binDir := t.TempDir()
	ghLog := filepath.Join(binDir, "created.log")
	fakeGH(t, binDir, ghLog)
	writeWorkflow(t, root, "main", map[string]any{"issueDestination": "github"})

	out, err := runCtlWithPath(t, root, binDir, "publish", "main")
	if err != nil {
		t.Fatalf("loopctl publish: %v\n%s", err, out)
	}

	created := createdTitles(t, ghLog)
	if len(created) != 3 {
		t.Fatalf("created %d issues, want one per story:\n%v", len(created), created)
	}
	for _, id := range []string{"US-001", "US-002", "US-003"} {
		if !strings.Contains(strings.Join(created, "\n"), id) {
			t.Errorf("no issue was created for %s:\n%v", id, created)
		}
	}

	// Every story carries its identifier and a clickable URL.
	doc := readPRDFile(t, root, "main")
	if n := strings.Count(doc, "**External Issue:**"); n != 3 {
		t.Fatalf("PRD carries %d references, want one per story:\n%s", n, doc)
	}
	for _, want := range []string{"#1", "#2", "#3", "https://github.com/acme/demo/issues/"} {
		if !strings.Contains(doc, want) {
			t.Errorf("the PRD is missing %q:\n%s", want, doc)
		}
	}

	// The references are recorded, so a later process knows the issues exist.
	got := readWorkflow(t, root, "main")
	if len(got.Issues) != 3 {
		t.Errorf("recorded %d references, want 3: %+v", len(got.Issues), got.Issues)
	}
	for storyID, ref := range got.Issues {
		if ref.Identifier == "" || ref.URL == "" {
			t.Errorf("%s recorded an incomplete reference: %+v", storyID, ref)
		}
	}
}

// Publishing twice must create nothing the second time. A duplicate issue
// cannot be un-created, so this is checked against the tracker's own log.
func TestRepublishingCreatesNoDuplicates(t *testing.T) {
	root := newProject(t)
	binDir := t.TempDir()
	ghLog := filepath.Join(binDir, "created.log")
	fakeGH(t, binDir, ghLog)
	writeWorkflow(t, root, "main", map[string]any{"issueDestination": "github"})

	if out, err := runCtlWithPath(t, root, binDir, "publish", "main"); err != nil {
		t.Fatalf("first publish: %v\n%s", err, out)
	}
	out, err := runCtlWithPath(t, root, binDir, "publish", "main")
	if err != nil {
		t.Fatalf("second publish: %v\n%s", err, out)
	}

	if created := createdTitles(t, ghLog); len(created) != 3 {
		t.Errorf("created %d issues across two publishes, want 3:\n%v", len(created), created)
	}
	if !strings.Contains(out, "already created") {
		t.Errorf("the second pass should report the stories it skipped:\n%s", out)
	}
	if n := strings.Count(readPRDFile(t, root, "main"), "**External Issue:**"); n != 3 {
		t.Errorf("PRD carries %d references after two publishes, want 3", n)
	}
}

// A PRD set to publish nowhere must perform no tracker write at all.
func TestNoPublishModeContactsNoTracker(t *testing.T) {
	root := newProject(t)
	binDir := t.TempDir()
	ghLog := filepath.Join(binDir, "created.log")
	fakeGH(t, binDir, ghLog)

	if _, err := runCtlWithPath(t, root, binDir, "publish", "main"); err == nil {
		t.Error("expected publishing to be refused with no destination configured")
	}
	if created := createdTitles(t, ghLog); len(created) != 0 {
		t.Errorf("created %v, want no tracker writes at all", created)
	}
	if strings.Contains(readPRDFile(t, root, "main"), "**External Issue:**") {
		t.Error("nothing should have been written into the PRD")
	}
}

// A tracker outage must never cost the generated PRD.
func TestAFailingTrackerLeavesThePRDIntact(t *testing.T) {
	root := newProject(t)
	binDir := t.TempDir()
	// A gh that authenticates but refuses to create anything.
	broken := filepath.Join(binDir, "gh")
	script := `#!/bin/sh
case "$1 $2" in
  "auth status") exit 0 ;;
  "repo view") printf '{"name":"demo"}\n'; exit 0 ;;
esac
echo "the tracker is unavailable" >&2
exit 1
`
	if err := os.WriteFile(broken, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, root, "main", map[string]any{"issueDestination": "github"})
	before := readPRDFile(t, root, "main")

	out, err := runCtlWithPath(t, root, binDir, "publish", "main")
	if err != nil {
		t.Fatalf("a failing tracker must still produce a report: %v\n%s", err, out)
	}
	if readPRDFile(t, root, "main") != before {
		t.Error("a total publishing failure must leave the PRD byte-identical")
	}
	// The PRD must still be readable as a PRD afterwards.
	if show, err := runCtl(t, root, "show", "main"); err != nil {
		t.Fatalf("the PRD no longer parses after a failed publish: %v\n%s", err, show)
	}
}

// createdTitles reads the fake tracker's log of what it was asked to create.
func createdTitles(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
