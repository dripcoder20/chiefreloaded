package session

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
	"github.com/dripcoder/loop/internal/chief/prd"
)

// StartRequest describes a run to begin.
type StartRequest struct {
	// PRD is the name of the PRD to run.
	PRD string `json:"prd"`
	// AttemptBudget caps how many agent attempts the run may spend in total.
	// Zero means derive it: max(5, incomplete stories + 5), matching chief.
	AttemptBudget int `json:"attemptBudget,omitempty"`
	// WorkDir is where the agent runs. Empty means the project root.
	WorkDir string `json:"workDir,omitempty"`
	// Provider overrides the configured agent CLI.
	Provider string `json:"provider,omitempty"`
}

// minimumAttemptBudget mirrors chief's floor. A budget below this makes even a
// one-story PRD fail on a single transient hiccup.
const minimumAttemptBudget = 5

// attemptBudgetFor derives the default budget: one attempt per remaining story
// plus five spare. Ported verbatim from chief's app.go, where it is computed
// inline during view construction and therefore cannot be tested.
func attemptBudgetFor(prdPath string) int {
	n := chiefloop.IncompleteStoryCount(prdPath) + minimumAttemptBudget
	if n < minimumAttemptBudget {
		return minimumAttemptBudget
	}
	return n
}

// run is one PRD executing. Its goroutine is the only writer of its own state.
type run struct {
	id      string
	prdName string
	prdPath string
	workDir string

	provider chiefloop.Provider
	sess     *Session

	mu       sync.Mutex
	state    LoopState
	storyID  string
	attempt  int
	budget   int
	started  time.Time
	finished time.Time
	err      *ErrorInfo
	gitErrs  int
	timings  map[string]time.Duration

	// active is the Loop currently running an attempt, so Stop can kill the
	// subprocess immediately rather than waiting for it to finish.
	active *chiefloop.Loop

	// stack is the per-story branch and pull-request bookkeeping, created lazily
	// on the first completed story and nil when per-story mode is off.
	stack *stackState

	cancel  context.CancelFunc
	pauseCh chan struct{} // closed to wake a paused run
	paused  bool
	stopped bool
	done    chan struct{}
}

// Start begins running a PRD and returns immediately.
//
// Nothing about this call blocks: the run's own goroutine drives the story loop
// and reports through the event stream. A run that needs a decision from the
// user parks in StateAwaiting and emits a Question rather than blocking here.
func (s *Session) Start(ctx context.Context, req StartRequest) (string, error) {
	root, err := s.requireProject()
	if err != nil {
		return "", err
	}

	var summary *PRDSummary
	for _, p := range s.PRDs() {
		if p.Name == req.PRD {
			cp := p
			summary = &cp
			break
		}
	}
	if summary == nil {
		return "", fmt.Errorf("no PRD named %q", req.PRD)
	}
	if summary.ParseError != "" {
		return "", fmt.Errorf("PRD %q cannot be parsed: %s", req.PRD, summary.ParseError)
	}

	s.mu.Lock()
	for _, existing := range s.runs {
		if existing.PRD == req.PRD && isLive(existing.State) {
			s.mu.Unlock()
			return "", fmt.Errorf("PRD %q is already running", req.PRD)
		}
	}
	s.mu.Unlock()

	provider, err := s.resolveProvider(req.Provider)
	if err != nil {
		return "", err
	}

	workDir := req.WorkDir
	if workDir == "" {
		workDir = root
	}

	budget := req.AttemptBudget
	if budget <= 0 {
		budget = attemptBudgetFor(summary.Path)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r := &run{
		id:       s.nextRunID(),
		prdName:  req.PRD,
		prdPath:  summary.Path,
		workDir:  workDir,
		provider: provider,
		sess:     s,
		state:    StateRunning,
		budget:   budget,
		started:  s.now(),
		timings:  make(map[string]time.Duration),
		cancel:   cancel,
		pauseCh:  make(chan struct{}),
		done:     make(chan struct{}),
	}

	s.mu.Lock()
	s.runs[r.id] = r.snapshotLocked()
	s.live[r.id] = r
	s.mu.Unlock()

	s.publish(Event{Kind: EvRunStarted, RunID: r.id, PRD: r.prdName, Run: ptr(r.snapshot())})
	go r.loop(runCtx)
	return r.id, nil
}

// loop drives stories until the PRD is finished, the budget runs out, or the
// caller pauses or stops.
func (r *run) loop(ctx context.Context) {
	defer close(r.done)

	for {
		if stop := r.awaitResume(ctx); stop {
			return
		}

		r.mu.Lock()
		spent, budget := r.attempt, r.budget
		r.mu.Unlock()

		if spent >= budget {
			// Out of attempts is a pause, not a failure: the work so far is
			// sound and raising the budget is a legitimate response.
			r.finish(StatePaused, EvRunPaused,
				fmt.Sprintf("attempt budget exhausted (%d); raise it to continue", budget), nil)
			return
		}

		outcome, err := r.runOneStory(ctx)
		switch {
		case err == chiefloop.ErrAllStoriesComplete:
			r.finish(StateComplete, EvRunComplete, "", nil)
			return
		case err != nil:
			if ctx.Err() != nil || r.isStopped() {
				r.finish(StateStopped, EvRunStopped, "", nil)
				return
			}
			r.finish(StateError, EvRunError, err.Error(), errInfo(err, ""))
			return
		case outcome == outcomeStoryFailed:
			r.finish(StatePaused, EvRunPaused,
				"story could not be completed; review the log and start again", nil)
			return
		}
	}
}

type outcome int

const (
	outcomeProgressed outcome = iota
	outcomeStoryFailed
)

// runOneStory performs a single agent attempt and decides what it achieved.
func (r *run) runOneStory(ctx context.Context) (outcome, error) {
	storyID, storyTitle := chiefloop.NextStoryID(r.prdPath)
	if storyID == "" {
		return outcomeProgressed, chiefloop.ErrAllStoriesComplete
	}

	headBefore := revParseHead(ctx, r.workDir)
	branchBefore := currentBranch(ctx, r.workDir)

	r.mu.Lock()
	r.attempt++
	attempt := r.attempt
	r.storyID = storyID
	r.mu.Unlock()

	r.sess.publish(Event{
		Kind: EvStoryStarted, RunID: r.id, PRD: r.prdName,
		StoryID: storyID, Attempt: attempt, Text: storyTitle,
	})

	l := chiefloop.NewStoryLoop(r.prdPath, r.workDir, r.provider)
	r.mu.Lock()
	r.active = l
	r.mu.Unlock()

	// Forward the agent's events into the session stream. The Loop's channel is
	// buffered at 100 and blocks its producer when full, so this must keep
	// draining for as long as the attempt lasts.
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		r.forwardAgentEvents(l, storyID, attempt)
	}()

	startedAt := r.sess.now()
	result, err := l.RunStory(ctx)
	<-forwarded

	r.mu.Lock()
	r.active = nil
	r.timings[storyID] += r.sess.now().Sub(startedAt)
	r.mu.Unlock()

	if err != nil {
		return outcomeProgressed, err
	}

	// The agent is gone by here — RunStory returns after cmd.Wait. That is what
	// makes it safe to touch git at all.
	return r.settleStory(ctx, storyID, storyTitle, headBefore, branchBefore, result)
}

// settleStory verifies the attempt and records the outcome.
func (r *run) settleStory(
	ctx context.Context,
	storyID, storyTitle, headBefore, branchBefore string,
	result chiefloop.StoryRun,
) (outcome, error) {
	check := verifyCommit(ctx, r.workDir, headBefore, storyID, storyTitle)

	// The prompt forbids the agent from touching branches. If it did anyway,
	// say so loudly and carry on from where it actually left us — fighting it
	// here would just strand the commits somewhere nobody looks.
	if after := currentBranch(ctx, r.workDir); after != branchBefore && branchBefore != "" {
		r.sess.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text: fmt.Sprintf("the agent switched branches: %s -> %s", branchBefore, after),
			Git: &GitEvent{
				Op: "checkout", Branch: after, BaseBranch: branchBefore,
				State: "warn", Fatal: false,
			},
		})
	}

	if len(check.Dirty) > 0 {
		r.sess.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text: fmt.Sprintf("%d uncommitted path(s) left behind: %s",
				len(check.Dirty), strings.Join(truncate(check.Dirty, 5), ", ")),
			Git: &GitEvent{Op: "commit-verify", State: "warn", Fatal: false},
		})
	}

	switch {
	case check.Verdict == VerdictCommitted, check.Verdict == VerdictWrongSubject:
		if check.Verdict == VerdictWrongSubject {
			r.sess.publish(Event{
				Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
				Text: "committed, but no commit matched the expected subject",
				Git:  &GitEvent{Op: "commit-verify", State: "warn", Fatal: false},
			})
		}
		return r.markDone(ctx, storyID, storyTitle, check)

	case result.DoneTag:
		// The agent says it finished but committed nothing. Genuinely ambiguous:
		// a docs-only or already-satisfied story is a real no-op. Accept it,
		// record it as such, and let the stacking layer skip the PR.
		r.sess.publish(Event{
			Kind: EvStorySkipped, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text: "agent reported the story complete without committing; recorded as a no-op",
		})
		return r.markDone(ctx, storyID, storyTitle, check)

	default:
		// No commit and no claim of completion: the attempt simply did not land.
		// Leave the story in-progress and let the budget decide whether to
		// try again.
		r.mu.Lock()
		spent, budget := r.attempt, r.budget
		r.mu.Unlock()
		if spent >= budget {
			r.sess.publish(Event{
				Kind: EvStoryFailed, RunID: r.id, PRD: r.prdName, StoryID: storyID,
				Text: "no commit and no completion signal; attempt budget exhausted",
			})
			return outcomeStoryFailed, nil
		}
		r.sess.publish(Event{
			Kind: EvStoryAttempt, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Attempt: spent, Text: "no commit and no completion signal; retrying",
		})
		return outcomeProgressed, nil
	}
}

// markDone writes the story's status and reports it.
//
// This is the write chief performs the moment the sentinel appears. Doing it
// here, after verification, is the whole reason RunStory exists.
func (r *run) markDone(ctx context.Context, storyID, storyTitle string, check CommitCheck) (outcome, error) {
	if err := prd.SetStoryStatus(r.prdPath, storyID, "done"); err != nil {
		return outcomeProgressed, fmt.Errorf("mark %s done: %w", storyID, err)
	}

	r.mu.Lock()
	elapsed := r.timings[storyID]
	r.mu.Unlock()

	r.sess.publish(Event{
		Kind: EvStoryDone, RunID: r.id, PRD: r.prdName, StoryID: storyID,
		Text: storyTitle,
		Story: &StorySnap{
			ID: storyID, Title: storyTitle, Status: StatusDone,
			DurationMs:               elapsed.Milliseconds(),
			CriteriaAreAuthoritative: false,
		},
	})

	// Hook point for M7: push the story branch, open its draft PR, and cut the
	// next branch off it. Deliberately after the status write and after the
	// agent process is gone.
	if err := r.sess.afterStoryDone(ctx, r, storyID, storyTitle, check); err != nil {
		r.noteGitError()
		r.sess.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text:  err.Error(),
			Git:   &GitEvent{Op: "stack", State: "error", Fatal: false},
			Error: errInfo(err, ""),
		})
	}

	_ = r.sess.Rescan(ctx)
	return outcomeProgressed, nil
}

// forwardAgentEvents republishes the agent's parsed output onto the session
// stream. It must run until the Loop closes its channel, or the Loop's producer
// — the goroutine scanning the agent's stdout — will block.
func (r *run) forwardAgentEvents(l *chiefloop.Loop, storyID string, attempt int) {
	for ev := range l.Events() {
		kind, ok := agentEventKind(ev.Type)
		if !ok {
			// Story-done, completion and max-iteration events drive the state
			// machine instead, and are re-emitted with better payloads.
			continue
		}
		out := Event{
			Kind: kind, RunID: r.id, PRD: r.prdName,
			StoryID: storyID, Attempt: attempt, Text: ev.Text,
		}
		if ev.Tool != "" || ev.RetryCount > 0 {
			out.Agent = &AgentEvent{
				Tool: ev.Tool, ToolInput: ev.ToolInput,
				RetryCount: ev.RetryCount, RetryMax: ev.RetryMax,
			}
		}
		r.sess.publish(out)
	}
}

// awaitResume blocks while paused. Returns true when the run should end.
func (r *run) awaitResume(ctx context.Context) bool {
	for {
		r.mu.Lock()
		if r.stopped {
			r.mu.Unlock()
			r.finish(StateStopped, EvRunStopped, "", nil)
			return true
		}
		if !r.paused {
			r.mu.Unlock()
			return ctx.Err() != nil
		}
		wait := r.pauseCh
		r.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
			return true
		}
	}
}

func (r *run) finish(state LoopState, kind EventKind, text string, e *ErrorInfo) {
	r.mu.Lock()
	r.state = state
	r.finished = r.sess.now()
	r.err = e
	r.storyID = ""
	r.mu.Unlock()

	// chief leaves a story wedged as in-progress when a run ends any way other
	// than cleanly, and never clears it on startup either — so a crash strands
	// it forever. Clear it on every terminal transition.
	r.clearInProgress()

	snap := r.snapshot()
	r.sess.mu.Lock()
	r.sess.runs[r.id] = &snap
	r.sess.mu.Unlock()

	r.sess.publish(Event{Kind: kind, RunID: r.id, PRD: r.prdName, Text: text, Run: &snap, Error: e})
}

// clearInProgress resets any story left mid-flight back to todo.
func (r *run) clearInProgress() {
	doc, err := prd.LoadPRD(r.prdPath)
	if err != nil {
		return
	}
	for _, st := range doc.UserStories {
		if st.InProgress && !st.Passes {
			_ = prd.SetStoryStatus(r.prdPath, st.ID, "todo")
		}
	}
}

func (r *run) noteGitError() {
	r.mu.Lock()
	r.gitErrs++
	r.mu.Unlock()
}

func (r *run) isStopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

func (r *run) snapshot() RunSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return *r.snapshotLocked()
}

func (r *run) snapshotLocked() *RunSnapshot {
	snap := RunSnapshot{
		ID: r.id, PRD: r.prdName, State: r.state, StoryID: r.storyID,
		Attempt: r.attempt, AttemptBudget: r.budget,
		Worktree: r.workDir, Error: r.err, PendingGitErrors: r.gitErrs,
	}
	if r.provider != nil {
		snap.Provider = r.provider.Name()
	}
	if !r.started.IsZero() {
		snap.StartedAt = r.started.UnixMilli()
	}
	if !r.finished.IsZero() {
		snap.FinishedAt = r.finished.UnixMilli()
	}
	return &snap
}

// --------------------------------------------------------------- controls --

// Pause stops the run after the current story attempt finishes. The agent is
// left alone to complete what it started; use Stop to kill it now.
func (s *Session) Pause(runID string) error {
	r, err := s.liveRun(runID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.paused {
		r.mu.Unlock()
		return nil
	}
	r.paused = true
	r.state = StatePaused
	r.mu.Unlock()

	s.publish(Event{Kind: EvRunPaused, RunID: r.id, PRD: r.prdName,
		Text: "pausing after the current story"})
	return nil
}

// Resume continues a paused run.
func (s *Session) Resume(runID string) error {
	r, err := s.liveRun(runID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if !r.paused {
		r.mu.Unlock()
		return nil
	}
	r.paused = false
	r.state = StateRunning
	close(r.pauseCh)
	r.pauseCh = make(chan struct{})
	r.mu.Unlock()

	s.publish(Event{Kind: EvRunResumed, RunID: r.id, PRD: r.prdName})
	return nil
}

// Stop ends the run immediately, killing the agent subprocess.
func (s *Session) Stop(runID string) error {
	r, err := s.liveRun(runID)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.stopped = true
	active := r.active
	if r.paused {
		r.paused = false
		close(r.pauseCh)
		r.pauseCh = make(chan struct{})
	}
	r.mu.Unlock()

	if active != nil {
		active.Stop()
	}
	r.cancel()
	return nil
}

// SetAttemptBudget changes how many attempts a live run may still spend.
func (s *Session) SetAttemptBudget(runID string, budget int) error {
	if budget < minimumAttemptBudget {
		return fmt.Errorf("attempt budget must be at least %d", minimumAttemptBudget)
	}
	r, err := s.liveRun(runID)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.budget = budget
	wasPaused := r.paused
	r.mu.Unlock()

	// Raising the budget is the natural response to "budget exhausted", so make
	// it actually continue rather than requiring a separate Resume.
	if wasPaused {
		return s.Resume(runID)
	}
	s.publish(Event{Kind: EvRunStarted, RunID: r.id, PRD: r.prdName, Run: ptr(r.snapshot())})
	return nil
}

// WaitForRun blocks until a run reaches a terminal state. For tests and for
// loopctl, which has nothing else to do meanwhile.
func (s *Session) WaitForRun(ctx context.Context, runID string) (RunSnapshot, error) {
	s.mu.RLock()
	r := s.live[runID]
	s.mu.RUnlock()
	if r == nil {
		return RunSnapshot{}, fmt.Errorf("no run %q", runID)
	}

	select {
	case <-r.done:
		return r.snapshot(), nil
	case <-ctx.Done():
		return r.snapshot(), ctx.Err()
	}
}

func (s *Session) liveRun(runID string) (*run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.live[runID]
	if !ok {
		return nil, fmt.Errorf("no run %q", runID)
	}
	return r, nil
}

func (s *Session) nextRunID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runSeq++
	return "run_" + strconv.Itoa(s.runSeq)
}

func isLive(state LoopState) bool {
	return state == StateRunning || state == StatePaused || state == StateAwaiting
}

func truncate(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(items[:n:n], fmt.Sprintf("and %d more", len(items)-n))
}

func ptr[T any](v T) *T { return &v }
