// Package fakeagent provides a scripted loop.Provider for tests.
//
// Running the real thing against Claude costs money, needs network, and is
// non-deterministic — none of which belongs in a test suite that should run on
// every commit. This provider spawns a real subprocess that emits real
// stream-json on stdout and makes real git commits, so everything downstream of
// the agent is exercised for real: the scanner, the parser, the watchdog, the
// retry path, commit verification, and branch switching.
//
// What is faked is only the model. Everything else is the production path.
package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dripcoder/loop/internal/chief/loop"
)

// Behaviour describes what the fake agent does for one attempt.
type Behaviour struct {
	// Text is emitted as assistant output before anything else.
	Text string
	// Tools are emitted as tool_use blocks, purely so log rendering has
	// something realistic to chew on.
	Tools []ToolCall
	// WriteFile, when set, creates or overwrites this path (relative to the work
	// directory) with FileBody before committing.
	WriteFile string
	FileBody  string
	// Commit makes a git commit. CommitSubject overrides the conventional
	// "feat: <ID> - <title>" subject, which is how the wrong-subject verification
	// path gets tested.
	Commit        bool
	CommitSubject string
	// Done emits the completion sentinel, i.e. the agent claims the story is
	// finished. Independent of Commit on purpose: "said done but committed
	// nothing" is a case the run engine has to handle.
	Done bool
	// ExitCode non-zero simulates a crash, which should drive the retry path.
	ExitCode int
	// Silence holds the process open emitting nothing, to trip the watchdog.
	Silence time.Duration
	// CreateBranch has the agent switch branches behind the orchestrator's back.
	// The prompt forbids it; detecting it anyway is the point.
	CreateBranch string
}

// ToolCall is a fake tool invocation.
type ToolCall struct {
	Name  string
	Input map[string]any
	// Result, when non-empty, is emitted as the tool result.
	Result string
}

// Provider is a scripted loop.Provider. Behaviours are consumed in order, one
// per attempt; once exhausted the last one repeats, so a test that only cares
// about the steady state can supply a single entry.
type Provider struct {
	mu         sync.Mutex
	behaviours []Behaviour
	attempt    int
	// Calls records every work directory the provider was invoked in, which is
	// how tests assert that the agent ran inside the worktree rather than the
	// project root.
	Calls []string
}

// New builds a Provider that plays the given behaviours in order.
func New(behaviours ...Behaviour) *Provider {
	return &Provider{behaviours: behaviours}
}

var _ loop.Provider = (*Provider)(nil)

func (p *Provider) Name() string        { return "fake" }
func (p *Provider) CLIPath() string     { return "sh" }
func (p *Provider) LogFileName() string { return "fake.log" }

// CleanOutput mirrors the real providers: reduce a full NDJSON dump to the last
// assistant text.
func (p *Provider) CleanOutput(output string) string {
	var last string
	for _, line := range strings.Split(output, "\n") {
		if ev := p.ParseLine(line); ev != nil && ev.Type == loop.EventAssistantText {
			last = ev.Text
		}
	}
	if last == "" {
		return output
	}
	return last
}

// ParseLine reuses chief's Claude parser: the fake emits the same wire format,
// so the production parser is what gets exercised.
func (p *Provider) ParseLine(line string) *loop.Event { return loop.ParseLine(line) }

// InteractiveCommand is the PRD-authoring path. The fake just echoes, which is
// enough to prove the plumbing without a terminal.
func (p *Provider) InteractiveCommand(workDir, prompt string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", "cat >/dev/null")
	cmd.Dir = workDir
	return cmd
}

// Attempts reports how many times LoopCommand has been called.
func (p *Provider) Attempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempt
}

// LoopCommand builds the subprocess for one attempt.
func (p *Provider) LoopCommand(ctx context.Context, prompt, workDir string) *exec.Cmd {
	p.mu.Lock()
	b := Behaviour{}
	if len(p.behaviours) > 0 {
		i := p.attempt
		if i >= len(p.behaviours) {
			i = len(p.behaviours) - 1
		}
		b = p.behaviours[i]
	}
	p.attempt++
	p.Calls = append(p.Calls, workDir)
	p.mu.Unlock()

	// The story ID and title are inlined in the prompt as JSON by
	// promptBuilderForPRD, so the fake can recover them and produce a
	// conventionally-subjected commit without the test having to thread them in.
	storyID, title := storyFromPrompt(prompt)

	cmd := exec.CommandContext(ctx, "sh", "-c", script(b, storyID, title))
	cmd.Dir = workDir
	return cmd
}

// script renders a behaviour into shell.
func script(b Behaviour, storyID, title string) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")

	emit := func(v any) {
		raw, _ := json.Marshal(v)
		// Single-quoted with the shell's '"'"' escape, so arbitrary JSON survives.
		sb.WriteString("printf '%s\\n' " + shellQuote(string(raw)) + "\n")
	}

	emit(map[string]any{"type": "system", "subtype": "init"})

	if b.Silence > 0 {
		fmt.Fprintf(&sb, "sleep %.3f\n", b.Silence.Seconds())
	}
	if b.Text != "" {
		emit(assistantText(b.Text))
	}
	for _, t := range b.Tools {
		emit(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []map[string]any{{
					"type": "tool_use", "id": "tu_" + t.Name, "name": t.Name, "input": t.Input,
				}},
			},
		})
		if t.Result != "" {
			emit(map[string]any{
				"type": "user",
				"message": map[string]any{
					"content": []map[string]any{{
						"type": "tool_result", "tool_use_id": "tu_" + t.Name, "content": t.Result,
					}},
				},
			})
		}
	}

	if b.CreateBranch != "" {
		fmt.Fprintf(&sb, "git checkout -q -b %s\n", shellQuote(b.CreateBranch))
	}
	if b.WriteFile != "" {
		fmt.Fprintf(&sb, "mkdir -p \"$(dirname %s)\"\n", shellQuote(b.WriteFile))
		fmt.Fprintf(&sb, "printf '%%s' %s > %s\n", shellQuote(b.FileBody), shellQuote(b.WriteFile))
	}
	if b.Commit {
		subject := b.CommitSubject
		if subject == "" {
			subject = fmt.Sprintf("feat: %s - %s", storyID, title)
		}
		sb.WriteString("git add -A\n")
		fmt.Fprintf(&sb, "git commit -q -m %s\n", shellQuote(subject))
	}

	if b.Done {
		// The sentinel is matched inside assistant text, exactly as the real
		// providers emit it.
		emit(assistantText("Story complete. <chief-done/>"))
	}
	if b.ExitCode != 0 {
		fmt.Fprintf(&sb, "exit %d\n", b.ExitCode)
	}
	return sb.String()
}

func assistantText(text string) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	}
}

// storyFromPrompt recovers the story ID and title from the prompt. The builder
// inlines the story as indented JSON, so a narrow scan is enough and is more
// robust than trying to keep a copy of the prompt template in sync.
func storyFromPrompt(prompt string) (id, title string) {
	id = jsonStringField(prompt, `"id":`)
	title = jsonStringField(prompt, `"title":`)
	return id, title
}

func jsonStringField(s, key string) string {
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	open := strings.Index(rest, `"`)
	if open < 0 {
		return ""
	}
	rest = rest[open+1:]
	// Unescaped closing quote.
	for j := 0; j < len(rest); j++ {
		if rest[j] == '\\' {
			j++
			continue
		}
		if rest[j] == '"' {
			var out string
			_ = json.Unmarshal([]byte(`"`+rest[:j]+`"`), &out)
			return out
		}
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
