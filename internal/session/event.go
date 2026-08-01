package session

import "github.com/dripcoder/loop/internal/chief/loop"

// Event is the single thing a Session emits. Every consumer — the Wails bridge,
// loopctl, tests — reads one ordered, sequence-numbered stream of these.
//
// One stream rather than several named ones is deliberate. Wails' Emit offers no
// ordering guarantee across event names and no delivery confirmation, so
// separate channels would let a frontend observe a push before the story that
// caused it. A single monotonic stream plus Replay/Snapshot is the only shape
// that stays correct over a lossy transport.
//
// Field notes for the TypeScript boundary:
//   - Kind is a string enum, never an int. Integers turn into magic numbers in
//     the frontend and reorder silently when a constant is inserted.
//   - At is unix milliseconds, never time.Time, which does not bind cleanly.
//   - Errors travel as *ErrorInfo. Go's error interface is not bindable.
type Event struct {
	Seq  uint64    `json:"seq"` // monotonic per session, starts at 1
	At   int64     `json:"at"`  // unix milliseconds
	Kind EventKind `json:"kind"`

	// Dropped is the number of coalescible events discarded immediately before
	// this one because the consumer had fallen behind. Non-zero means the stream
	// has a gap: Replay from the last seen sequence, or take a Snapshot if the
	// ring has already rolled.
	//
	// It rides on a real event rather than getting an event of its own so that a
	// backed-up consumer cannot be flooded by the very notices telling it that it
	// is backed up.
	Dropped uint64 `json:"dropped,omitempty"`

	RunID   string `json:"runId,omitempty"`
	PRD     string `json:"prd,omitempty"`
	StoryID string `json:"storyId,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Text    string `json:"text,omitempty"`

	Agent    *AgentEvent  `json:"agent,omitempty"`
	Step     *StepEvent   `json:"step,omitempty"`
	Git      *GitEvent    `json:"git,omitempty"`
	Question *Question    `json:"question,omitempty"`
	Story    *StorySnap   `json:"story,omitempty"`
	Run      *RunSnapshot `json:"run,omitempty"`
	Error    *ErrorInfo   `json:"error,omitempty"`
}

// EventKind names what happened. Dotted lowercase so the frontend can group by
// prefix without a lookup table.
type EventKind string

const (
	// Project lifecycle.
	EvProjectOpened  EventKind = "project.opened"
	EvPRDChanged     EventKind = "prd.changed"
	EvProgressChange EventKind = "progress.changed"
	EvConfigChanged  EventKind = "config.changed"

	// Run lifecycle.
	EvRunStarted  EventKind = "run.started"
	EvRunPaused   EventKind = "run.paused"
	EvRunResumed  EventKind = "run.resumed"
	EvRunStopped  EventKind = "run.stopped"
	EvRunComplete EventKind = "run.complete"
	EvRunError    EventKind = "run.error"

	// Story lifecycle.
	EvStoryStarted EventKind = "story.started"
	EvStoryAttempt EventKind = "story.attempt"
	EvStoryDone    EventKind = "story.done"
	EvStoryFailed  EventKind = "story.failed"
	EvStorySkipped EventKind = "story.skipped"

	// Agent output. These are the high-volume kinds and the only ones the bus is
	// allowed to drop under pressure.
	EvAgentText       EventKind = "agent.text"
	EvAgentTool       EventKind = "agent.tool"
	EvAgentToolResult EventKind = "agent.toolResult"
	EvAgentRetry      EventKind = "agent.retry"
	EvAgentWatchdog   EventKind = "agent.watchdog"

	// Worktree provisioning, setup commands, stack steps.
	EvStep EventKind = "step"

	// Branch / commit / push / PR, including non-fatal failures.
	EvGit EventKind = "git"

	// Blocking decisions surfaced as data rather than dialogs.
	EvQuestionAsked    EventKind = "question.asked"
	EvQuestionResolved EventKind = "question.resolved"
)

// coalescible reports whether the bus may discard this event under backpressure.
//
// Only agent chatter qualifies. Losing a line of assistant text degrades the log;
// losing a story.done corrupts the caller's model of the world. Everything not
// listed here is guaranteed to be delivered, and the bus blocks-or-drops-oldest
// accordingly.
func (k EventKind) coalescible() bool {
	switch k {
	case EvAgentText, EvAgentToolResult:
		return true
	default:
		return false
	}
}

// AgentEvent carries the parsed agent output for the agent.* kinds.
type AgentEvent struct {
	Tool       string         `json:"tool,omitempty"`
	ToolInput  map[string]any `json:"toolInput,omitempty"`
	RetryCount int            `json:"retryCount,omitempty"`
	RetryMax   int            `json:"retryMax,omitempty"`
}

// StepEvent is a unit of visible progress in a multi-step operation: worktree
// provisioning, a setup command, a stack operation.
type StepEvent struct {
	Group  string `json:"group"` // "worktree" | "setup" | "stack"
	Name   string `json:"name"`  // "create-branch" | "add-worktree" | "run-setup"
	State  string `json:"state"` // "running" | "ok" | "error" | "skipped"
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Output string `json:"output,omitempty"` // streamed tail, e.g. from `npm ci`
}

// GitEvent reports a git or GitHub operation.
//
// Fatal is false for every per-story git failure: the chosen policy is to open a
// PR and keep going, so a failed push must not stop the run. It still has to be
// impossible to miss, which is the UI's job — see RetryGit.
type GitEvent struct {
	Op         string `json:"op"` // "branch"|"commit-verify"|"push"|"pr-create"|"checkout"|"stack"
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"baseBranch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	PRNumber   int    `json:"prNumber,omitempty"`
	PRURL      string `json:"prUrl,omitempty"`
	State      string `json:"state"` // "running" | "ok" | "warn" | "error"
	Fatal      bool   `json:"fatal"`
	Hint       string `json:"hint,omitempty"` // remedy, e.g. "gh auth login"
}

// ErrorInfo is a bindable error.
type ErrorInfo struct {
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func errInfo(err error, hint string) *ErrorInfo {
	if err == nil {
		return nil
	}
	return &ErrorInfo{Message: err.Error(), Hint: hint}
}

// agentEventKind maps a vendored chief loop.EventType onto our kind vocabulary.
// The bool is false for chief event types that the session handles structurally
// rather than forwarding (completion, max-iterations, story-done), since those
// drive the run state machine and are re-emitted with richer payloads.
func agentEventKind(t loop.EventType) (EventKind, bool) {
	switch t {
	case loop.EventAssistantText:
		return EvAgentText, true
	case loop.EventToolStart:
		return EvAgentTool, true
	case loop.EventToolResult:
		return EvAgentToolResult, true
	case loop.EventRetrying:
		return EvAgentRetry, true
	case loop.EventWatchdogTimeout:
		return EvAgentWatchdog, true
	default:
		return "", false
	}
}
