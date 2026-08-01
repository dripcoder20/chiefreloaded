package session

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dripcoder/loop/internal/chief/chiefver"
)

// knownAgents are the providers the vendored chief can drive. Gemini exists
// upstream but landed after v0.8.0; it appears here when sync-upstream picks it
// up and agent.Resolve learns about it.
var knownAgents = []string{"claude", "codex", "opencode", "cursor"}

// probeTimeout bounds each `--version` style call. These are local binaries, so
// anything slower than this is hung, and a hung probe must not wedge startup.
const probeTimeout = 5 * time.Second

// ProbeEnvironment reports what Loop found on this machine.
//
// Nothing here is fatal on its own. Missing `gh` disables pull requests; missing
// gh-stack falls back to manual base-chaining, which produces the same branch
// topology without a native GitHub Stack object. The UI is expected to render
// the degradation rather than refuse to start, so every negative result carries
// a Hint with the remedy.
//
// Probes run concurrently — serially this is about a second of dead time on a
// cold start, all of it waiting on unrelated processes.
func ProbeEnvironment(ctx context.Context) Environment {
	env := Environment{
		ChiefRef: chiefver.Ref,
		Platform: runtime.GOOS,
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		agents = make([]Tool, 0, len(knownAgents))
	)

	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	run(func() {
		t := probeTool(ctx, "git", "--version", "install Xcode command line tools, or your package manager's git")
		mu.Lock()
		env.Git = t
		mu.Unlock()
	})

	run(func() {
		t := probeTool(ctx, "gh", "--version", "https://cli.github.com — required for pull requests")
		auth := false
		if t.Available {
			// `gh auth status` exits non-zero when unauthenticated; that is the
			// whole signal, so the output is not worth parsing.
			auth = runQuiet(ctx, "gh", "auth", "status") == nil
		}
		mu.Lock()
		env.GH, env.GHAuth = t, auth
		mu.Unlock()
	})

	run(func() {
		t := Tool{
			Name: "gh-stack",
			Hint: "gh extension install github/gh-stack",
		}
		if _, err := exec.LookPath("gh"); err == nil {
			out, err := output(ctx, "gh", "extension", "list")
			t.Available = err == nil && strings.Contains(out, "github/gh-stack")
		}
		mu.Lock()
		env.GHStack = t
		mu.Unlock()
	})

	for _, name := range knownAgents {
		run(func() {
			path, err := exec.LookPath(name)
			if err != nil {
				return // absent agents are simply not listed
			}
			mu.Lock()
			agents = append(agents, Tool{Name: name, Available: true, Path: path})
			mu.Unlock()
		})
	}

	wg.Wait()

	// Stable order regardless of which probe finished first, so the UI does not
	// reshuffle its provider list between launches.
	sorted := make([]Tool, 0, len(agents))
	for _, want := range knownAgents {
		for _, got := range agents {
			if got.Name == want {
				sorted = append(sorted, got)
			}
		}
	}
	env.Agents = sorted
	env.StackMode = resolveStackMode(env)
	return env
}

// resolveStackMode picks the stacked-PR driver the environment can actually
// support. It is the "auto" setting resolved to a concrete answer, so the UI can
// state what will happen rather than what was configured.
func resolveStackMode(env Environment) string {
	switch {
	case !env.GH.Available || !env.GHAuth:
		return "none"
	case env.GHStack.Available:
		return "gh-stack"
	default:
		return "manual"
	}
}

func probeTool(ctx context.Context, name, versionArg, hint string) Tool {
	path, err := exec.LookPath(name)
	if err != nil {
		return Tool{Name: name, Hint: hint}
	}
	t := Tool{Name: name, Available: true, Path: path}
	if out, err := output(ctx, name, versionArg); err == nil {
		t.Version = firstVersionToken(out)
	}
	return t
}

// firstVersionToken pulls the version out of a first line like
// "git version 2.50.1" or "gh version 2.68.0 (2025-03-05)".
func firstVersionToken(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	for _, f := range strings.Fields(line) {
		if f == "" {
			continue
		}
		if c := f[0]; c == 'v' || (c >= '0' && c <= '9') {
			if strings.ContainsAny(f, "0123456789") && strings.Contains(f, ".") {
				return strings.TrimPrefix(f, "v")
			}
		}
	}
	return line
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func runQuiet(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}
