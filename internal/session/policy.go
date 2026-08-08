package session

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/chief/git"
)

// Branch safety: deciding where a run is allowed to put its commits.
//
// This is chief's most consequential decision and in chief it exists only as
// keystroke handling inside a Bubble Tea model, so it cannot be tested or
// scripted. Here it is a pure function returning a Question, and a separate
// piece of code that acts on the answer.

// ensureRunBranch puts the checkout on the run's branch, creating it only if it
// is not already there.
//
// Resuming a PRD is the ordinary case — the branch from the previous run still
// exists, and failing with "branch already exists" would make a second run
// impossible. The worktree path has always adopted an existing worktree; this
// is the same courtesy for branches.
func (s *Session) ensureRunBranch(ctx context.Context, root, prdName, branch string) error {
	// Recorded whether or not the branch had to be created: adopting an existing
	// one is the ordinary resumed-run case, and the PRD still belongs to it.
	// A sidecar that cannot be written is not worth failing a run over.
	_ = s.recordBranch(prdName, "", branch)

	if currentBranch(ctx, root) == branch {
		return nil
	}

	exists, err := branchExists(ctx, root, branch)
	if err != nil {
		return fmt.Errorf("check for branch %s: %w", branch, err)
	}
	if exists {
		if err := gitRun(ctx, root, "checkout", branch); err != nil {
			return fmt.Errorf("switch to the existing branch %s: %w", branch, err)
		}
		s.publish(Event{
			Kind: EvGit, PRD: prdName,
			Text: "continuing on an existing branch",
			Git:  &GitEvent{Op: "branch", Branch: branch, State: "ok"},
		})
		return nil
	}

	if err := git.CreateBranch(root, branch); err != nil {
		return fmt.Errorf("create branch %s: %w", branch, err)
	}
	s.publish(Event{
		Kind: EvGit, PRD: prdName,
		Git: &GitEvent{Op: "branch", Branch: branch, State: "ok"},
	})
	return nil
}

// branchLabel names the current branch, falling back to wording that still
// reads properly on a detached HEAD.
func branchLabel(branch string) string {
	if branch == "" {
		return "the current checkout"
	}
	return branch
}

// askContext is what the caller has resolved about this particular run, as
// opposed to what the project config says in general. The layout is a per-PRD
// decision now, so the question is told rather than deriving it.
type askContext struct {
	// layout is preselected: what this PRD recorded, or the project default.
	layout BranchLayout
	// layoutOffered is false where the layout is not this run's to arrange at
	// all — git mode `off`, which creates no branches either way.
	layoutOffered bool
	// layoutSettled means the layout is shown but cannot be changed, because a
	// story has already committed under it.
	layoutSettled bool
	othersInRoot  bool
}

// perStory reports whether this run gives each story its own branch.
func (a askContext) perStory() bool { return a.layout == LayoutBranchPerStory }

// Option IDs for the branch-safety question.
const (
	optWorktree = "worktree"
	optBranch   = "branch"
	optHere     = "here"
	optCancel   = "cancel"
)

// Input keys the branch-safety answer may carry.
const (
	inputBranch = "branch"
	inputLayout = "layout"
)

// branchSafetyQuestion returns the decision a run needs before it may start.
//
// It always asks. An agent is about to make commits, and where they land is a
// choice the person starting the run should make rather than discover
// afterwards — previously a run on an ordinary branch silently committed into
// whatever was checked out, which is only the right answer sometimes.
//
// Three situations make the choice more than a preference, and each gets its
// own wording rather than a generic prompt:
//
//   - The checkout is on a protected branch. Committing agent output straight
//     onto main is almost never what anyone wants.
//   - Another PRD is already running in the same directory. Two agents editing
//     one working tree produces garbage neither of them intended.
//   - Per-story mode is on. It switches branches between stories, which inside
//     the user's own checkout would move their working tree under them while
//     they are using it.
//
// The layout rides on this question as an Input rather than arriving as a second
// prompt of its own, because two modal decisions before a run starts is one too
// many — and because where the commits go and how they are arranged are one
// decision to the person making them.
func branchSafetyQuestion(p Project, cfg config.LoopConfig, prdName string, ask askContext) *Question {
	protected := p.Branch != "" && git.IsProtectedBranch(p.Branch)
	perStory := ask.perStory()
	othersInRoot := ask.othersInRoot

	worktreeHint := filepath.Join(".chief", "worktrees", prdName)
	suggested := "chief/" + prdName

	q := &Question{
		Kind:          QBranchSafety,
		PRD:           prdName,
		DefaultOption: optWorktree,
		Inputs: []Input{{
			Key:   inputBranch,
			Label: "Branch name",
			Value: suggested,
		}},
	}
	if ask.layoutOffered {
		q.Inputs = append(q.Inputs, layoutInput(ask, cfg))
	}

	switch {
	case perStory:
		// Per-story mode genuinely requires isolation, so the wording says so
		// rather than offering "continue here" as an equal choice.
		q.Title = "Per-story branches need a worktree"
		q.Body = "Each story gets its own branch. Switching branches in this " +
			"checkout would move your working tree between stories."
		q.Options = []Option{
			{ID: optWorktree, Label: "Create a worktree", Hint: worktreeHint, Recommended: true},
			{ID: optCancel, Label: "Cancel"},
		}
		if !cfg.Git.RequireWorktree {
			q.Options = append(q.Options[:1],
				append([]Option{{
					ID: optHere, Label: "Run here anyway", Hint: "./", Destructive: true,
				}}, q.Options[1:]...)...)
		}

	case othersInRoot:
		q.Title = "This directory is already in use"
		q.Body = "Another PRD is running here. Two agents in one working tree " +
			"will overwrite each other."
		q.Options = []Option{
			{ID: optWorktree, Label: "Create a worktree", Hint: worktreeHint, Recommended: true},
			{ID: optBranch, Label: "Create a branch", Hint: suggested},
			{ID: optHere, Label: "Run here anyway", Hint: "./", Destructive: true},
			{ID: optCancel, Label: "Cancel"},
		}

	case protected:
		q.Title = fmt.Sprintf("You are on %s", p.Branch)
		q.Body = "The agent commits directly to whatever branch is checked out."
		q.DefaultOption = optBranch
		q.Options = []Option{
			{ID: optBranch, Label: "Create a branch", Hint: suggested, Recommended: true},
			{ID: optWorktree, Label: "Create a worktree", Hint: worktreeHint},
			{ID: optHere, Label: "Continue on " + p.Branch, Destructive: true},
			{ID: optCancel, Label: "Cancel"},
		}

	default:
		// Nothing is unsafe here, but the run is still about to commit
		// somewhere. Asking makes that a decision instead of a discovery.
		q.Title = "Where should this run commit?"
		q.Body = fmt.Sprintf(
			"The agent commits as it goes. It can work on a new branch, in a "+
				"separate worktree, or directly on %s.", branchLabel(p.Branch))
		q.DefaultOption = optBranch
		q.Options = []Option{
			{ID: optBranch, Label: "Create a branch", Hint: suggested, Recommended: true},
			{ID: optWorktree, Label: "Create a worktree", Hint: worktreeHint},
			{ID: optHere, Label: "Continue on " + branchLabel(p.Branch)},
			{ID: optCancel, Label: "Cancel"},
		}
	}

	return q
}

// layoutInput is the layout half of the branch-safety decision.
//
// Worded as an arrangement of commits rather than as a pull-request choice: "a
// branch per story" leaves both publishing options open, while "stacked pull
// requests" would imply a decision that is not being made yet.
func layoutInput(ask askContext, cfg config.LoopConfig) Input {
	in := Input{
		Key:   inputLayout,
		Label: "Branch layout",
		Value: string(ask.layout),
	}

	if ask.layoutSettled {
		in.ReadOnly = true
		in.Value = string(ask.layout)
		in.Hint = "Settled: this PRD already has commits arranged this way."
		return in
	}

	perStory := Choice{Value: string(LayoutBranchPerStory), Label: LayoutBranchPerStory.Label()}
	if cfg.Git.RequireWorktree {
		// Saying so here is the difference between a choice and a trap: the run
		// is refused later if these two answers disagree.
		perStory.Hint = "needs a worktree"
	}
	in.Choices = []Choice{
		{Value: string(LayoutOneBranch), Label: LayoutOneBranch.Label()},
		perStory,
	}
	return in
}

// prepareWorkspace resolves the branch-safety question and sets up wherever the
// run is going to happen. It returns the working directory.
func (s *Session) prepareWorkspace(ctx context.Context, req StartRequest, runID string) (string, error) {
	root, err := s.requireProject()
	if err != nil {
		return "", err
	}
	project := s.Project()
	cfg := s.LoopConfig()

	// An explicit working directory means the caller has already decided.
	if req.WorkDir != "" {
		return req.WorkDir, nil
	}
	if !project.IsGitRepo {
		// Nothing to protect and nothing to branch. chief behaves the same way.
		return root, nil
	}

	ask := askContext{
		layout:        s.layoutFor(req.PRD),
		layoutOffered: cfg.Git.Mode != config.GitModeOff,
		layoutSettled: s.layoutIsSettled(req.PRD),
		othersInRoot:  s.othersRunningIn(root, req.PRD),
	}
	q := branchSafetyQuestion(*project, cfg, req.PRD, ask)
	if q == nil {
		return root, nil
	}
	q.RunID = runID

	answer, err := s.askOrDefault(ctx, *q)
	if err != nil {
		return "", err
	}
	if answer.OptionID == optCancel {
		return "", fmt.Errorf("cancelled")
	}

	layout, err := resolveLayout(req.PRD, ask, answer)
	if err != nil {
		return "", err
	}
	if err := requireWorktreeFor(layout, cfg, answer.OptionID); err != nil {
		return "", err
	}
	// Recorded before anything is created, so what the branches mean is on disk
	// from the moment the run starts arranging them.
	if ask.layoutOffered {
		_ = s.recordLayout(req.PRD, layout)
	}

	branch := strings.TrimSpace(answer.Inputs[inputBranch])
	if branch == "" {
		branch = "chief/" + req.PRD
	}

	switch answer.OptionID {
	case optHere:
		return root, nil

	case optBranch:
		if err := s.ensureRunBranch(ctx, root, req.PRD, branch); err != nil {
			return "", err
		}
		return root, nil

	default: // worktree
		return s.provisionWorktree(ctx, root, req.PRD, branch, cfg)
	}
}

// resolveLayout is the layout this run will use, given what was offered and what
// came back.
//
// An answer carrying nothing means the preselected layout, which is how
// auto-answering and every caller that does not care about layout keep working.
func resolveLayout(prdName string, ask askContext, answer Answer) (BranchLayout, error) {
	chosen := BranchLayout(strings.TrimSpace(answer.Inputs[inputLayout]))
	if chosen == "" || !ask.layoutOffered {
		return ask.layout, nil
	}
	if !chosen.isKnown() {
		return "", fmt.Errorf("%q is not a branch layout", chosen)
	}
	if ask.layoutSettled && chosen != ask.layout {
		return "", fmt.Errorf(
			"%s already has commits arranged as %q; its layout cannot be changed",
			prdName, ask.layout.Label())
	}
	return chosen, nil
}

// requireWorktreeFor refuses the one combination the layout cannot honour.
//
// Per-story branches switch the checkout between stories, so running them in the
// user's own working tree would move it under them mid-run. requireWorktree is on
// by default for exactly that reason, and refusing here — with the reason — is
// better than starting a run that would do it anyway.
func requireWorktreeFor(layout BranchLayout, cfg config.LoopConfig, optionID string) error {
	if layout != LayoutBranchPerStory || !cfg.Git.RequireWorktree {
		return nil
	}
	if optionID == optWorktree {
		return nil
	}
	return fmt.Errorf(
		"%q needs a worktree: each story switches branch, which would move this checkout under you. "+
			"Start again and choose to create a worktree", layout.Label())
}

// provisionWorktree creates the worktree and runs the configured setup command,
// reporting each step as it happens.
func (s *Session) provisionWorktree(
	ctx context.Context, root, prdName, branch string, cfg config.LoopConfig,
) (string, error) {
	path := git.WorktreePathForPRD(root, prdName)
	total := 2
	if cfg.Worktree.Setup != "" {
		total = 3
	}

	step := func(i int, name, state, detail string) {
		s.publish(Event{
			Kind: EvStep, PRD: prdName, Text: detail,
			Step: &StepEvent{Group: "worktree", Name: name, State: state, Index: i, Total: total, Output: detail},
		})
	}

	step(1, "create-branch", "running", branch)
	_ = git.PruneWorktrees(root)

	step(2, "add-worktree", "running", path)
	if err := git.CreateWorktree(root, path, branch); err != nil {
		step(2, "add-worktree", "error", err.Error())
		return "", fmt.Errorf("create worktree at %s: %w", path, err)
	}
	step(2, "add-worktree", "ok", path)

	if cfg.Worktree.Setup == "" {
		return path, nil
	}

	step(3, "run-setup", "running", cfg.Worktree.Setup)
	if err := s.runSetup(ctx, path, prdName, cfg.Worktree.Setup, total); err != nil {
		step(3, "run-setup", "error", err.Error())
		// The worktree exists and is usable; a failed `npm ci` is worth
		// reporting but not worth refusing to run over.
		return path, nil
	}
	step(3, "run-setup", "ok", "")
	return path, nil
}

// runSetup executes the worktree setup command, streaming its output.
//
// Two deviations from chief, both because a GUI makes them matter: the output is
// streamed rather than collected with CombinedOutput, since a silent three-minute
// `npm install` is unacceptable when there is a progress list on screen; and it
// runs under the context, so cancelling actually kills it. chief's version
// cannot be cancelled at all — pressing Esc during setup only appears to work.
func (s *Session) runSetup(ctx context.Context, dir, prdName, command string, total int) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	// Setup commands are chatty on both streams and interleaving matters for
	// reading them, so both go down one pipe.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = pw, pw

	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	// The child holds its own copy of the write end; ours has to go or the
	// reader below never sees EOF.
	pw.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			s.publish(Event{
				Kind: EvStep, PRD: prdName, Text: line,
				Step: &StepEvent{
					Group: "setup", Name: "run-setup", State: "running",
					Index: 3, Total: total, Output: line,
				},
			})
		}
	}()

	err = cmd.Wait()
	pr.Close()
	<-done
	return err
}

// othersRunningIn reports whether a different PRD is already running in dir.
func (s *Session) othersRunningIn(dir, exceptPRD string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.live {
		if r.prdName == exceptPRD {
			continue
		}
		if r.workDir == dir && isLive(r.snapshot().State) {
			return true
		}
	}
	return false
}
