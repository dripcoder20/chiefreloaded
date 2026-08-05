// Package authoring runs the interactive agent session that writes a PRD.
//
// chief does this by quitting its TUI, handing the terminal to the agent, and
// relaunching itself afterwards. That works in a terminal and is impossible in a
// window, so the session runs on a pseudo-terminal here and its output goes to
// an embedded emulator.
//
// A PTY rather than parsing the agent's structured output, even though only one
// provider is involved at a time: the authoring conversation uses the whole
// terminal surface. Lettered clarifying questions, slash commands, permission
// prompts, `/login`, Ctrl-C. Reinterpreting all of that per provider would be a
// large amount of work whose best possible outcome is behaving exactly like the
// terminal already does — and it would break the moment an agent CLI changed its
// output. The PTY gets every provider right on day one, including the user's own
// skills, because nothing is being reinterpreted.
package authoring

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/creack/pty"
	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
	"github.com/dripcoder/loop/internal/chief/prd"
	"github.com/dripcoder/loop/internal/prompts"
)

// Kind is what the session is for.
type Kind = prompts.Kind

const (
	KindNew  = prompts.KindNew
	KindEdit = prompts.KindEdit
)

// Spec describes a session to start.
type Spec struct {
	// Kind selects the prompt: creating a PRD or revising one.
	Kind Kind `json:"kind"`
	// PRD is the name, which is also its directory under .chief/prds/.
	PRD string `json:"prd"`
	// Context is what the user typed to describe what they want. Substituted
	// into the prompt; may be empty, in which case the agent is told to ask.
	Context string `json:"context,omitempty"`
	// Agent overrides the configured authoring agent for this session. Empty
	// means the configured default.
	Agent string `json:"agent,omitempty"`
	// Cols and Rows size the pseudo-terminal.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// Outcome reports what a finished session produced.
type Outcome struct {
	PRDPath string `json:"prdPath"`
	// Created is whether a prd.md exists now.
	Created bool `json:"created"`
	// Parsed is whether it could be read as a PRD. A file that exists but does
	// not parse is the common failure and needs saying explicitly, or the user
	// is left with a PRD the runner will silently refuse.
	Parsed     bool   `json:"parsed"`
	ParseError string `json:"parseError,omitempty"`
	Stories    int    `json:"stories"`
	// ExitCode is the agent's exit status.
	ExitCode int `json:"exitCode"`
}

// Session is a running agent conversation.
type Session struct {
	id      string
	prdDir  string
	prdPath string

	mu      sync.Mutex
	pty     *os.File
	cmd     *exec.Cmd
	closed  bool
	scrollb []byte

	onData func(id string, chunk []byte)
	onExit func(id string, outcome Outcome)
}

// scrollbackLimit bounds the replay buffer. Enough to redraw a reconnecting
// frontend without letting a chatty session grow without bound.
const scrollbackLimit = 256 * 1024

// Manager owns the running sessions.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	seq      int

	// OnData receives raw terminal output.
	OnData func(id string, chunk []byte)
	// OnExit fires once the agent has exited and the result has been inspected.
	OnExit func(id string, outcome Outcome)
}

// NewManager creates an empty manager.
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// Start launches an agent session for spec and returns its id.
//
// root is the project directory; the agent runs there rather than in the PRD
// directory so it can read the codebase it is writing a PRD about.
func (m *Manager) Start(root string, provider chiefloop.Provider, spec Spec) (string, error) {
	if provider == nil {
		return "", errors.New("no agent provider is configured")
	}
	if err := validPRDName(spec.PRD); err != nil {
		return "", err
	}

	prdDir := filepath.Join(root, ".chief", "prds", spec.PRD)
	prdPath := filepath.Join(prdDir, "prd.md")

	switch spec.Kind {
	case KindNew:
		if _, err := os.Stat(prdPath); err == nil {
			return "", fmt.Errorf("%s already has a PRD; edit it instead", spec.PRD)
		}
		if err := os.MkdirAll(prdDir, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", prdDir, err)
		}
	case KindEdit:
		if _, err := os.Stat(prdPath); err != nil {
			return "", fmt.Errorf("%s has no PRD to edit", spec.PRD)
		}
	default:
		return "", fmt.Errorf("unknown session kind %q", spec.Kind)
	}

	prompt, err := prompts.Resolve(root, spec.Kind, prdDir, spec.Context)
	if err != nil {
		return "", err
	}

	cmd := provider.InteractiveCommand(root, prompt)
	// A terminal the agent CLIs will render into properly. Without TERM they
	// fall back to line mode and the conversation becomes unusable.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")

	size := &pty.Winsize{Cols: uint16(orDefault(spec.Cols, 120)), Rows: uint16(orDefault(spec.Rows, 32))}
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return "", fmt.Errorf("start %s: %w", provider.Name(), err)
	}

	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("chat_%d", m.seq)
	s := &Session{
		id: id, prdDir: prdDir, prdPath: prdPath,
		pty: f, cmd: cmd,
		onData: m.OnData, onExit: m.OnExit,
	}
	m.sessions[id] = s
	m.mu.Unlock()

	go s.pump()
	return id, nil
}

// pump forwards terminal output until the agent exits.
func (s *Session) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.retain(chunk)
			if s.onData != nil {
				s.onData(s.id, chunk)
			}
		}
		if err != nil {
			// EIO is how a PTY reports that the child has gone. It is the normal
			// end of a session, not a failure.
			break
		}
	}

	_ = s.cmd.Wait()
	s.finish()
}

func (s *Session) retain(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scrollb = append(s.scrollb, chunk...)
	if over := len(s.scrollb) - scrollbackLimit; over > 0 {
		s.scrollb = append(s.scrollb[:0], s.scrollb[over:]...)
	}
}

// finish inspects what the session produced and reports it.
func (s *Session) finish() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	onExit := s.onExit
	s.mu.Unlock()

	outcome := s.outcome()

	// A `new` session that produced nothing leaves an empty directory behind.
	// chief removes it, and so should we: an empty PRD directory shows up in the
	// rail as a broken entry the user then has to work out how to get rid of.
	if !outcome.Created {
		_ = os.Remove(s.prdDir)
	}

	if onExit != nil {
		onExit(s.id, outcome)
	}
}

func (s *Session) outcome() Outcome {
	out := Outcome{PRDPath: s.prdPath}
	if s.cmd.ProcessState != nil {
		out.ExitCode = s.cmd.ProcessState.ExitCode()
	}

	if _, err := os.Stat(s.prdPath); err != nil {
		return out
	}
	out.Created = true

	doc, err := prd.LoadPRD(s.prdPath)
	if err != nil {
		out.ParseError = err.Error()
		return out
	}
	out.Parsed = true
	out.Stories = len(doc.UserStories)
	return out
}

// Write sends keystrokes to the session.
func (m *Manager) Write(id string, data []byte) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = s.pty.Write(data)
	return err
}

// Resize tells the session its terminal changed size, so the agent re-wraps.
func (m *Manager) Resize(id string, cols, rows int) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return pty.Setsize(s.pty, &pty.Winsize{
		Cols: uint16(orDefault(cols, 120)),
		Rows: uint16(orDefault(rows, 32)),
	})
}

// Interrupt sends Ctrl-C, the same as pressing it in a terminal.
func (m *Manager) Interrupt(id string) error {
	return m.Write(id, []byte{0x03})
}

// Scrollback returns the output so far, so a reconnecting frontend can redraw.
func (m *Manager) Scrollback(id string) ([]byte, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.scrollb...), nil
}

// Stop ends a session and everything it spawned.
func (m *Manager) Stop(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}

	if s.cmd.Process != nil {
		// The whole group: an agent that shelled out to an editor or a pager
		// would otherwise keep the PTY open and the session would never end.
		if err := syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL); err != nil {
			_ = s.cmd.Process.Kill()
		}
	}
	_ = s.pty.Close()

	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

// StopAll ends every session, for application shutdown.
func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		_ = m.Stop(id)
	}
}

func (m *Manager) get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("no authoring session %q", id)
	}
	return s, nil
}

// validPRDName mirrors chief's constraint. The name becomes a directory and part
// of a branch name, so anything outside this set has to be refused at the door
// rather than failing later inside git.
func validPRDName(name string) error {
	if name == "" {
		return errors.New("the PRD needs a name")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("PRD name %q: use only letters, digits, - and _", name)
		}
	}
	return nil
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
