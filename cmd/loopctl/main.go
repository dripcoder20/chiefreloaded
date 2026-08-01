// Command loopctl drives internal/session from a terminal.
//
// It exists for three reasons, in order of importance:
//
//  1. It proves the session API is genuinely headless. If a feature cannot be
//     driven from here, it has leaked into the GUI and belongs back in the
//     session package.
//  2. It is the harness for the end-to-end tests, which run a whole PRD against
//     a scripted fake agent with no window and no API cost.
//  3. It is a usable debugging tool when the GUI is misbehaving and you need to
//     know whether the problem is the engine or the rendering.
//
// It is deliberately not a replacement for the chief CLI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/dripcoder/loop/internal/session"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "loopctl:", err)
		os.Exit(1)
	}
}

const usage = `loopctl — headless driver for the Loop engine

Usage:
  loopctl [flags] <command> [flags] [args]

Commands:
  doctor            Report the detected environment
  list              List the PRDs in a project
  show <prd>        Show a PRD's stories
  watch             Stream the session event log until interrupted

Flags:
  -C <dir>          Project directory (default: current directory)
  -json             Emit JSON instead of a table

Flags may appear on either side of the command:
  loopctl -C ../app list
  loopctl show checkout -json
`

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	fs := flag.NewFlagSet("loopctl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("C", ".", "project directory")
	asJSON := fs.Bool("json", false, "emit JSON")

	// Go's flag package stops at the first positional, so a single Parse would
	// silently ignore anything after the command. Parse repeatedly, peeling off
	// one positional each round, so flags work anywhere on the line:
	// `loopctl -C ../app list` and `loopctl show checkout -C ../app` are both
	// things people type, and rejecting either is just rudeness.
	var positional []string
	for remaining := args; len(remaining) > 0; {
		if err := fs.Parse(remaining); err != nil {
			return err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		remaining = rest[1:]
	}

	if len(positional) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, cmdArgs := positional[0], positional[1:]

	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "doctor", "list", "show", "watch":
	default:
		return fmt.Errorf("unknown command %q (try `loopctl help`)", cmd)
	}

	// Ctrl-C must shut the session down cleanly rather than leaving a half-run
	// PRD behind; from M5 that includes resetting in-progress stories.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := session.New(session.Options{})
	if err != nil {
		return err
	}
	defer s.Close()

	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if _, err := s.OpenProject(ctx, root); err != nil {
		return err
	}

	switch cmd {
	case "doctor":
		return doctor(s, *asJSON)
	case "list":
		return list(s, *asJSON)
	case "show":
		if len(cmdArgs) < 1 {
			return fmt.Errorf("show needs a PRD name")
		}
		return show(s, cmdArgs[0], *asJSON)
	case "watch":
		return watch(ctx, s)
	}
	return nil
}

func doctor(s *session.Session, asJSON bool) error {
	env := s.Environment()
	if asJSON {
		return emitJSON(env)
	}

	p := s.Project()
	w := table()
	fmt.Fprintf(w, "project\t%s\n", p.Root)
	fmt.Fprintf(w, "git repo\t%s\n", yesNo(p.IsGitRepo))
	if p.IsGitRepo {
		fmt.Fprintf(w, "branch\t%s\n", or(p.Branch, "(none)"))
		fmt.Fprintf(w, "default base\t%s\n", or(p.DefaultBase, "(unknown)"))
		fmt.Fprintf(w, ".chief ignored\t%s\n", yesNo(p.ChiefIgnored))
	}
	fmt.Fprintf(w, "chief engine\t%s\n", env.ChiefRef)
	fmt.Fprintf(w, "git\t%s\n", tool(env.Git))
	fmt.Fprintf(w, "gh\t%s\n", tool(env.GH))
	fmt.Fprintf(w, "gh auth\t%s\n", yesNo(env.GHAuth))
	fmt.Fprintf(w, "gh-stack\t%s\n", tool(env.GHStack))
	// The resolved answer, not the configured one: "auto" is useless to a user
	// trying to work out why their PRs are not stacking.
	fmt.Fprintf(w, "stacked PRs\t%s\n", stackModeDescription(env.StackMode))

	names := make([]string, 0, len(env.Agents))
	for _, a := range env.Agents {
		names = append(names, a.Name)
	}
	fmt.Fprintf(w, "agents\t%s\n", or(strings.Join(names, ", "), "(none found)"))
	return w.Flush()
}

func stackModeDescription(mode string) string {
	switch mode {
	case "gh-stack":
		return "gh-stack (native GitHub stacks)"
	case "manual":
		return "manual (gh pr create --draft --base); install github/gh-stack for native stacks"
	default:
		return "unavailable — needs gh, authenticated"
	}
}

func list(s *session.Session, asJSON bool) error {
	prds := s.PRDs()
	if asJSON {
		return emitJSON(prds)
	}
	if len(prds) == 0 {
		fmt.Println("No PRDs found. Create one with `chief new`.")
		return nil
	}

	w := table()
	fmt.Fprintln(w, "NAME\tPROGRESS\tSTATE\tTITLE")
	for _, p := range prds {
		title := p.Title
		if p.ParseError != "" {
			title = "unreadable: " + p.ParseError
		}
		if p.Legacy {
			title += "  (legacy .chief/prd.md)"
		}
		fmt.Fprintf(w, "%s\t%d/%d\t%s\t%s\n", p.Name, p.Completed, p.Total, p.State, title)
	}
	return w.Flush()
}

func show(s *session.Session, name string, asJSON bool) error {
	detail, err := s.PRD(name)
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON(detail)
	}

	fmt.Printf("%s — %d/%d stories\n", detail.Title, detail.Completed, detail.Total)
	if detail.ParseError != "" {
		fmt.Printf("\n  cannot parse %s:\n  %s\n", detail.Path, detail.ParseError)
		return nil
	}

	progress, err := s.Progress(name)
	if err != nil {
		return err
	}

	w := table()
	fmt.Fprintln(w, "\n \tID\tSTATUS\tTITLE\tNOTES")
	for _, st := range detail.Stories {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n",
			statusIcon(st.Status), st.ID, st.Status, st.Title, len(progress[st.ID]))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Say this out loud rather than let anyone read a completed story's ticked
	// boxes as evidence: chief ticks them all as a side effect of marking the
	// story done, whether or not anything was verified.
	for _, st := range detail.Stories {
		if st.Status == session.StatusDone && len(st.Criteria) > 0 {
			fmt.Println("\nNote: acceptance criteria on completed stories are auto-checked when")
			fmt.Println("the story is marked done. They are not a record of what was verified.")
			break
		}
	}
	return nil
}

func watch(ctx context.Context, s *session.Session) error {
	fmt.Fprintln(os.Stderr, "watching; press ctrl-c to stop")

	enc := json.NewEncoder(os.Stdout)
	go func() {
		<-ctx.Done()
		s.Close()
	}()

	// Reading to completion is the contract: the channel closes once Close has
	// drained it.
	for ev := range s.Events() {
		if ev.Dropped > 0 {
			fmt.Fprintf(os.Stderr, "warning: %d events dropped before seq %d\n", ev.Dropped, ev.Seq)
		}
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

// ------------------------------------------------------------------ output --

func table() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func statusIcon(s session.Status) string {
	switch s {
	case session.StatusDone:
		return "+"
	case session.StatusInProgress:
		return ">"
	case session.StatusBlocked:
		return "!"
	default:
		return "-"
	}
}

func tool(t session.Tool) string {
	if !t.Available {
		if t.Hint != "" {
			return "not found — " + t.Hint
		}
		return "not found"
	}
	if t.Version != "" {
		return t.Version
	}
	return "installed"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
