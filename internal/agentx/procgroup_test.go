package agentx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
)

// stubProvider builds a command the test controls, so these exercise the
// decorator rather than a real agent CLI.
type stubProvider struct {
	chiefloop.Provider
	args []string
}

func (p *stubProvider) Name() string { return "stub" }

func (p *stubProvider) LoopCommand(ctx context.Context, _, workDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, p.args[0], p.args[1:]...)
	cmd.Dir = workDir
	return cmd
}

// The whole reason this decorator exists: a grandchild holding the output pipe
// keeps cmd.Wait blocked, so the kill has to reach the process group.
func TestLoopCommandLeadsItsOwnProcessGroup(t *testing.T) {
	g := NewGroupLeader(&stubProvider{args: []string{"sh", "-c", "sleep 30"}})

	cmd := g.LoopCommand(t.Context(), "", t.TempDir())

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("the agent must lead its own process group, or a kill cannot reach what it spawned")
	}
}

/*
Cancelling the context must actually kill the process, through the group.

This is the path Stop now takes. os/exec calls Cancel from the goroutine it
starts after Start returns, which is what orders the kill against the process
starting — reaching into the command from the stopping goroutine instead is a
data race, and was one.
*/
func TestCancellingTheContextKillsTheGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// The grandchild is the point of the test, so wait until it exists before
	// cancelling. Start returns before the shell has forked anything, and
	// cancelling in that window kills a group of one — which passes whether or
	// not the kill reaches the group at all.
	dir := t.TempDir()
	ready := filepath.Join(dir, "spawned")
	g := NewGroupLeader(&stubProvider{
		args: []string{"sh", "-c", "sleep 30 & echo up > " + ready + "; wait"},
	})

	cmd := g.LoopCommand(ctx, "", dir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pgid := cmd.Process.Pid
	waitForFile(t, ready)

	cancel()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the process outlived its cancelled context")
	}

	// ESRCH means the group is gone, which is the outcome. Anything else means
	// something is still alive in it.
	if err := syscall.Kill(-pgid, 0); err != syscall.ESRCH {
		t.Errorf("process group still present after cancellation: %v", err)
	}
}

// waitForFile blocks until path exists, so a test can synchronise on a
// subprocess having got somewhere rather than on a sleep.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// Kill is the no-context path, for application shutdown. It must tolerate being
// called when nothing has run and when the process is already gone.
func TestKillIsSafeWithNothingRunning(t *testing.T) {
	g := NewGroupLeader(&stubProvider{args: []string{"true"}})
	g.Kill() // never built a command

	cmd := g.LoopCommand(t.Context(), "", t.TempDir())
	g.Kill() // built, never started

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = cmd.Wait()
	g.Kill() // already exited
}
