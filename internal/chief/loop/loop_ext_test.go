package loop_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dripcoder/loop/internal/chief/loop"
	"github.com/dripcoder/loop/internal/chief/prd"
	"github.com/dripcoder/loop/internal/fakeagent"
)

// These run the real subprocess, scanner, parser and git. The only thing faked
// is the model.

const twoStoryPRD = `# Demo

## User Stories

### US-001: First story
**Status:** todo
**Priority:** 1
**Description:** Do the first thing.
- [ ] It works

### US-002: Second story
**Status:** todo
**Priority:** 2
**Description:** Do the second thing.
- [ ] It also works
`

func setupRepo(t *testing.T, prdBody string) (root, prdPath string) {
	t.Helper()
	root = t.TempDir()

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	dir := filepath.Join(root, ".chief", "prds", "main")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	prdPath = filepath.Join(dir, "prd.md")
	if err := os.WriteFile(prdPath, []byte(prdBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, prdPath
}

// drainEvents consumes the loop's channel so the producer never blocks, and
// returns everything it saw once the channel closes.
func drainEvents(l *loop.Loop) <-chan []loop.Event {
	done := make(chan []loop.Event, 1)
	go func() {
		var evs []loop.Event
		for ev := range l.Events() {
			evs = append(evs, ev)
		}
		done <- evs
	}()
	return done
}

func TestRunStoryRunsOneStoryAndReportsTheDoneTag(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)

	p := fakeagent.New(fakeagent.Behaviour{
		Text:      "Implementing.",
		WriteFile: "first.txt",
		FileBody:  "one",
		Commit:    true,
		Done:      true,
	})

	l := loop.NewStoryLoop(prdPath, root, p)
	events := drainEvents(l)

	got, err := l.RunStory(context.Background())
	if err != nil {
		t.Fatalf("RunStory: %v", err)
	}
	<-events

	if got.StoryID != "US-001" {
		t.Errorf("StoryID = %q, want US-001", got.StoryID)
	}
	if got.Title != "First story" {
		t.Errorf("Title = %q, want %q — the title must be captured before the status write", got.Title, "First story")
	}
	if !got.DoneTag {
		t.Error("DoneTag should be true when the agent emits the sentinel")
	}
}

// The status write is the caller's job. RunStory returning before it is what
// lets the session verify the commit first — chief writes it immediately, and
// that write also ticks every acceptance-criteria box.
func TestRunStoryDoesNotMarkTheStoryDone(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)

	p := fakeagent.New(fakeagent.Behaviour{Commit: true, WriteFile: "a.txt", FileBody: "a", Done: true})
	l := loop.NewStoryLoop(prdPath, root, p)
	events := drainEvents(l)

	if _, err := l.RunStory(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-events

	doc, err := prd.LoadPRD(prdPath)
	if err != nil {
		t.Fatal(err)
	}
	if doc.UserStories[0].Passes {
		t.Error("US-001 was marked done by RunStory; only the caller may do that")
	}
	if !doc.UserStories[0].InProgress {
		t.Error("US-001 should be left in-progress by the prompt builder")
	}
}

// Successive runs advance only once the caller marks a story done, which is what
// makes the session's verify-then-commit ordering possible.
func TestRunStoryAdvancesOnlyAfterTheCallerMarksDone(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)
	ctx := context.Background()

	first := loop.NewStoryLoop(prdPath, root, fakeagent.New(fakeagent.Behaviour{Done: true}))
	ev1 := drainEvents(first)
	r1, err := first.RunStory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-ev1

	// Without a status write, the same story comes up again.
	repeat := loop.NewStoryLoop(prdPath, root, fakeagent.New(fakeagent.Behaviour{Done: true}))
	ev2 := drainEvents(repeat)
	r2, err := repeat.RunStory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-ev2
	if r2.StoryID != r1.StoryID {
		t.Fatalf("second run picked %s; without a status write it should repeat %s", r2.StoryID, r1.StoryID)
	}

	if err := prd.SetStoryStatus(prdPath, r1.StoryID, "done"); err != nil {
		t.Fatal(err)
	}

	third := loop.NewStoryLoop(prdPath, root, fakeagent.New(fakeagent.Behaviour{Done: true}))
	ev3 := drainEvents(third)
	r3, err := third.RunStory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-ev3
	if r3.StoryID != "US-002" {
		t.Errorf("third run picked %s, want US-002", r3.StoryID)
	}
}

func TestRunStoryReportsAllComplete(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)
	for _, id := range []string{"US-001", "US-002"} {
		if err := prd.SetStoryStatus(prdPath, id, "done"); err != nil {
			t.Fatal(err)
		}
	}

	l := loop.NewStoryLoop(prdPath, root, fakeagent.New())
	events := drainEvents(l)

	_, err := l.RunStory(context.Background())
	<-events
	if err != loop.ErrAllStoriesComplete {
		t.Fatalf("err = %v, want ErrAllStoriesComplete", err)
	}
}

// A PRD that cannot be read must not be reported as finished — that would look
// like success and silently skip the work.
func TestRunStoryDistinguishesUnreadablePRDFromCompletion(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)
	if err := os.Remove(prdPath); err != nil {
		t.Fatal(err)
	}

	l := loop.NewStoryLoop(prdPath, root, fakeagent.New())
	events := drainEvents(l)

	_, err := l.RunStory(context.Background())
	<-events
	if err == nil {
		t.Fatal("expected an error for a missing PRD")
	}
	if err == loop.ErrAllStoriesComplete {
		t.Fatal("a missing PRD was reported as all-stories-complete")
	}
}

// A crash must surface as an error, not a silent success, so the caller can
// spend an attempt and retry the same story.
func TestRunStoryReportsAgentCrash(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)

	p := fakeagent.New(fakeagent.Behaviour{Text: "starting", ExitCode: 3})
	l := loop.NewStoryLoop(prdPath, root, p)
	l.DisableRetry() // exercise the crash path itself, not the retry loop
	events := drainEvents(l)

	got, err := l.RunStory(context.Background())
	<-events

	if err == nil {
		t.Fatal("a non-zero agent exit must be reported as an error")
	}
	if got.StoryID != "US-001" {
		t.Errorf("StoryID = %q; a failed attempt should still say which story it was", got.StoryID)
	}
	if got.DoneTag {
		t.Error("DoneTag must be false when the agent crashed")
	}
}

func TestRunStoryRetriesOnCrash(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)

	// Crash once, then succeed. Upstream's retry logic should absorb the first.
	p := fakeagent.New(
		fakeagent.Behaviour{Text: "boom", ExitCode: 1},
		fakeagent.Behaviour{Text: "ok", Done: true},
	)
	l := loop.NewStoryLoop(prdPath, root, p)
	l.SetRetryConfig(loop.RetryConfig{MaxRetries: 2, RetryDelays: []time.Duration{0, 0}, Enabled: true})
	events := drainEvents(l)

	got, err := l.RunStory(context.Background())
	evs := <-events

	if err != nil {
		t.Fatalf("retry should have absorbed the crash: %v", err)
	}
	if !got.DoneTag {
		t.Error("second attempt emitted the sentinel; DoneTag should be true")
	}
	if p.Attempts() != 2 {
		t.Errorf("agent invoked %d times, want 2", p.Attempts())
	}
	if !hasType(evs, loop.EventRetrying) {
		t.Error("a retry event should have been emitted")
	}
}

func TestRunStoryRunsInTheGivenWorkDir(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)
	workDir := filepath.Join(root, "sub")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := fakeagent.New(fakeagent.Behaviour{Text: "hi"})
	l := loop.NewStoryLoop(prdPath, workDir, p)
	events := drainEvents(l)
	if _, err := l.RunStory(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-events

	if len(p.Calls) != 1 || p.Calls[0] != workDir {
		t.Errorf("agent ran in %v, want [%s] — per-story mode depends on this being the worktree", p.Calls, workDir)
	}
}

// The agent's file writes and commits must land, so commit verification has
// something real to inspect.
func TestRunStoryAgentCommitIsReal(t *testing.T) {
	root, prdPath := setupRepo(t, twoStoryPRD)

	p := fakeagent.New(fakeagent.Behaviour{
		WriteFile: "src/thing.go", FileBody: "package thing\n", Commit: true, Done: true,
	})
	l := loop.NewStoryLoop(prdPath, root, p)
	events := drainEvents(l)
	if _, err := l.RunStory(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-events

	out, err := exec.Command("git", "-C", root, "log", "--oneline", "-1").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "US-001") {
		t.Errorf("last commit = %q, want it to mention US-001", strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(filepath.Join(root, "src", "thing.go")); err != nil {
		t.Errorf("agent's file was not written: %v", err)
	}
}

func TestNextStoryIDDoesNotMutateThePRD(t *testing.T) {
	_, prdPath := setupRepo(t, twoStoryPRD)

	id, title := loop.NextStoryID(prdPath)
	if id != "US-001" || title != "First story" {
		t.Errorf("NextStoryID = %q/%q, want US-001/First story", id, title)
	}

	// promptBuilderForPRD flips the story to in-progress; this must not.
	doc, err := prd.LoadPRD(prdPath)
	if err != nil {
		t.Fatal(err)
	}
	if doc.UserStories[0].InProgress {
		t.Error("NextStoryID marked the story in-progress; it must be side-effect free")
	}
}

func TestIncompleteStoryCount(t *testing.T) {
	_, prdPath := setupRepo(t, twoStoryPRD)

	if got := loop.IncompleteStoryCount(prdPath); got != 2 {
		t.Errorf("count = %d, want 2", got)
	}
	if err := prd.SetStoryStatus(prdPath, "US-001", "done"); err != nil {
		t.Fatal(err)
	}
	if got := loop.IncompleteStoryCount(prdPath); got != 1 {
		t.Errorf("count after one done = %d, want 1", got)
	}
}

func hasType(evs []loop.Event, t loop.EventType) bool {
	for _, ev := range evs {
		if ev.Type == t {
			return true
		}
	}
	return false
}
