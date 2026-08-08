package session

// The types in this file are the session's read model: everything a front-end
// needs to render, with JSON tags chosen for the TypeScript boundary. They are
// deliberately plain data — no methods with side effects, no pointers into
// session internals — so they can be marshalled, cached and diffed freely.

// Project is an opened working directory.
type Project struct {
	Root        string `json:"root"`
	Name        string `json:"name"`
	IsGitRepo   bool   `json:"isGitRepo"`
	Branch      string `json:"branch,omitempty"`
	DefaultBase string `json:"defaultBase,omitempty"` // repo default branch, e.g. "main"
	HasChiefDir bool   `json:"hasChiefDir"`
	// ChiefIgnored reports whether .chief/ is excluded from version control.
	// Per-story mode refuses to run when it is not: the agent would otherwise
	// commit Loop's own state file into the story branches it creates.
	ChiefIgnored bool `json:"chiefIgnored"`
}

// PRDSummary is the rail-row view of a PRD: enough to render progress and state
// without parsing the whole document.
type PRDSummary struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	Total      int    `json:"total"`
	Completed  int    `json:"completed"`
	InProgress int    `json:"inProgress"`
	// Legacy is true for a PRD at the pre-0.7 .chief/prd.md location rather than
	// .chief/prds/<name>/prd.md.
	Legacy bool `json:"legacy"`
	// ParseError is non-empty when prd.md exists but could not be parsed. The PRD
	// is still listed — hiding it would leave the user with no way to fix it.
	ParseError string `json:"parseError,omitempty"`

	Worktree string    `json:"worktree,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	State    LoopState `json:"state"`

	// PR is the pull request for this PRD's own branch, in per-PRD mode. Nil
	// when there is none, when the branch has never been pushed, or when no
	// lookup has run yet.
	PR *PRRef `json:"pr,omitempty"`
}

// PRDDetail is the full document plus its progress journal.
type PRDDetail struct {
	PRDSummary
	Description string      `json:"description"`
	Stories     []StorySnap `json:"stories"`
}

// StorySnap is one user story as the UI sees it.
//
// It flattens chief's prd.UserStory (whose Passes/InProgress booleans are an
// awkward pair to render) into a single Status, and carries the branch and PR
// state that per-story stacked mode adds. Those live here rather than in the PRD
// markdown on purpose: prd.md is authored by the agent, and asking it to maintain
// pull-request bookkeeping would be both fragile and outside its job.
type StorySnap struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Criteria    []string `json:"criteria,omitempty"`
	Priority    float64  `json:"priority"`
	Status      Status   `json:"status"`

	// CriteriaAreAuthoritative is false once a story is done.
	//
	// chief's SetStoryStatus(id, "done") ticks every unchecked acceptance-criteria
	// box as a side effect, so a completed story's checklist reflects the status
	// write rather than anything that was verified. The UI must label it as such
	// instead of presenting it as evidence.
	CriteriaAreAuthoritative bool `json:"criteriaAreAuthoritative"`

	Branch     string      `json:"branch,omitempty"`
	PR         *PRRef      `json:"pr,omitempty"`
	DurationMs int64       `json:"durationMs,omitempty"`
	Errors     []ErrorInfo `json:"errors,omitempty"`
}

// Status is a story's lifecycle position.
type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in-progress"
	StatusDone       Status = "done"
	StatusBlocked    Status = "blocked"
)

// PRRef is a pull request belonging to a story or to a PRD.
type PRRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"` // "OPEN" | "MERGED" | "CLOSED"
	Draft  bool   `json:"draft"`
	Base   string `json:"base,omitempty"`
	// Head is the branch the pull request was opened from. It is what the cache
	// is keyed on, and what lets a PR be matched to a story without GitHub
	// knowing anything about stories.
	Head string `json:"head,omitempty"`

	// CheckedAt is when this state was last confirmed against GitHub, in unix
	// milliseconds.
	//
	// It is the only freshness signal, deliberately. A pull request merged or
	// closed outside Loop looks exactly like one still open until something
	// re-reads it, and how much that matters depends on how old the answer is —
	// which a boolean cannot say. The link is always worth showing; the state
	// beside it is worth trusting in proportion to this.
	CheckedAt int64 `json:"checkedAt,omitempty"`
}

// LoopState is the run state of one PRD. Several PRDs run concurrently, so this
// is always per-PRD and never global.
type LoopState string

const (
	StateIdle     LoopState = "idle"
	StateRunning  LoopState = "running"
	StatePaused   LoopState = "paused"
	StateStopped  LoopState = "stopped"
	StateComplete LoopState = "complete"
	StateError    LoopState = "error"
	// StateAwaiting means the run is blocked on a Question the UI must answer.
	StateAwaiting LoopState = "awaiting-answer"
)

// RunSnapshot is the live state of a single run.
type RunSnapshot struct {
	ID            string    `json:"id"`
	PRD           string    `json:"prd"`
	State         LoopState `json:"state"`
	StoryID       string    `json:"storyId,omitempty"`
	Attempt       int       `json:"attempt"`
	AttemptBudget int       `json:"attemptBudget"`
	StartedAt     int64     `json:"startedAt,omitempty"`
	FinishedAt    int64     `json:"finishedAt,omitempty"`
	Worktree      string    `json:"worktree,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	// Model is the provider model this session is actually using, learned from
	// the agent's own usage output rather than from configured defaults — a
	// session may run an override or a provider-selected fallback.
	Model string `json:"model,omitempty"`
	// ModelUnavailable reports that the agent has produced usage but named no
	// model, which several CLIs never do. It is what separates "not reported by
	// this provider" from "not resolved yet"; showing either as the other would
	// be a guess.
	ModelUnavailable bool       `json:"modelUnavailable,omitempty"`
	Error            *ErrorInfo `json:"error,omitempty"`
	// PendingGitErrors counts non-fatal git failures needing attention. The run
	// continues regardless; this drives the "N git actions need attention" banner.
	PendingGitErrors int `json:"pendingGitErrors"`
}

// Snapshot is the whole observable state of a session. A consumer that has lost
// its place in the event stream can call Snapshot and start again from
// Snapshot.Seq without any replay.
type Snapshot struct {
	Seq         uint64        `json:"seq"`
	Project     *Project      `json:"project,omitempty"`
	PRDs        []PRDSummary  `json:"prds"`
	Runs        []RunSnapshot `json:"runs"`
	Questions   []Question    `json:"questions"`
	Environment Environment   `json:"environment"`
	// Usage is the absolute cumulative usage roll-up, so a reconnecting consumer
	// adopts the totals wholesale rather than replaying deltas.
	Usage UsageReport `json:"usage"`
}

// ---------------------------------------------------------------- questions --

// Question is a blocking decision expressed as data.
//
// chief asks these with modal dialogs inside its TUI, which welds the policy to
// the presentation. Here the session states the choice and the caller answers
// however it likes — a modal, a CLI prompt, or a hardcoded reply in a test.
// Nothing blocks while a question is outstanding; the run simply sits in
// StateAwaiting.
type Question struct {
	ID      QuestionID   `json:"id"`
	RunID   string       `json:"runId,omitempty"`
	PRD     string       `json:"prd,omitempty"`
	Kind    QuestionKind `json:"kind"`
	Title   string       `json:"title"`
	Body    string       `json:"body,omitempty"`
	Options []Option     `json:"options"`
	// Inputs are free-text fields the answer may carry, e.g. an editable branch
	// name on the branch-safety question.
	Inputs        []Input `json:"inputs,omitempty"`
	DefaultOption string  `json:"defaultOption"`
}

// QuestionID identifies an outstanding question.
type QuestionID string

// QuestionKind is the decision being asked about.
type QuestionKind string

const (
	QBranchSafety   QuestionKind = "branch-safety"
	QWorktreeExists QuestionKind = "worktree-exists"
	QDirtyWorktree  QuestionKind = "dirty-worktree"
	QStoryNoCommit  QuestionKind = "story-no-commit"
	QBranchConflict QuestionKind = "branch-conflict"
	QPRExists       QuestionKind = "pr-exists"
	QQuitWithRuns   QuestionKind = "quit-with-running"
)

// Option is one mutually exclusive answer.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Hint        string `json:"hint,omitempty"` // e.g. the target path
	Recommended bool   `json:"recommended"`
	Destructive bool   `json:"destructive"`
}

// Input is a field attached to a question, answered alongside the option rather
// than as a second prompt.
//
// Free text when Choices is empty, a selection between fixed values otherwise.
// One type rather than two because the answer carries both the same way — as a
// string under Key — and because the UI renders them in the same row of the same
// dialog.
type Input struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Value       string `json:"value"`
	Placeholder string `json:"placeholder,omitempty"`
	// Choices, when present, are the only values the answer may carry for this
	// key. Value is the preselected one.
	Choices []Choice `json:"choices,omitempty"`
	// ReadOnly states the field's value without offering to change it. It is not
	// a disabled control: a decision that has already been made is worth showing,
	// and worth showing as settled rather than as something the user failed to
	// reach.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Hint explains the field, e.g. why a read-only one can no longer change.
	Hint string `json:"hint,omitempty"`
}

// Choice is one value an Input may take.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// BranchLayout is how a run arranges its commits across branches.
//
// It is decided when a run starts rather than when the PRD is created, because it
// determines what the commits look like and that is only worth deciding with the
// work in front of you. It is recorded per PRD so the second run of a PRD does
// not have to remember what the first one chose.
type BranchLayout string

const (
	// LayoutOneBranch puts every story's commit on one branch for the whole PRD.
	LayoutOneBranch BranchLayout = "one-branch"
	// LayoutBranchPerStory gives every story its own branch, each based on the
	// one below it.
	LayoutBranchPerStory BranchLayout = "branch-per-story"
)

// Label is the layout as the user was offered it.
func (l BranchLayout) Label() string {
	if l == LayoutBranchPerStory {
		return "A branch per story"
	}
	return "One branch for the whole PRD"
}

// isKnown rejects a layout the engine cannot act on, so a stale or malformed
// answer fails loudly instead of silently reshaping the run.
func (l BranchLayout) isKnown() bool {
	return l == LayoutOneBranch || l == LayoutBranchPerStory
}

// Answer resolves a Question.
type Answer struct {
	OptionID string            `json:"optionId"`
	Inputs   map[string]string `json:"inputs,omitempty"`
}

// ------------------------------------------------------------- environment --

// Environment is what Loop found on this machine. It drives both the first-run
// experience and the degraded modes: no gh means no pull requests, no gh-stack
// means the manual stacking fallback rather than a native GitHub Stack.
type Environment struct {
	Git       Tool   `json:"git"`
	GH        Tool   `json:"gh"`
	GHAuth    bool   `json:"ghAuth"`
	GHStack   Tool   `json:"ghStack"`
	Agents    []Tool `json:"agents"`
	ChiefRef  string `json:"chiefRef"`
	Platform  string `json:"platform"`
	StackMode string `json:"stackMode"` // resolved driver: "gh-stack" | "manual" | "none"
}

// Tool is one external dependency probe.
type Tool struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	// Hint is the remedy when Available is false, e.g. an install command.
	Hint string `json:"hint,omitempty"`
}
