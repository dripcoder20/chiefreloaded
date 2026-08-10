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
  run <prd>         Run a PRD to completion, streaming progress
  usage             Show the cumulative usage roll-up (project/session/story)
  workflow <prd>    Show a PRD's implementation agent and stacking settings
  publish <prd>     Push the PRD's branch and open one pull request for its work
  watch             Stream the session event log until interrupted

Flags:
  -C <dir>          Project directory (default: current directory)
  -json             Emit JSON instead of a table
  -draft            With publish: open the pull request as a draft
  -stack            With publish: open one pull request per story, bottom-up

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
	draft := fs.Bool("draft", false, "open the pull request as a draft")
	stacked := fs.Bool("stack", false, "open one pull request per story")

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
	case "doctor", "list", "show", "run", "usage", "watch", "workflow", "publish":
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
	case "run":
		if len(cmdArgs) < 1 {
			return fmt.Errorf("run needs a PRD name")
		}
		return runPRD(ctx, s, cmdArgs[0])
	case "usage":
		return showUsage(s, *asJSON)
	case "workflow":
		if len(cmdArgs) < 1 {
			return fmt.Errorf("workflow needs a PRD name")
		}
		return showWorkflow(s, cmdArgs[0], *asJSON)
	case "publish":
		if len(cmdArgs) < 1 {
			return fmt.Errorf("publish needs a PRD name")
		}
		return publishPRD(ctx, s, publishArgs{
			prd: cmdArgs[0], draft: *draft, stacked: *stacked, asJSON: *asJSON,
		})
	case "watch":
		return watch(ctx, s)
	}
	return nil
}

// runPRD drives a PRD to completion, printing progress as it goes.
//
// Questions answer themselves here. A terminal could prompt, but the value of
// this command is unattended operation — it is what the end-to-end tests use —
// and every question has a recommended option chosen to be the safe one.
func runPRD(ctx context.Context, s *session.Session, name string) error {
	s.AutoAnswer(true)

	printed := make(chan struct{})
	go func() {
		defer close(printed)
		for ev := range s.Events() {
			line := describe(ev)
			if line != "" {
				fmt.Println(line)
			}
		}
	}()

	runID, err := s.Start(ctx, session.StartRequest{PRD: name})
	if err != nil {
		return err
	}

	// A run deliberately outlives the context it was started with — in the GUI
	// that context belongs to a single bound method call, and killing the run
	// when it returns would be absurd. The consequence here is that Ctrl-C has
	// to stop the run explicitly. Without this the process exits with a story
	// still marked in-progress, and the next run finds it wedged.
	go func() {
		<-ctx.Done()
		fmt.Fprintln(os.Stderr, "\nstopping the run…")
		_ = s.Stop(runID)
	}()

	// Wait on the run itself, not on ctx: once stopping has been asked for we
	// want the cleanup to finish, which is the whole point of asking.
	snap, err := s.WaitForRun(context.WithoutCancel(ctx), runID)
	_ = s.Close()
	<-printed

	if err != nil {
		return err
	}

	fmt.Printf("\n%s: %s", name, snap.State)
	if snap.Error != nil {
		fmt.Printf(" — %s", snap.Error.Message)
	}
	fmt.Printf(" (%d attempt(s))\n", snap.Attempt)

	// A run that ends anywhere but complete is a failure for scripting purposes,
	// so the exit code has to say so.
	if snap.State != session.StateComplete {
		return fmt.Errorf("run did not complete")
	}
	return nil
}

// describe renders one event as a line, or "" for the ones not worth printing.
func describe(ev session.Event) string {
	switch ev.Kind {
	case session.EvStoryStarted:
		return fmt.Sprintf("▶  %s  %s", ev.StoryID, ev.Text)
	case session.EvStoryDone:
		return fmt.Sprintf("✓  %s  %s", ev.StoryID, ev.Text)
	case session.EvStorySkipped:
		return fmt.Sprintf("—  %s  %s", ev.StoryID, ev.Text)
	case session.EvStoryFailed:
		return fmt.Sprintf("✗  %s  %s", ev.StoryID, ev.Text)
	case session.EvStoryAttempt:
		return fmt.Sprintf("↻  %s  %s", ev.StoryID, ev.Text)
	case session.EvGit:
		if ev.Git == nil || ev.Git.State == "running" {
			return ""
		}
		return fmt.Sprintf("git %s %s: %s", ev.Git.Op, ev.Git.State, ev.Text)
	case session.EvAgentWatchdog:
		return "⏱  the agent went quiet and was restarted"
	case session.EvRunError:
		return fmt.Sprintf("✗  %s", ev.Text)
	case session.EvRunComplete:
		return "🎉 all stories complete"
	default:
		return ""
	}
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

// showUsage prints the cumulative usage roll-up. The JSON form is the whole
// read model the GUI status bar and usage panel render from — project (General),
// per-run (Session) and per-story (Story) scopes — so the end-to-end tests can
// assert live totals and, after a restart, that persisted history reloads.
// showWorkflow prints a PRD's workflow settings plus the agent that would
// actually implement it, which is the resolution the run itself performs.
func showWorkflow(s *session.Session, name string, asJSON bool) error {
	workflow, err := s.PRDWorkflowFor(name)
	if err != nil {
		return err
	}
	// Resolution can legitimately fail — a saved agent that is no longer
	// installed — and reporting that is the point of showing it here.
	resolved, resolveErr := s.ResolveImplementationAgent(name)

	// What the runs did with branches, read from the sidecar rather than from a
	// live run — which is the point: this process performed none of them.
	git, _ := s.PRDGitFor(name)

	if asJSON {
		return emitJSON(struct {
			Workflow      session.PRDWorkflow   `json:"workflow"`
			ResolvedAgent string                `json:"resolvedAgent"`
			ResolveError  string                `json:"resolveError,omitempty"`
			Git           session.PRDGitState   `json:"git"`
			Branches      []session.StoryBranch `json:"branches"`
			BasesKnown    bool                  `json:"basesKnown"`
		}{
			Workflow:      workflow,
			ResolvedAgent: resolved,
			ResolveError:  errText(resolveErr),
			Git:           git,
			Branches:      git.StoryBranches(),
			BasesKnown:    git.BasesAreKnown(),
		})
	}

	w := table()
	fmt.Fprintf(w, "implementation agent\t%s\n", orNone(workflow.ImplementationAgent))
	fmt.Fprintf(w, "resolved agent\t%s\n", orNone(resolved))
	if resolveErr != nil {
		fmt.Fprintf(w, "resolve error\t%s\n", resolveErr)
	}
	fmt.Fprintf(w, "branch layout\t%s\n", recordedLayout(git))
	fmt.Fprintf(w, "run branch\t%s\n", orNone(git.Branch))
	fmt.Fprintf(w, "run branch base\t%s\n", orNone(git.Base))
	if err := w.Flush(); err != nil {
		return err
	}
	return printStoryBranches(git)
}

// printStoryBranches lists the story branches a run created, in creation order,
// with what each was based on. Order is the whole point — it is what a stack
// means — so it is printed as a list rather than a keyed table.
func printStoryBranches(git session.PRDGitState) error {
	branches := git.StoryBranches()
	if len(branches) == 0 {
		return nil
	}

	fmt.Printf("\nbranches (in creation order)\n")
	w := table()
	for _, b := range branches {
		// A story under a single-branch layout has a record but no branch of its
		// own; listing it here would invent one.
		if !b.HasBranch() {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\ton %s%s\n", b.StoryID, b.Branch, orNone(b.Base), publishNote(b))
	}
	return w.Flush()
}

// publishArgs is one invocation of `loopctl publish`, kept as a struct so the
// command stays within the project's parameter limit.
type publishArgs struct {
	prd     string
	draft   bool
	stacked bool
	asJSON  bool
}

// publishPRD opens one pull request for a PRD's work.
//
// The refusals — a live run, a project that is not a git repository, a PRD with
// no commit — are the session's, reported here as a plain error. Nothing in this
// command re-implements them; that is the point of driving the engine from here.
func publishPRD(ctx context.Context, s *session.Session, args publishArgs) error {
	if args.stacked {
		return publishStack(ctx, s, args)
	}
	report, err := s.PublishPullRequest(ctx, session.PublishRequest{PRD: args.prd, Draft: args.draft})
	if err != nil {
		return err
	}
	if args.asJSON {
		return emitJSON(report)
	}

	w := table()
	fmt.Fprintf(w, "branch\t%s\n", report.Branch)
	fmt.Fprintf(w, "base\t%s\n", report.Base)
	fmt.Fprintf(w, "stories\t%s\n", strings.Join(report.Stories, ", "))
	if report.PR != nil {
		fmt.Fprintf(w, "pull request\t#%d %s\n", report.PR.Number, report.PR.URL)
	}
	fmt.Fprintf(w, "action\t%s\n", publishAction(report.Updated))
	return w.Flush()
}

// publishStack opens one pull request per story, from the bottom of the stack
// upwards.
//
// The report is printed whether or not publishing succeeded: a stack that fails
// half way has still created what it created, and the command's job is to say so
// rather than to leave the user guessing what is on GitHub.
func publishStack(ctx context.Context, s *session.Session, args publishArgs) error {
	report, err := s.PublishStack(ctx, session.PublishRequest{PRD: args.prd, Draft: args.draft})
	if len(report.Stories) == 0 {
		return err
	}
	if args.asJSON {
		if jsonErr := emitJSON(report); jsonErr != nil {
			return jsonErr
		}
		return err
	}
	if printErr := printStackReport(report); printErr != nil {
		return printErr
	}
	return err
}

func printStackReport(report session.StackReport) error {
	w := table()
	for _, story := range report.Stories {
		fmt.Fprintf(w, "%s\t%s\ton %s\t%s\n",
			story.StoryID, orNone(story.Branch), orNone(story.Base), storyOutcome(story))
	}
	return w.Flush()
}

// storyOutcome is what happened to one story, in the words that distinguish the
// four cases: opened, updated, deliberately skipped, and failed.
func storyOutcome(story session.StoryPublish) string {
	if story.Error != "" {
		return "failed: " + story.Error
	}
	if story.Skipped != "" {
		return "skipped: " + story.Skipped
	}
	if story.PR == nil {
		return "no pull request"
	}
	return fmt.Sprintf("%s #%d %s", publishVerb(story.Updated), story.PR.Number, story.PR.URL)
}

func publishVerb(updated bool) string {
	if updated {
		return "updated"
	}
	return "opened"
}

func publishAction(updated bool) string {
	if updated {
		return "updated the existing pull request"
	}
	return "opened a pull request"
}

// publishNote marks the branches that publishing must skip.
func publishNote(b session.StoryBranch) string {
	if b.HasSomethingToPublish() {
		return ""
	}
	return "\tno commit"
}

// recordedLayout reports the layout a run has chosen for this PRD. A PRD that has
// never run has none, which is not an error worth failing the command over.
func recordedLayout(git session.PRDGitState) string {
	if git.Layout == "" {
		return "not chosen yet"
	}
	return string(git.Layout)
}

func orNone(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func showUsage(s *session.Session, asJSON bool) error {
	report := s.Usage()
	if asJSON {
		return emitJSON(report)
	}

	w := table()
	fmt.Fprintln(w, "SCOPE\tINPUT\tOUTPUT\tCACHE-R\tCACHE-W\tTOTAL\tCOST")
	printUsageRow(w, "general", report.Project)
	for runID, totals := range report.Runs {
		printUsageRow(w, "session "+runID, totals)
	}
	for storyKey, totals := range report.Stories {
		printUsageRow(w, "story "+storyKey, totals)
	}
	return w.Flush()
}

func printUsageRow(w *tabwriter.Writer, scope string, t session.UsageTotals) {
	cost := "—"
	if t.Currency != "" {
		cost = fmt.Sprintf("%.4f %s", t.Cost, t.Currency)
	}
	fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%s\n",
		scope, t.InputTokens, t.OutputTokens, t.CacheReadTokens, t.CacheWriteTokens, t.TotalTokens, cost)
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
