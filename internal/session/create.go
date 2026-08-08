package session

// Creating a PRD before the agent has written one.
//
// A PRD used to come into existence only when an authoring session finished and
// the agent saved prd.md. Until then it was nowhere: not in the sidebar, not
// selectable, and its workflow settings had nothing to attach to. Abandoning the
// conversation left no trace of the thing you had started, and a crash lost it
// entirely.
//
// Creating it up front turns the PRD into the thing you are working on rather
// than the eventual output of a conversation. The stub carries only what the
// user actually supplied — a title, and the brief they typed — so the agent has
// somewhere to write and the rest of the application has something real to point
// at.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewPRDRequest is what creating a PRD needs.
type NewPRDRequest struct {
	// Name is the directory under .chief/prds/ and the identity everything else
	// keys off.
	Name string `json:"name"`
	// Context is the brief the user typed. Recorded in the stub so it survives a
	// session that never finishes, and so the document is not empty.
	Context string `json:"context,omitempty"`
	// Workflow is the implementation agent, stacking and issue settings chosen
	// at the same moment.
	Workflow PRDWorkflow `json:"workflow"`
}

// CreatePRD writes a new PRD and returns its summary.
//
// The stub deliberately has no user stories. An empty story list is what makes
// the difference between "written but not filled in" and "finished" legible —
// summarisePRD reports complete only when there are stories and all of them
// pass, so a fresh PRD reads as 0/0 rather than as done.
func (s *Session) CreatePRD(ctx context.Context, req NewPRDRequest) (PRDSummary, error) {
	root, err := s.requireProject()
	if err != nil {
		return PRDSummary{}, err
	}
	name := strings.TrimSpace(req.Name)
	if err := validPRDName(name); err != nil {
		return PRDSummary{}, err
	}

	path := filepath.Join(root, chiefDir, prdsDir, name, prdFile)
	if _, err := os.Stat(path); err == nil {
		return PRDSummary{}, fmt.Errorf(
			"%s already exists. Pick another name, or open it from the sidebar.", name)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return PRDSummary{}, fmt.Errorf("could not create the folder for %s: %w", name, err)
	}
	if err := writeFileAtomic(path, []byte(stubPRD(name, req.Context))); err != nil {
		return PRDSummary{}, fmt.Errorf("could not write %s: %w", name, err)
	}

	// The workflow settings were chosen in the same dialog; saving them here
	// keeps the two halves of one decision together.
	if err := s.SavePRDWorkflow(name, req.Workflow); err != nil {
		return PRDSummary{}, err
	}
	if err := s.Rescan(ctx); err != nil {
		return PRDSummary{}, err
	}

	for _, p := range s.PRDs() {
		if p.Name == name {
			return p, nil
		}
	}
	return PRDSummary{}, fmt.Errorf("%s was written but could not be read back", name)
}

// stubPRD is the document an authoring session starts from.
//
// It is the minimum that parses as a PRD and still says what the thing is for:
// a title, and the brief as written. Anything more would be Loop dictating the
// document's shape, which is the agent's job and the prompt's.
func stubPRD(name, context string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# PRD: %s\n\n", name)

	if brief := strings.TrimSpace(context); brief != "" {
		b.WriteString("## Overview\n\n")
		b.WriteString(brief)
		b.WriteString("\n\n")
	}

	// The heading is present but empty: the parser needs it, and its emptiness
	// is what marks the PRD as not yet written rather than complete.
	b.WriteString("## User Stories\n")
	return b.String()
}

// writeFileAtomic replaces a file's contents via a temp file and a rename, so an
// interrupted write leaves the original intact rather than a truncated file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".prd-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
