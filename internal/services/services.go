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
	"errors"
	"fmt"

	"github.com/dripcoder/loop/internal/authoring"
	"github.com/dripcoder/loop/internal/prompts"
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

// LocalApps reports which supported editors are installed.
func (p *ProjectService) LocalApps() []session.AppStatus { return p.s.LocalApps() }

// OpenInApp opens the repository root in a local editor.
func (p *ProjectService) OpenInApp(app string) error {
	return p.s.OpenInApp(context.Background(), session.LocalApp(app))
}

// OpenOnGitHub opens the repository's GitHub page in the default browser.
func (p *ProjectService) OpenOnGitHub() error {
	url, err := p.s.GitHubURL(context.Background())
	if err != nil {
		return err
	}
	app := application.Get()
	if app == nil {
		return fmt.Errorf("the application is not running")
	}
	return app.Browser.OpenURL(url)
}

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

// OpenFile opens a PRD's prd.md, preferring VS Code.
func (p *PRDService) OpenFile(name string) error {
	err := p.s.OpenPRDFile(context.Background(), name)
	if !errors.Is(err, session.ErrNoEditor) {
		return err
	}
	return p.openWithSystemDefault(name)
}

// openWithSystemDefault hands the file to the operating system. Reached only
// when no editor Loop knows how to launch is installed.
func (p *PRDService) openWithSystemDefault(name string) error {
	path, err := p.s.PRDPath(name)
	if err != nil {
		return err
	}
	app := application.Get()
	if app == nil {
		return fmt.Errorf("the application is not running")
	}
	return app.Browser.OpenFile(path)
}

// Create writes a new PRD and returns its summary, so the caller can select it
// without waiting for a rescan.
func (p *PRDService) Create(req session.NewPRDRequest) (session.PRDSummary, error) {
	return p.s.CreatePRD(context.Background(), req)
}

// Delete removes a PRD directory and everything in it.
func (p *PRDService) Delete(name string) error { return p.s.DeletePRD(name) }

// Workflow returns a PRD's implementation agent, stacking and issue settings.
func (p *PRDService) Workflow(name string) (session.PRDWorkflow, error) {
	return p.s.PRDWorkflowFor(name)
}

// SaveWorkflow stores a PRD's workflow settings. It starts nothing.
func (p *PRDService) SaveWorkflow(name string, w session.PRDWorkflow) error {
	return p.s.SavePRDWorkflow(name, w)
}

// PullRequests returns the cached pull request state for a PRD's branches,
// without contacting GitHub.
func (p *PRDService) PullRequests(name string) (session.PullRequestSet, error) {
	return p.s.PullRequestsFor(name)
}

// RefreshPullRequests re-reads pull request state from GitHub. Slow enough to be
// explicit — it shells out to gh — and never fails because GitHub is out of
// reach; that is reported in the result.
func (p *PRDService) RefreshPullRequests(name string) (session.PullRequestSet, error) {
	return p.s.RefreshPullRequests(context.Background(), name)
}

// AgentDefaults reports the resolved per-phase agent defaults.
func (p *ProjectService) AgentDefaults() session.AgentDefaults {
	return session.AgentDefaults{
		Authoring:      p.s.DefaultAuthoringAgent(),
		Implementation: p.s.DefaultImplementationAgent(),
	}
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

// AuthoringService drives the interactive agent session that writes a PRD, and
// the prompt it is given.
type AuthoringService struct{ s *session.Session }

func NewAuthoring(s *session.Session) *AuthoringService { return &AuthoringService{s: s} }

// Start opens a session and returns its id. Output arrives on the
// "loop:author" event, not as a return value.
func (a *AuthoringService) Start(spec authoring.Spec) (string, error) {
	return a.s.StartAuthoring(spec)
}

// Write sends keystrokes. data is base64, because terminal input is bytes.
func (a *AuthoringService) Write(id, data string) error { return a.s.WriteAuthoring(id, data) }

// Resize tells the session its terminal changed size so the agent re-wraps.
func (a *AuthoringService) Resize(id string, cols, rows int) error {
	return a.s.ResizeAuthoring(id, cols, rows)
}

// Interrupt sends Ctrl-C.
func (a *AuthoringService) Interrupt(id string) error { return a.s.InterruptAuthoring(id) }

// Stop ends a session and everything it spawned.
func (a *AuthoringService) Stop(id string) error { return a.s.StopAuthoring(id) }

// Scrollback returns the output so far, base64, so a reopened view can redraw
// instead of showing a blank terminal mid-conversation.
func (a *AuthoringService) Scrollback(id string) (string, error) {
	return a.s.AuthoringScrollback(id)
}

// GetPrompt returns the project's authoring prompt, which is chief's built-in
// unless the project has overridden it.
func (a *AuthoringService) GetPrompt(kind string) (prompts.Prompt, error) {
	return a.s.Prompt(kind)
}

// SavePrompt writes a project override to .chief/prompts/<kind>.md. Saving an
// empty body, or the built-in verbatim, clears the override instead.
func (a *AuthoringService) SavePrompt(kind, body string) error {
	return a.s.SavePrompt(kind, body)
}

// ResetPrompt removes the override, restoring chief's prompt.
func (a *AuthoringService) ResetPrompt(kind string) error { return a.s.ResetPrompt(kind) }

// BuiltinPrompt returns chief's own prompt, so the editor can offer it as a
// starting point.
func (a *AuthoringService) BuiltinPrompt(kind string) string { return a.s.BuiltinPrompt(kind) }
