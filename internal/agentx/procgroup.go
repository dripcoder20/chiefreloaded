// Package agentx decorates chief's agent providers with behaviour Loop needs.
package agentx

import (
	"context"
	"os/exec"
	"sync"
	"syscall"

	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
)

// GroupLeader wraps a provider so each agent invocation runs in its own process
// group, and exposes a Kill that takes the whole group down.
//
// Without this, stopping a run can hang indefinitely. Killing the agent kills
// only the agent: any process it spawned — a dev server from a Bash tool call, a
// test runner, a `sleep` in a shell script — inherits the same stdout pipe and
// keeps it open. chief's runIteration waits for its output scanners to see EOF
// before cmd.Wait() returns, and EOF never comes while a grandchild holds the
// write end. The user presses Stop and the application sits there.
//
// Putting the agent in its own process group and signalling the negative PID
// reaches every descendant, so the pipe actually closes.
//
// This is a decorator rather than a change to the vendored code because the
// vendored code is regenerated on every upstream sync; behaviour we add belongs
// outside it wherever that is possible.
type GroupLeader struct {
	chiefloop.Provider

	mu      sync.Mutex
	current *exec.Cmd
}

// NewGroupLeader wraps p.
func NewGroupLeader(p chiefloop.Provider) *GroupLeader {
	return &GroupLeader{Provider: p}
}

// LoopCommand returns the wrapped provider's command, configured to lead its own
// process group.
func (g *GroupLeader) LoopCommand(ctx context.Context, prompt, workDir string) *exec.Cmd {
	cmd := g.Provider.LoopCommand(ctx, prompt, workDir)
	if cmd == nil {
		return nil
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	// The kill runs through os/exec's own cancellation rather than from whoever
	// happens to call Stop.
	//
	// Every provider builds its command with exec.CommandContext, and os/exec
	// invokes Cancel from the goroutine it starts *after* Start returns — so
	// reading cmd.Process here is ordered against Start's write to it. Reaching
	// into the command from another goroutine is not: chief's runIteration calls
	// Start without holding its own mutex, so a concurrent reader racing that
	// write is a real data race, and one the race detector catches.
	cmd.Cancel = func() error { return killGroup(cmd) }

	g.mu.Lock()
	g.current = cmd
	g.mu.Unlock()
	return cmd
}

// killGroup signals the whole process group, falling back to the single process
// when the group has already gone.
//
// The group id equals the leader's pid because of Setpgid above. Callers must
// already be ordered against cmd.Start.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

// UsageFormat forwards the wrapped provider's output-format declaration.
//
// The embedded field is the Provider interface, so Go promotes only the methods
// that interface declares — UsageFormat is not one of them, and without this a
// wrapped provider would silently report no usage at all.
func (g *GroupLeader) UsageFormat() string {
	if f, ok := g.Provider.(chiefloop.UsageFormatter); ok {
		return f.UsageFormat()
	}
	return g.Provider.Name()
}

// Kill terminates the current agent and everything it spawned.
//
// Prefer cancelling the run's context, which reaches the same kill through
// cmd.Cancel and is ordered against Start. This exists for the case where there
// is no context to cancel — application shutdown — and accepts that it reads
// the command from outside os/exec's synchronisation.
//
// Safe to call when nothing is running, and safe to call more than once: a
// finished process yields ESRCH, which is the outcome we wanted anyway.
func (g *GroupLeader) Kill() {
	g.mu.Lock()
	cmd := g.current
	g.mu.Unlock()

	if cmd == nil {
		return
	}
	_ = killGroup(cmd)
}
