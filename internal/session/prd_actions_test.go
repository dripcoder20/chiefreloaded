package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
