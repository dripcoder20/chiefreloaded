package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dripcoder/loop/internal/authoring"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// These cover the PRD-action backend the rail's overflow / context menus call:
// resolving a PRD's prd.md path (Open markdown file) and removing a PRD
// directory (Delete PRD), including the guards that keep a delete from reaching
// outside .chief/prds/.

func TestPRDPathReturnsTheMarkdownFile(t *testing.T) {
	root := t.TempDir()
	want := writePRD(t, root, "checkout", samplePRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	got, err := s.PRDPath("checkout")
	if err != nil {
		t.Fatalf("PRDPath: %v", err)
	}
	if got != want {
		t.Errorf("PRDPath = %q, want %q", got, want)
	}
}

func TestPRDPathUnknownPRD(t *testing.T) {
	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PRDPath("nope"); err == nil {
		t.Error("expected an error for an unknown PRD")
	}
}

func TestDeletePRDRemovesTheDirectoryAndRepublishes(t *testing.T) {
	root := t.TempDir()
	writePRD(t, root, "checkout", samplePRD)
	writePRD(t, root, "docs-site", samplePRD)

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	if err := s.DeletePRD("checkout"); err != nil {
		t.Fatalf("DeletePRD: %v", err)
	}

	dir := filepath.Join(root, ".chief", "prds", "checkout")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("PRD directory still present after delete: %v", err)
	}

	// The in-memory list is refreshed, so the rail no longer offers the deleted PRD.
	names := make([]string, 0, len(s.PRDs()))
	for _, p := range s.PRDs() {
		names = append(names, p.Name)
	}
	if len(names) != 1 || names[0] != "docs-site" {
		t.Errorf("remaining PRDs = %v, want [docs-site]", names)
	}
}

// Deleting the legacy .chief/prd.md would mean removing .chief itself. Refuse it
// rather than take the whole project's Loop state with the PRD.
func TestDeletePRDRefusesLegacyLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".chief"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".chief", "prd.md"), []byte(samplePRD), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	if err := s.DeletePRD("main"); err == nil {
		t.Error("expected DeletePRD to refuse the legacy layout")
	}
	if _, err := os.Stat(filepath.Join(root, ".chief")); err != nil {
		t.Errorf(".chief must survive a refused legacy delete: %v", err)
	}
}

func TestDeletePRDUnknownPRD(t *testing.T) {
	s := newTestSession(t)
	if _, err := s.OpenProject(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePRD("nope"); err == nil {
		t.Error("expected an error deleting an unknown PRD")
	}
}

// --- US-006: deletion safety and file-open errors ---------------------------

// Deleting a PRD that is being implemented would silently terminate the run, or
// leave the agent writing into a directory that no longer exists.
func TestDeletePRDRefusedWhileImplementationIsActive(t *testing.T) {
	// An agent that says nothing keeps the run genuinely active for the whole
	// test. Writing StateRunning into s.runs looked like it did the same, but
	// the guard consults the live run and never reads that field — so the test
	// was really racing the agent to finish, and lost often enough to matter.
	s, root, runID := startRun(t, oneStoryPRD, fakeagent.Behaviour{Silence: 10 * time.Second})
	t.Cleanup(func() { stopRun(t, s, runID) })

	err := s.DeletePRD("main")
	if err == nil {
		t.Fatal("expected deletion to be refused while the PRD is being implemented")
	}
	if !strings.Contains(err.Error(), "main") {
		t.Errorf("error %q should name the PRD", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".chief", "prds", "main")); statErr != nil {
		t.Errorf("the PRD directory must survive a refused deletion: %v", statErr)
	}
}

func TestDeletePRDRefusedWhileAuthoringIsOpen(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "docs", oneStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.authorSpecs["author-1"] = authoring.Spec{Kind: authoring.KindEdit, PRD: "docs"}
	s.mu.Unlock()

	err := s.DeletePRD("docs")
	if err == nil {
		t.Fatal("expected deletion to be refused while an authoring session is open")
	}
	if !strings.Contains(err.Error(), "authoring session") {
		t.Errorf("error %q should explain that an authoring session is in the way", err)
	}
}

// A PRD listed but whose file has gone must produce an actionable error rather
// than a path the OS would open as a directory.
func TestPRDPathReportsAMissingMarkdownFile(t *testing.T) {
	s := newTestSession(t)
	root := t.TempDir()
	gitInit(t, root)
	writePRD(t, root, "gone", oneStoryPRD)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(root, ".chief", "prds", "gone", "prd.md")); err != nil {
		t.Fatal(err)
	}

	path, err := s.PRDPath("gone")
	if err == nil {
		t.Fatalf("expected an error for a missing markdown file, got %q", path)
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("error %q should name the PRD", err)
	}
}
