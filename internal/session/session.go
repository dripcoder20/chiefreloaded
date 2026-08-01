// Package session is Loop's headless orchestrator.
//
// It owns everything chief keeps inside its Bubble Tea model: which PRDs exist,
// what is running, the branch-safety and worktree policy, and (from M7) the
// per-story stacked-branch lifecycle. Extracting that from the UI is the point of
// the package — chief's app.go interleaves policy with rendering, so the policy
// cannot be tested, scripted, or reused by a second front-end.
//
// The rules that keep it that way:
//
//   - Nothing here imports Wails, or any UI library.
//   - Nothing blocks on a consumer. See bus.
//   - Blocking decisions are emitted as Questions and answered asynchronously,
//     rather than being asked with a modal dialog.
//
// internal/services is a thin adapter that exposes this over Wails; cmd/loopctl
// drives the same API from a terminal, and is what the e2e tests use.
package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/dripcoder/loop/internal/agentx"
	"github.com/dripcoder/loop/internal/authoring"
	"github.com/dripcoder/loop/internal/chief/agent"
	"github.com/dripcoder/loop/internal/chief/config"
	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
)

// Options configures a Session. The zero value is usable.
type Options struct {
	// Logger receives diagnostics. Defaults to a logger that discards.
	Logger *slog.Logger
	// Clock is the time source. Injectable so tests are not time-dependent.
	Clock func() time.Time
	// EventBuffer is how many events are retained for Replay. Defaults to 4096.
	EventBuffer int
	// Probe reports what tooling is installed. Defaults to ProbeEnvironment,
	// which shells out to git, gh and the agent CLIs. Injectable so tests are not
	// paying a second of subprocess time per OpenProject, and so they can assert
	// behaviour in environments the test machine does not have — no gh, no
	// gh-stack, an unauthenticated gh.
	Probe func(context.Context) Environment
	// Provider overrides agent resolution entirely. Tests inject a scripted
	// agent here; production leaves it nil and resolves from config.
	Provider chiefloop.Provider
}

// Session is a single opened project and everything running against it.
//
// It is safe for concurrent use. Exactly one Session exists per application
// window; multiple PRDs run concurrently inside it.
type Session struct {
	opts Options
	log  *slog.Logger
	now  func() time.Time
	bus  *bus

	mu      sync.RWMutex
	project *Project
	// cfg is chief's view of the config; loopCfg is the same file including
	// Loop's own git block. Both are kept so Config() stays byte-compatible with
	// what the chief TUI would write.
	cfg     *config.Config
	loopCfg *config.LoopConfig
	prds    []PRDSummary
	env     Environment
	// runs holds a snapshot per run, including finished ones, so the UI can still
	// render a completed run's summary. live holds only those with a goroutine
	// attached and is what the control methods operate on.
	runs   map[string]*RunSnapshot
	live   map[string]*run
	runSeq int
	// questions are the outstanding decisions; pending holds the channel each
	// asking goroutine is parked on.
	questions   map[QuestionID]Question
	pending     map[QuestionID]chan Answer
	questionSeq int
	autoAnswer  bool

	// authoring owns the interactive PRD-writing sessions. authorSpecs remembers
	// what each was started for, so the completion event can say which PRD it
	// belonged to without the front-end having to track it.
	authoring   *authoring.Manager
	authorSpecs map[string]authoring.Spec

	// usage accumulates attributed usage across every run in the open project,
	// deduplicating on submission so replays cannot inflate a total.
	usage *usageLedger

	closeOnce sync.Once
}

// New creates a Session with no project open.
func New(opts Options) (*Session, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Probe == nil {
		opts.Probe = ProbeEnvironment
	}

	return &Session{
		opts:        opts,
		log:         opts.Logger,
		now:         opts.Clock,
		bus:         newBus(opts.EventBuffer, opts.Clock),
		runs:        make(map[string]*RunSnapshot),
		live:        make(map[string]*run),
		questions:   make(map[QuestionID]Question),
		pending:     make(map[QuestionID]chan Answer),
		authoring:   authoring.NewManager(),
		authorSpecs: make(map[string]authoring.Spec),
		usage:       newUsageLedger(),
	}, nil
}

// publish puts an event on the session's stream. Never blocks.
func (s *Session) publish(ev Event) { s.bus.publish(ev) }

// resolveProvider picks the agent CLI: explicit override, then config, then
// chief's own resolution order (flag, CHIEF_AGENT, config, claude).
//
// The provider is verified to be on PATH here rather than at first use. A
// missing CLI discovered mid-run looks like the agent silently doing nothing,
// which is a miserable thing to debug.
func (s *Session) resolveProvider(override string) (*agentx.GroupLeader, error) {
	if s.opts.Provider != nil {
		return agentx.NewGroupLeader(s.opts.Provider), nil
	}

	cfg := s.Config()
	p, err := agent.Resolve(override, "", &cfg)
	if err != nil {
		return nil, err
	}
	if err := agent.CheckInstalled(p); err != nil {
		return nil, err
	}
	// Every provider is wrapped so stopping a run reaches whatever the agent
	// spawned, not just the agent. See agentx.GroupLeader.
	return agentx.NewGroupLeader(p), nil
}

// afterStoryDone is the per-story git hook: push the branch, open its draft PR,
// and cut the next one. Implemented in stack.go; a no-op when stacking is off.
//
// It runs after the status write and after the agent process has exited, which
// is what makes touching git safe at all.
func (s *Session) afterStoryDone(ctx context.Context, r *run, storyID, title string, check CommitCheck) error {
	return s.stackAfterStory(ctx, r, storyID, title, check)
}

// Events is the session's single ordered event stream. It is closed by Close.
//
// The caller must read it until it closes. A consumer that stops reading will
// not stall the agent — the bus discards agent chatter instead — but it will
// miss events and should reconcile via Replay or Snapshot when it returns.
func (s *Session) Events() <-chan Event { return s.bus.events() }

// Replay returns the retained events after sinceSeq. complete is false when the
// retention ring has already rolled past that point, in which case the caller
// has an unrecoverable gap and should take a Snapshot instead.
func (s *Session) Replay(sinceSeq uint64) (evs []Event, complete bool) {
	return s.bus.replay(sinceSeq)
}

// Snapshot returns the whole observable state, tagged with the sequence number it
// is current as of. A consumer that has lost its place can adopt this wholesale
// and resume from Snapshot.Seq with no replay.
func (s *Session) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seq, _ := s.bus.stats()
	snap := Snapshot{
		Seq:         seq,
		Project:     s.project,
		PRDs:        append([]PRDSummary(nil), s.prds...),
		Environment: s.env,
		Runs:        make([]RunSnapshot, 0, len(s.runs)),
		Questions:   make([]Question, 0, len(s.questions)),
		Usage:       s.usage.report(),
	}
	for _, r := range s.runsLocked() {
		snap.Runs = append(snap.Runs, r)
	}
	for _, q := range s.questions {
		snap.Questions = append(snap.Questions, q)
	}
	return snap
}

// OpenProject opens root, probes the environment, and scans for PRDs.
//
// Calling it again re-opens, replacing any previously opened project. It is also
// the crash-recovery point: from M5 it resets stories left wedged in-progress by
// a process that died mid-run, which chief never does — a crashed chief leaves
// such a story stuck forever.
func (s *Session) OpenProject(ctx context.Context, root string) (Project, error) {
	project, err := openProject(root)
	if err != nil {
		return Project{}, err
	}

	loopCfg, err := config.LoadLoop(project.Root)
	if err != nil {
		return Project{}, fmt.Errorf("load .chief/config.yaml: %w", err)
	}
	cfg := &loopCfg.Config

	env := s.opts.Probe(ctx)
	prds := discoverPRDs(project.Root)

	s.mu.Lock()
	s.project = &project
	s.cfg = cfg
	s.loopCfg = loopCfg
	s.env = env
	s.prds = prds
	// Runs, questions and usage totals belong to the previous project.
	s.runs = make(map[string]*RunSnapshot)
	s.live = make(map[string]*run)
	s.questions = make(map[QuestionID]Question)
	s.pending = make(map[QuestionID]chan Answer)
	s.usage.reset()
	s.mu.Unlock()

	s.bus.publish(Event{
		Kind: EvProjectOpened,
		Text: project.Root,
	})
	return project, nil
}

// Project returns the currently open project, or nil.
func (s *Session) Project() *Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.project
}

// Environment returns the last probe result.
func (s *Session) Environment() Environment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.env
}

// PRDs returns the discovered PRDs, sorted by name.
func (s *Session) PRDs() []PRDSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]PRDSummary(nil), s.prds...)
}

// PRD returns one PRD in full, including its stories.
func (s *Session) PRD(name string) (PRDDetail, error) {
	root, err := s.requireProject()
	if err != nil {
		return PRDDetail{}, err
	}
	return loadPRDDetail(root, name)
}

// Progress returns a PRD's progress.md journal, keyed by story ID.
func (s *Session) Progress(name string) (map[string][]ProgressEntry, error) {
	root, err := s.requireProject()
	if err != nil {
		return nil, err
	}
	summaries := discoverPRDs(root)
	for _, p := range summaries {
		if p.Name != name {
			continue
		}
		raw := progressFor(p.Path)
		out := make(map[string][]ProgressEntry, len(raw))
		for id, entries := range raw {
			converted := make([]ProgressEntry, 0, len(entries))
			for _, e := range entries {
				converted = append(converted, ProgressEntry{
					StoryID: e.StoryID,
					Date:    e.Date,
					Content: e.Content,
				})
			}
			out[id] = converted
		}
		return out, nil
	}
	return nil, fmt.Errorf("no PRD named %q", name)
}

// Rescan re-reads the PRDs from disk and publishes the result.
//
// A file watcher drives this from M3. Until then it is how a caller picks up
// changes made by an editor, or by the chief TUI running against the same
// .chief/ directory.
func (s *Session) Rescan(ctx context.Context) error {
	root, err := s.requireProject()
	if err != nil {
		return err
	}

	prds := discoverPRDs(root)
	s.mu.Lock()
	s.prds = prds
	s.mu.Unlock()

	s.bus.publish(Event{Kind: EvPRDChanged})
	return nil
}

// LoopConfig returns the project config including Loop's own git block.
func (s *Session) LoopConfig() config.LoopConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loopCfgLocked()
}

func (s *Session) loopCfgLocked() config.LoopConfig {
	if s.loopCfg == nil {
		return *config.DefaultLoop()
	}
	return *s.loopCfg
}

// SaveLoopConfig writes the project config including Loop's git block.
func (s *Session) SaveLoopConfig(cfg config.LoopConfig) error {
	root, err := s.requireProject()
	if err != nil {
		return err
	}
	cfg.Normalise()
	if err := config.SaveLoop(root, &cfg); err != nil {
		return fmt.Errorf("save .chief/config.yaml: %w", err)
	}

	s.mu.Lock()
	s.loopCfg = &cfg
	base := cfg.Config
	s.cfg = &base
	s.mu.Unlock()

	s.publish(Event{Kind: EvConfigChanged})
	return nil
}

// Config returns the project's .chief/config.yaml.
func (s *Session) Config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return *config.Default()
	}
	return *s.cfg
}

// SaveConfig writes .chief/config.yaml.
//
// The file is shared with the chief TUI, which parses it into a struct without
// KnownFields and therefore ignores keys it does not recognise. That is what lets
// Loop add its own `git:` block without breaking a user who still runs both
// tools against the same project.
func (s *Session) SaveConfig(cfg config.Config) error {
	root, err := s.requireProject()
	if err != nil {
		return err
	}
	if err := config.Save(root, &cfg); err != nil {
		return fmt.Errorf("save .chief/config.yaml: %w", err)
	}

	s.mu.Lock()
	s.cfg = &cfg
	s.mu.Unlock()

	s.bus.publish(Event{Kind: EvConfigChanged})
	return nil
}

// Usage returns the absolute cumulative usage roll-up for the open project,
// attributed by attempt, story and run.
func (s *Session) Usage() UsageReport {
	return s.usage.report()
}

// Runs returns a snapshot of every run in this session.
func (s *Session) Runs() []RunSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runsLocked()
}

// runsLocked builds the run list, reading live runs from the run itself.
//
// The stored copy is only refreshed on terminal transitions, so consulting it
// for a running run reports whatever state it was in when it started. Reading
// through to the live run means the answer cannot go stale, and no future
// transition has to remember to write the map. Callers hold s.mu.
func (s *Session) runsLocked() []RunSnapshot {
	out := make([]RunSnapshot, 0, len(s.runs))
	for id, stored := range s.runs {
		if live, ok := s.live[id]; ok {
			out = append(out, live.snapshot())
			continue
		}
		out = append(out, *stored)
	}
	return out
}

// PendingQuestions returns every unanswered question.
func (s *Session) PendingQuestions() []Question {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Question, 0, len(s.questions))
	for _, q := range s.questions {
		out = append(out, q)
	}
	return out
}

// Close shuts the session down and closes the event channel once the queue has
// drained. Safe to call more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		// Authoring sessions are interactive subprocesses holding a PTY; leaving
		// them behind would strand an agent with no window attached to it.
		s.authoring.StopAll()
		s.bus.close()
	})
	return nil
}

func (s *Session) requireProject() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.project == nil {
		return "", fmt.Errorf("no project is open")
	}
	return s.project.Root, nil
}

// ProgressEntry is one dated note the agent appended to progress.md for a story.
// Mirrors chief's prd.ProgressEntry with JSON tags for the TypeScript boundary.
type ProgressEntry struct {
	StoryID string `json:"storyId"`
	Date    string `json:"date"`
	Content string `json:"content"`
}
