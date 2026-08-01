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

	g.mu.Lock()
	g.current = cmd
	g.mu.Unlock()
	return cmd
}

// Kill terminates the current agent and everything it spawned.
//
// Safe to call when nothing is running, and safe to call more than once: a
// finished process yields ESRCH, which is the outcome we wanted anyway.
func (g *GroupLeader) Kill() {
	g.mu.Lock()
	cmd := g.current
	g.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	// The process group id equals the leader's pid because of Setpgid above.
	// Negating it signals the group. Fall back to the single process if the
	// group has already gone.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
