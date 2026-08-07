package agentx

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

	// The grandchild inherits this pipe, exactly as an agent's spawn inherits
	// the agent's stdout. EOF on the read end therefore means every process
	// holding the write end has gone — which is the property the decorator
	// exists to provide, and precisely what killing only the leader fails to
	// achieve.
	//
	// Asserted this way rather than by signalling the group, which cannot be
	// done reliably after the fact: cmd.Wait reaps only the leader, so a
	// grandchild reparented to init is briefly an unreaped zombie still in the
	// group, and once the group has gone its id is free to be reused by a
	// group we do not own. Both were seen — as a spurious pass on Linux and
	// EPERM on macOS — from the same single check against a group id that is
	// only meaningful for an instant.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer reader.Close()
	cmd.Stdout = writer

	if err := cmd.Start(); err != nil {
		writer.Close()
		t.Fatalf("start: %v", err)
	}
	// Drop our own copy, so the children are the only holders left.
	writer.Close()
	waitForFile(t, ready)

	cancel()

	drained := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, reader)
		drained <- err
	}()
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("read the agent's output: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("something in the group outlived the cancelled context, still holding the pipe open")
	}

	// Reaps the leader. It cannot block on the pipe — cmd.Stdout is an *os.File,
	// so os/exec hands the descriptor over rather than copying it on a goroutine.
	if err := cmd.Wait(); err == nil {
		t.Error("the agent exited cleanly; it was supposed to be killed")
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
