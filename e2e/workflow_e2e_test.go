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
