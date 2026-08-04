//go:build e2e

// Package e2e drives the whole system through cmd/loopctl.
//
// The point is coverage the unit tests structurally cannot give: a real binary,
// a real git repository, a real agent subprocess, and the full run lifecycle
// from start to a completed PRD — including the crash-and-resume path, which
// only exists once processes are involved.
//
// It costs nothing to run. The agent is scripted, so there is no network, no
// API spend, and no non-determinism.
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var loopctl string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "loopctl-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	loopctl = filepath.Join(dir, "loopctl")
	build := exec.Command("go", "build", "-o", loopctl, "../cmd/loopctl")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building loopctl: %v\n%s", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

const prdBody = `# Demo Project

## Overview
A project with three small stories.

## User Stories

### US-001: First story
**Status:** todo
**Priority:** 1
**Description:** Create the first file.
- [ ] It exists

### US-002: Second story
**Status:** todo
**Priority:** 2
**Description:** Create the second file.
- [ ] It also exists

### US-003: Third story
**Status:** todo
**Priority:** 3
**Description:** Create the third file.
- [ ] And the third
`

// fakeAgent writes a shell script that behaves like an agent CLI: it emits
// stream-json on stdout, writes a file, commits it, and signals completion.
//
// Pointing the real provider resolution at this — rather than injecting a Go
// fake — means the subprocess handling, the scanner and the parser are all the
// production ones.
func fakeAgent(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "fake-claude")
	script := `#!/bin/sh
# The story JSON is inlined in the prompt by the prompt builder; the last
# argument is that prompt for the claude provider.
prompt="$*"
id=$(printf '%s' "$prompt" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
title=$(printf '%s' "$prompt" | sed -n 's/.*"title"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)

printf '{"type":"system","subtype":"init"}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"Working on %s."}]}}\n' "$id"

printf 'work for %s\n' "$id" > "file-$id.txt"
git add -A >/dev/null 2>&1
git commit -q -m "feat: $id - $title" >/dev/null 2>&1

printf '{"type":"assistant","message":{"content":[{"type":"text","text":"Done. <chief-done/>"}]}}\n'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "e2e@example.com"},
		{"config", "user.name", "E2E"},
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
	if err := os.WriteFile(filepath.Join(dir, "prd.md"), []byte(prdBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// runCtl invokes loopctl with the fake agent wired in through the same
// environment variables a user would use.
func runCtl(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(loopctl, append([]string{"-C", root}, args...)...)
	cmd.Env = os.Environ()
	// Let a test pin its own scripted agent (e.g. the usage tests); otherwise
	// wire in the default fake agent the same way a user would.
	if os.Getenv("CHIEF_AGENT_PATH") == "" {
		cmd.Env = append(cmd.Env,
			"CHIEF_AGENT=claude",
			"CHIEF_AGENT_PATH="+fakeAgent(t, t.TempDir()),
		)
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatalf("loopctl %v timed out\n%s", args, out.String())
		return "", nil
	}
}

func gitLog(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRunCompletesEveryStory(t *testing.T) {
	root := newProject(t)

	out, err := runCtl(t, root, "run", "main")
	if err != nil {
		t.Fatalf("loopctl run: %v\n%s", err, out)
	}

	for _, want := range []string{"US-001", "US-002", "US-003", "all stories complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// One commit per story, which is chief's central promise.
	log := gitLog(t, root)
	for _, id := range []string{"US-001", "US-002", "US-003"} {
		if !strings.Contains(log, id) {
			t.Errorf("no commit for %s:\n%s", id, log)
		}
		if _, err := os.Stat(filepath.Join(root, "file-"+id+".txt")); err != nil {
			t.Errorf("the agent's file for %s is missing: %v", id, err)
		}
	}
}

func TestListAndShowReflectTheRun(t *testing.T) {
	root := newProject(t)

	before, err := runCtl(t, root, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, before)
	}
	if !strings.Contains(before, "0/3") {
		t.Errorf("expected 0/3 before the run:\n%s", before)
	}

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	after, err := runCtl(t, root, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, after)
	}
	if !strings.Contains(after, "3/3") {
		t.Errorf("expected 3/3 after the run:\n%s", after)
	}
}

// Re-running a finished PRD must be a no-op rather than redoing the work. This
// is also the regression test for chief's whole-history commit grep, which would
// otherwise match the previous run's commits.
func TestRerunningACompletePRDDoesNothing(t *testing.T) {
	root := newProject(t)

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	first := gitLog(t, root)

	if out, err := runCtl(t, root, "run", "main"); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if second := gitLog(t, root); second != first {
		t.Errorf("a second run changed history:\nbefore:\n%s\nafter:\n%s", first, second)
	}
}

// A run killed mid-flight must not leave a story wedged as in-progress. chief
// does exactly that, and never clears it on startup either, so the story is
// stuck until someone edits the markdown by hand.
func TestInterruptedRunLeavesNoWedgedStory(t *testing.T) {
	root := newProject(t)

	// An agent that hangs, so the kill lands mid-story.
	slow := filepath.Join(t.TempDir(), "slow-agent")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nprintf '{\"type\":\"system\",\"subtype\":\"init\"}\\n'\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(loopctl, "-C", root, "run", "main")
	cmd.Env = append(os.Environ(), "CHIEF_AGENT=claude", "CHIEF_AGENT_PATH="+slow)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(3 * time.Second)
	_ = cmd.Process.Signal(os.Interrupt)

	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
	}

	body, err := os.ReadFile(filepath.Join(root, ".chief", "prds", "main", "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "**Status:** in-progress") {
		t.Errorf("a story was left in-progress after the run was interrupted:\n%s", body)
	}
}

func TestDoctorReportsTheEnvironment(t *testing.T) {
	root := newProject(t)

	out, err := runCtl(t, root, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	for _, want := range []string{"project", "git repo", "chief engine", "stacked PRs"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}
