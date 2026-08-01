package authoring

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chiefloop "github.com/dripcoder/loop/internal/chief/loop"
	"github.com/dripcoder/loop/internal/prompts"
)

// scriptProvider runs a shell script instead of a real agent. The PTY, the
// prompt resolution, the output pump and the outcome inspection are all the
// production ones; only the model is fake.
type scriptProvider struct {
	script string
	// prompt captures what the agent was handed, which is how the tests assert
	// that a custom prompt actually reached it.
	mu     sync.Mutex
	prompt string
}

func (p *scriptProvider) Name() string        { return "script" }
func (p *scriptProvider) CLIPath() string     { return "sh" }
func (p *scriptProvider) LogFileName() string { return "script.log" }
func (p *scriptProvider) CleanOutput(s string) string {
	return s
}
func (p *scriptProvider) ParseLine(string) *chiefloop.Event { return nil }
func (p *scriptProvider) LoopCommand(ctx context.Context, prompt, workDir string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", "true")
}

func (p *scriptProvider) InteractiveCommand(workDir, prompt string) *exec.Cmd {
	p.mu.Lock()
	p.prompt = prompt
	p.mu.Unlock()

	cmd := exec.Command("sh", "-c", p.script)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "LOOP_PROMPT="+prompt)
	return cmd
}

func (p *scriptProvider) seenPrompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prompt
}

const samplePRD = `# Written By The Agent

## User Stories

### US-001: A story
**Status:** todo
**Priority:** 1
- [ ] It works
`

func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".chief", "prds"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// collector gathers a session's output and its outcome.
type collector struct {
	mu   sync.Mutex
	out  []byte
	done chan Outcome
}

func newCollector() *collector { return &collector{done: make(chan Outcome, 1)} }

func (c *collector) attach(m *Manager) {
	m.OnData = func(_ string, chunk []byte) {
		c.mu.Lock()
		c.out = append(c.out, chunk...)
		c.mu.Unlock()
	}
	m.OnExit = func(_ string, o Outcome) { c.done <- o }
}

func (c *collector) wait(t *testing.T) Outcome {
	t.Helper()
	select {
	case o := <-c.done:
		return o
	case <-time.After(30 * time.Second):
		t.Fatal("the session never finished")
		return Outcome{}
	}
}

func (c *collector) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.out)
}

func TestSessionReportsAWrittenPRD(t *testing.T) {
	root := newProject(t)

	// An "agent" that writes the PRD and exits, like a real one would.
	p := &scriptProvider{script: `
printf 'writing the PRD\n'
mkdir -p "$1"
cat > .chief/prds/checkout/prd.md <<'PRD'
` + samplePRD + `
PRD
`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"}); err != nil {
		t.Fatal(err)
	}

	o := c.wait(t)
	if !o.Created {
		t.Fatal("the PRD the agent wrote was not detected")
	}
	if !o.Parsed {
		t.Errorf("the PRD did not parse: %s", o.ParseError)
	}
	if o.Stories != 1 {
		t.Errorf("stories = %d, want 1", o.Stories)
	}
	if !strings.Contains(c.text(), "writing the PRD") {
		t.Errorf("terminal output was not forwarded; got %q", c.text())
	}
}

// A file that exists but does not parse is the common failure. Saying so is the
// difference between a fixable problem and a PRD the runner silently refuses.
func TestSessionReportsAnUnparseablePRD(t *testing.T) {
	root := newProject(t)
	p := &scriptProvider{script: `
mkdir -p .chief/prds/checkout
printf 'this is not a PRD\n' > .chief/prds/checkout/prd.md
`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"}); err != nil {
		t.Fatal(err)
	}

	o := c.wait(t)
	if !o.Created {
		t.Fatal("the file should be reported as created")
	}
	if o.Stories != 0 {
		t.Errorf("stories = %d, want 0 for a PRD with none", o.Stories)
	}
}

// An abandoned session must not leave an empty PRD directory behind, or the rail
// shows a broken entry the user then has to work out how to remove.
func TestAbandonedSessionCleansUpItsDirectory(t *testing.T) {
	root := newProject(t)
	p := &scriptProvider{script: `printf 'changed my mind\n'`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "abandoned"}); err != nil {
		t.Fatal(err)
	}

	if o := c.wait(t); o.Created {
		t.Fatal("nothing was written, so Created should be false")
	}
	if _, err := os.Stat(filepath.Join(root, ".chief", "prds", "abandoned")); !os.IsNotExist(err) {
		t.Error("the empty PRD directory should have been removed")
	}
}

// The whole point of the feature: what the user wrote in Settings is what the
// agent is handed.
func TestACustomPromptReachesTheAgent(t *testing.T) {
	root := newProject(t)
	custom := "Write to {{PRD_DIR}}/prd.md, then run /grilling and /to-issues."
	if err := prompts.Save(root, prompts.KindNew, custom); err != nil {
		t.Fatal(err)
	}

	p := &scriptProvider{script: `printf 'ok\n'`}
	m := NewManager()
	c := newCollector()
	c.attach(m)

	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"}); err != nil {
		t.Fatal(err)
	}
	c.wait(t)

	got := p.seenPrompt()
	if !strings.Contains(got, "/grilling") || !strings.Contains(got, "/to-issues") {
		t.Errorf("the custom prompt did not reach the agent; got %q", got)
	}
	if strings.Contains(got, prompts.VarPRDDir) {
		t.Error("an unsubstituted placeholder reached the agent")
	}
	if !strings.Contains(got, filepath.Join(".chief", "prds", "checkout")) {
		t.Errorf("the PRD directory was not substituted; got %q", got)
	}
}

func TestBuiltinPromptIsUsedWhenThereIsNoOverride(t *testing.T) {
	root := newProject(t)
	p := &scriptProvider{script: `printf 'ok\n'`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"}); err != nil {
		t.Fatal(err)
	}
	c.wait(t)

	if !strings.Contains(p.seenPrompt(), "PRD") {
		t.Errorf("chief's own prompt should have been used; got %q", p.seenPrompt())
	}
}

func TestWriteReachesTheAgent(t *testing.T) {
	root := newProject(t)
	// Echo one line of input back, which only works over a real PTY.
	p := &scriptProvider{script: `read -r line; printf 'you said: %s\n' "$line"`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	id, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := m.Write(id, []byte("1A 2B\r")); err != nil {
		t.Fatal(err)
	}

	c.wait(t)
	if !strings.Contains(c.text(), "you said: 1A 2B") {
		t.Errorf("input did not reach the agent; output was %q", c.text())
	}
}

func TestStopEndsAHangingSession(t *testing.T) {
	root := newProject(t)
	p := &scriptProvider{script: `printf 'thinking\n'; sleep 120`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	id, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	if err := m.Stop(id); err != nil {
		t.Fatal(err)
	}
	c.wait(t) // must finish promptly, not in two minutes
}

func TestScrollbackLetsAReconnectingViewRedraw(t *testing.T) {
	root := newProject(t)
	p := &scriptProvider{script: `printf 'line one\nline two\n'; sleep 5`}

	m := NewManager()
	c := newCollector()
	c.attach(m)

	id, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(id) }()
	time.Sleep(400 * time.Millisecond)

	back, err := m.Scrollback(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "line one") {
		t.Errorf("scrollback did not retain earlier output: %q", back)
	}
}

func TestStartRejectsBadInput(t *testing.T) {
	root := newProject(t)
	p := &scriptProvider{script: `true`}
	m := NewManager()

	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "has spaces"}); err == nil {
		t.Error("a PRD name with spaces should be rejected")
	}
	if _, err := m.Start(root, p, Spec{Kind: KindNew, PRD: "../escape"}); err == nil {
		t.Error("a PRD name that escapes the directory should be rejected")
	}
	if _, err := m.Start(root, nil, Spec{Kind: KindNew, PRD: "ok"}); err == nil {
		t.Error("starting with no provider should be rejected")
	}
	if _, err := m.Start(root, p, Spec{Kind: KindEdit, PRD: "missing"}); err == nil {
		t.Error("editing a PRD that does not exist should be rejected")
	}
}

// Overwriting an existing PRD by accident would destroy work the user cannot
// recover, since prd.md is not necessarily committed.
func TestNewRefusesToOverwriteAnExistingPRD(t *testing.T) {
	root := newProject(t)
	dir := filepath.Join(root, ".chief", "prds", "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prd.md"), []byte(samplePRD), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	if _, err := m.Start(root, &scriptProvider{script: "true"}, Spec{Kind: KindNew, PRD: "checkout"}); err == nil {
		t.Error("creating over an existing PRD should be refused")
	}
}
