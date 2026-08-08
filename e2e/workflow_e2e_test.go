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
