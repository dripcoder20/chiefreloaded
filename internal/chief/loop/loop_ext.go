package loop

// This file is Loop's, not chief's. It lives inside the vendored package so it
// can reach unexported fields (buildPrompt, sawStoryDone, currentStoryID) and
// reuse runIterationWithRetry. scripts/sync-upstream.sh never deletes *_ext.go,
// so no patch against upstream is needed and re-syncing stays a clean
// regeneration.
//
// Why this exists: Loop drives the agent one story at a time, whereas chief's
// Run loops until the whole PRD is finished. Two things force that.
//
// Commit verification has to happen before the status write. Run calls
// SetStoryStatus(id, "done") the instant the agent emits the done sentinel, and
// that call also ticks every acceptance-criteria checkbox. By the time any hook
// placed after it could look, the PRD already claims the story passed. We need
// to check whether the agent actually committed anything first, and decide.
//
// Branch switching needs a process that is provably gone. Per-story stacking
// runs `git checkout -b` between stories; doing that while the agent subprocess
// is still alive is the most destructive failure available to this program.
// Returning after cmd.Wait() makes it structural rather than a matter of getting
// defer ordering right.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dripcoder/loop/internal/chief/prd"
)

// ErrAllStoriesComplete reports that the PRD has no story left to run.
//
// chief signals this by having the prompt builder fail and translating that into
// an event. A sentinel error is easier to act on than an event when the caller
// is a state machine rather than a render loop.
var ErrAllStoriesComplete = errors.New("all stories are complete")

// StoryRun is the outcome of one attempt at one story.
type StoryRun struct {
	// StoryID is the story the agent was asked to implement.
	StoryID string
	// Title is its title at the time of the attempt, captured before any status
	// write so a PR body can quote what was actually asked for.
	Title string
	// DoneTag reports whether the agent emitted the completion sentinel. It says
	// the agent believes it finished, which is not the same as it having
	// committed anything — the caller verifies that separately.
	DoneTag bool
	// Iteration is the loop's iteration counter for this attempt.
	Iteration int
}

// NewStoryLoop builds a Loop that runs the agent for exactly one story.
//
// workDir is where the agent runs — a git worktree, or the project root. Pass
// the same prdPath the session is tracking; the prompt is rebuilt from it on
// every attempt so a story completed out of band is skipped.
func NewStoryLoop(prdPath, workDir string, provider Provider) *Loop {
	l := NewLoopWithWorkDir(prdPath, workDir, "", 1, provider)
	l.buildPrompt = promptBuilderForPRD(prdPath)
	return l
}

// RunStory performs a single attempt at the PRD's next incomplete story.
//
// It mirrors Run's body minus the outer loop and minus the status write, reusing
// runIterationWithRetry so crash retry and the watchdog behave exactly as they do
// upstream. The events channel is closed on return, as Run does, so each attempt
// gets a fresh Loop — the session multiplexes those short-lived channels into its
// own long-lived stream.
//
// Returns ErrAllStoriesComplete when nothing is left to do. The caller is
// responsible for marking the story done; RunStory deliberately does not.
func (l *Loop) RunStory(ctx context.Context) (StoryRun, error) {
	if l.provider == nil {
		return StoryRun{}, fmt.Errorf("loop provider is not configured")
	}
	if l.buildPrompt == nil {
		return StoryRun{}, fmt.Errorf("story loop requires a prompt builder; use NewStoryLoop")
	}

	prdDir := filepath.Dir(l.prdPath)
	logPath := filepath.Join(prdDir, l.provider.LogFileName())
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		close(l.events)
		return StoryRun{}, fmt.Errorf("open agent log: %w", err)
	}
	l.logFile = logFile
	defer logFile.Close()
	defer close(l.events)

	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return StoryRun{}, context.Canceled
	}
	l.iteration++
	iteration := l.iteration
	l.mu.Unlock()

	// Capture the story before building the prompt: promptBuilderForPRD flips it
	// to in-progress as a side effect, and a PR body wants the title as authored.
	title := storyTitle(l.prdPath)

	prompt, storyID, err := l.buildPrompt()
	if err != nil {
		// The builder fails for exactly one expected reason — nothing left to
		// run. Anything else is a genuine failure to read the PRD and must not be
		// reported as success.
		if doc, loadErr := prd.LoadPRD(l.prdPath); loadErr == nil && doc.AllComplete() {
			return StoryRun{}, ErrAllStoriesComplete
		}
		return StoryRun{}, fmt.Errorf("build prompt: %w", err)
	}

	l.mu.Lock()
	l.prompt = prompt
	l.currentStoryID = storyID
	l.sawStoryDone = false
	l.mu.Unlock()

	l.events <- Event{Type: EventIterationStart, Iteration: iteration, StoryID: storyID}

	if err := l.runIterationWithRetry(ctx); err != nil {
		return StoryRun{StoryID: storyID, Title: title, Iteration: iteration}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return StoryRun{StoryID: storyID, Title: title, Iteration: iteration}, ctxErr
	}

	l.mu.Lock()
	saw := l.sawStoryDone
	l.sawStoryDone = false
	l.mu.Unlock()

	return StoryRun{
		StoryID:   storyID,
		Title:     title,
		DoneTag:   saw,
		Iteration: iteration,
	}, nil
}

// StopStory marks the loop stopped without touching the agent process.
//
// chief's Stop does both: it sets the flag and kills agentCmd. The kill is the
// problem. runIteration calls agentCmd.Start() without holding l.mu, and Start
// writes fields of the exec.Cmd — including Process — so Stop reading
// agentCmd.Process under l.mu is not ordered against it. The mutex protects the
// pointer, never the thing it points at, and the race detector reports exactly
// that whenever a stop lands while an attempt is starting.
//
// Killing the process is better done by cancelling the context the command was
// built with: os/exec runs its cancellation after Start has returned, which is
// the ordering this cannot get on its own. So the two halves are separated —
// this sets the flag, and the caller cancels.
func (l *Loop) StopStory() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
}

// storyTitle reads the title of the story that is about to run. Best effort: an
// unreadable PRD is reported by buildPrompt a moment later with a better message.
func storyTitle(prdPath string) string {
	doc, err := prd.LoadPRD(prdPath)
	if err != nil {
		return ""
	}
	if story := doc.NextStory(); story != nil {
		return story.Title
	}
	return ""
}

// NextStoryID reports which story a RunStory call would attempt next, without
// running anything or mutating the PRD. Returns "" when the PRD is complete.
//
// Used for planning and for preflight checks — deriving a branch name, deciding
// whether a run has anything to do — where promptBuilderForPRD's in-progress
// side effect would be wrong.
func NextStoryID(prdPath string) (id, title string) {
	doc, err := prd.LoadPRD(prdPath)
	if err != nil {
		return "", ""
	}
	story := doc.NextStory()
	if story == nil {
		return "", ""
	}
	return story.ID, story.Title
}

// IncompleteStoryCount returns how many stories still need work. It is the input
// to the attempt budget.
func IncompleteStoryCount(prdPath string) int {
	doc, err := prd.LoadPRD(prdPath)
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range doc.UserStories {
		if !s.Passes {
			n++
		}
	}
	return n
}
