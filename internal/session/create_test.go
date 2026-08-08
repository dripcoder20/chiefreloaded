package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dripcoder/loop/internal/authoring"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// A PRD used to exist only once the agent had written one, so abandoning the
// conversation left no trace of the thing you had started.
func TestCreatePRDMakesItImmediatelyVisible(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	got, err := s.CreatePRD(t.Context(), NewPRDRequest{
		Name:     "secondary-email",
		Context:  "Let people nominate a second email on a contact.",
		Workflow: PRDWorkflow{ImplementationAgent: "codex", StackPerStory: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "secondary-email" {
		t.Errorf("name = %q", got.Name)
	}

	// In the list straight away, without waiting for an agent.
	var listed bool
	for _, p := range s.PRDs() {
		if p.Name == "secondary-email" {
			listed = true
		}
	}
	if !listed {
		t.Error("the new PRD is not in the list")
	}

	// And on disk, where the authoring session will find it.
	if _, err := os.Stat(filepath.Join(root, ".chief", "prds", "secondary-email", "prd.md")); err != nil {
		t.Errorf("prd.md was not written: %v", err)
	}
}

// A brand-new PRD has no stories, so it must read as unwritten rather than as
// finished — summarisePRD reports complete only when stories exist and all pass.
func TestANewPRDIsNotComplete(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	got, err := s.CreatePRD(t.Context(), NewPRDRequest{Name: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || got.Completed != 0 {
		t.Errorf("counts = %d/%d, want 0/0", got.Completed, got.Total)
	}
	if got.State == StateComplete {
		t.Error("a PRD with no stories must not report complete")
	}
	if got.ParseError != "" {
		t.Errorf("the stub must parse as a PRD: %s", got.ParseError)
	}
}

// The workflow chosen in the same dialog is saved with it, so the later run
// reads back what was actually asked for.
func TestCreatePRDSavesTheChosenWorkflow(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	want := PRDWorkflow{
		ImplementationAgent: "codex",
		StackPerStory:       true,
	}
	if _, err := s.CreatePRD(t.Context(), NewPRDRequest{Name: "checkout", Workflow: want}); err != nil {
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

// The brief the user typed survives a session that never finishes.
func TestCreatePRDRecordsTheBrief(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	const brief = "Let people nominate a second email on a contact."
	if _, err := s.CreatePRD(t.Context(), NewPRDRequest{Name: "checkout", Context: brief}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, ".chief", "prds", "checkout", "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), brief) {
		t.Errorf("the brief is missing from the stub:\n%s", raw)
	}
}

// Overwriting an existing PRD would destroy work; the name is refused instead.
func TestCreatePRDRefusesAnExistingName(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", oneStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(filepath.Join(root, ".chief", "prds", "checkout", "prd.md"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreatePRD(t.Context(), NewPRDRequest{Name: "checkout"}); err == nil {
		t.Fatal("expected an existing name to be refused")
	}

	after, _ := os.ReadFile(filepath.Join(root, ".chief", "prds", "checkout", "prd.md"))
	if string(after) != string(before) {
		t.Error("the existing PRD was modified")
	}
}

// The name becomes a directory, so it must not be able to escape .chief/prds.
func TestCreatePRDRejectsAnUnsafeName(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"", "  ", "../escape", "a/b"} {
		if _, err := s.CreatePRD(t.Context(), NewPRDRequest{Name: name}); err == nil {
			t.Errorf("creating %q should be refused", name)
		}
	}
}

// The bug this guards against: creating a PRD writes a stub, and an authoring
// session that refused to start because its own stub existed could never begin.
func TestAuthoringStartsOnAFreshlyCreatedPRD(t *testing.T) {
	s := newTestSessionWith(t, fakeagent.New())
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreatePRD(t.Context(), NewPRDRequest{Name: "test"}); err != nil {
		t.Fatal(err)
	}

	id, err := s.StartAuthoring(authoring.Spec{Kind: authoring.KindNew, PRD: "test"})
	if err != nil {
		t.Fatalf("a session for a newly created PRD must start: %v", err)
	}
	t.Cleanup(func() { _ = s.StopAuthoring(id) })
}

// A PRD with written work is still protected: the new-PRD prompt would write
// over stories somebody already has.
func TestAuthoringRefusesToOverwriteWrittenStories(t *testing.T) {
	s := newTestSessionWith(t, fakeagent.New())
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "checkout", oneStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	_, err := s.StartAuthoring(authoring.Spec{Kind: authoring.KindNew, PRD: "checkout"})
	if err == nil {
		t.Fatal("expected a PRD with stories to be protected")
	}
	if !strings.Contains(err.Error(), "Update PRD") {
		t.Errorf("error %q should point at the way forward", err)
	}
}
