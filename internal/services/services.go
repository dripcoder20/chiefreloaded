// Package services exposes internal/session to the Wails frontend.
//
// Every method here is a one-to-five-line delegation. That is the rule, not a
// coincidence: the moment logic appears in this package it stops being testable
// without a window and stops being reachable from cmd/loopctl, which is exactly
// the coupling that made chief's app.go impossible to reuse.
//
// Bound methods are coarse-grained and return quickly. Anything slow — a run, a
// worktree provision, an authoring session — returns immediately and reports
// through the event stream.
package services

import (
	"context"
	"fmt"

	"github.com/dripcoder/loop/internal/session"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ProjectService covers opening a project and its configuration.
type ProjectService struct{ s *session.Session }

func NewProject(s *session.Session) *ProjectService { return &ProjectService{s: s} }

// Open opens a project directory and scans it.
func (p *ProjectService) Open(path string) (session.Project, error) {
	return p.s.OpenProject(context.Background(), path)
}

// Pick shows a native folder chooser and opens what the user selects.
//
// Returns a nil project when the dialog is dismissed. That is a normal outcome,
// not an error — reporting it as one would put an alert on screen every time
// someone changes their mind.
func (p *ProjectService) Pick() (*session.Project, error) {
	// application.Get() rather than an injected *App: services are constructed
	// as part of the Options literal that creates the App, so there is nothing
	// to inject at that point.
	app := application.Get()
	if app == nil {
		return nil, fmt.Errorf("the application is not running")
	}

	dir, err := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Open a project").
		PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, nil
	}

	project, err := p.s.OpenProject(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	return &project, nil
}

// Current returns the open project, or nil when none is open.
func (p *ProjectService) Current() *session.Project { return p.s.Project() }

// Environment reports the detected tooling, including whether stacked pull
// requests are actually available.
func (p *ProjectService) Environment() session.Environment { return p.s.Environment() }

// GetConfig returns the project settings.
func (p *ProjectService) GetConfig() session.Settings { return p.s.Settings() }

// SaveConfig writes the project settings.
func (p *ProjectService) SaveConfig(v session.Settings) error { return p.s.SaveSettings(v) }

// Rescan re-reads the PRDs from disk.
func (p *ProjectService) Rescan() error { return p.s.Rescan(context.Background()) }

// PRDService reads PRDs and their progress journals.
type PRDService struct{ s *session.Session }

func NewPRD(s *session.Session) *PRDService { return &PRDService{s: s} }

// List returns every PRD in the project.
func (p *PRDService) List() []session.PRDSummary { return p.s.PRDs() }

// Get returns one PRD in full, including its stories.
func (p *PRDService) Get(name string) (session.PRDDetail, error) { return p.s.PRD(name) }

// Progress returns a PRD's journal entries keyed by story ID.
func (p *PRDService) Progress(name string) (map[string][]session.ProgressEntry, error) {
	return p.s.Progress(name)
}

// RunService starts and controls runs.
type RunService struct{ s *session.Session }

func NewRun(s *session.Session) *RunService { return &RunService{s: s} }

// Start begins a run and returns its ID immediately.
func (r *RunService) Start(req session.StartRequest) (string, error) {
	return r.s.Start(context.Background(), req)
}

// Pause stops after the current story; the agent finishes what it started.
func (r *RunService) Pause(runID string) error { return r.s.Pause(runID) }

// Resume continues a paused run.
func (r *RunService) Resume(runID string) error { return r.s.Resume(runID) }

// Stop ends a run now, killing the agent subprocess.
func (r *RunService) Stop(runID string) error { return r.s.Stop(runID) }

// SetAttemptBudget changes how many attempts a run may still spend. Raising it
// on an exhausted run also resumes it, since that is invariably the intent.
func (r *RunService) SetAttemptBudget(runID string, budget int) error {
	return r.s.SetAttemptBudget(runID, budget)
}

// List returns every run in this session, finished ones included.
func (r *RunService) List() []session.RunSnapshot { return r.s.Runs() }

// Snapshot returns the whole observable state, tagged with the sequence number
// it is current as of. The frontend adopts this when it has lost its place in
// the event stream.
func (r *RunService) Snapshot() session.Snapshot { return r.s.Snapshot() }

// ReplayResult is Replay's return value. Wails binds a struct more predictably
// than multiple returns, and Complete has to be checked, so naming it makes that
// harder to skip.
type ReplayResult struct {
	Events []session.Event `json:"events"`
	// Complete is false when the retention ring rolled past the requested
	// sequence. The caller then has an unrecoverable gap and must take a
	// Snapshot instead of assuming these events are the whole story.
	Complete bool `json:"complete"`
}

// Replay returns the events after sinceSeq.
func (r *RunService) Replay(sinceSeq uint64) ReplayResult {
	evs, complete := r.s.Replay(sinceSeq)
	return ReplayResult{Events: evs, Complete: complete}
}

// Answer resolves a pending question.
func (r *RunService) Answer(id string, a session.Answer) error {
	return r.s.Answer(session.QuestionID(id), a)
}

// Questions returns everything currently waiting on the user.
func (r *RunService) Questions() []session.Question { return r.s.PendingQuestions() }
