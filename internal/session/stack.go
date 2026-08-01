package session

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/chief/prd"
	"github.com/dripcoder/loop/internal/ghstack"
)

// stackAfterStory runs the per-story git lifecycle once a story is verified and
// marked done: push its branch, open a draft pull request based on the branch
// below, then cut the next story's branch off it and continue.
//
// Nothing here is fatal. The chosen policy is to keep going, so every failure is
// recorded and reported and the run proceeds to the next story. Silence would be
// the real failure mode, which is why each one publishes an event the UI keeps
// on screen rather than a toast that scrolls away.
func (s *Session) stackAfterStory(ctx context.Context, r *run, storyID, title string, check CommitCheck) error {
	cfg := s.LoopConfig()
	if !cfg.PerStory() {
		return nil
	}

	// A story that changed nothing has nothing to review. Carry the branch
	// pointer forward untouched so the next story stacks on the same base.
	if check.Verdict == VerdictNoCommit {
		s.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text: "no commit for this story; skipping its pull request",
			Git:  &GitEvent{Op: "pr-create", State: "warn", Fatal: false},
		})
		return nil
	}

	st := s.stackState(r)
	branch := st.branchFor(storyID, title)
	base := st.baseFor(storyID)

	// The base of a stacked pull request has to exist on the remote. If the
	// previous story's push failed, walk down to the nearest branch that did make
	// it — ultimately the trunk — rather than letting gh fail with something
	// opaque about a missing ref.
	base, deviated := st.resolveRemoteBase(ctx, r.workDir, base)

	body := s.prBody(r, storyID, title, check, base, deviated)
	spec := ghstack.Spec{
		Dir:   r.workDir,
		Head:  branch,
		Base:  base,
		Title: fmt.Sprintf("feat(%s): %s %s", r.prdName, storyID, title),
		Body:  body,
		Draft: cfg.Git.Draft,
	}

	s.publish(Event{
		Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
		Git: &GitEvent{Op: "push", Branch: branch, BaseBranch: base, State: "running"},
	})

	pr, err := st.driver.Submit(ctx, spec)
	if err != nil {
		r.noteGitError()
		s.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text:  err.Error(),
			Git:   &GitEvent{Op: "pr-create", Branch: branch, BaseBranch: base, State: "error", Fatal: false, Hint: prHint(err)},
			Error: errInfo(err, prHint(err)),
		})
		// Still cut the next branch: the commits are local and valid, and
		// stopping here would strand every later story too.
	} else {
		st.recordPR(storyID, branch, pr)
		s.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Text: fmt.Sprintf("opened %s", pr.URL),
			Git: &GitEvent{
				Op: "pr-create", Branch: branch, BaseBranch: base,
				PRNumber: pr.Number, PRURL: pr.URL, State: "ok",
			},
		})
	}

	return st.startNextBranch(ctx, s, r, branch)
}

// startNextBranch creates the branch for whatever story comes next, on top of
// the one just completed.
func (st *stackState) startNextBranch(ctx context.Context, s *Session, r *run, previous string) error {
	nextID, nextTitle := nextIncompleteStory(r.prdPath)
	if nextID == "" {
		return nil // the PRD is finished; nothing left to stack
	}

	branch := st.branchFor(nextID, nextTitle)
	st.setBase(nextID, previous)

	if exists, _ := branchExists(ctx, r.workDir, branch); exists {
		// Resuming onto a branch we created earlier. Only safe when it has not
		// diverged from where we are now.
		if fastForwardable(ctx, r.workDir, branch) {
			if err := gitRun(ctx, r.workDir, "checkout", branch); err != nil {
				return fmt.Errorf("checkout existing branch %s: %w", branch, err)
			}
			return nil
		}
		return fmt.Errorf("branch %s already exists and has diverged; resolve it before continuing", branch)
	}

	if err := st.driver.AddBranch(ctx, r.workDir, branch); err != nil {
		return fmt.Errorf("create branch %s: %w", branch, err)
	}
	s.publish(Event{
		Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: nextID,
		Git: &GitEvent{Op: "branch", Branch: branch, BaseBranch: previous, State: "ok"},
	})
	return nil
}

// prBody renders the pull request description for a story.
//
// The story is read from the PRD *before* the status write wherever possible,
// because SetStoryStatus(id, "done") ticks every acceptance-criteria checkbox.
// Rendering after that would produce a body claiming every criterion was met
// regardless of whether anything was verified.
func (s *Session) prBody(r *run, storyID, title string, check CommitCheck, base string, deviated bool) string {
	var b strings.Builder

	story := s.storySnapshotFor(r, storyID)
	if story != nil && story.Description != "" {
		b.WriteString(story.Description)
		b.WriteString("\n\n")
	}

	if story != nil && len(story.Criteria) > 0 {
		b.WriteString("## Acceptance criteria\n\n")
		for _, c := range story.Criteria {
			fmt.Fprintf(&b, "- [ ] %s\n", c)
		}
		b.WriteString("\nThese are the criteria as written in the PRD. Loop does not verify them.\n\n")
	}

	if len(check.NewCommits) > 0 {
		b.WriteString("## Commits\n\n")
		for _, h := range check.NewCommits {
			short := h
			if len(short) > 8 {
				short = short[:8]
			}
			fmt.Fprintf(&b, "- `%s`\n", short)
		}
		b.WriteString("\n")
	}

	if notes := s.progressNoteFor(r, storyID); notes != "" {
		b.WriteString("## Progress notes\n\n")
		b.WriteString(notes)
		b.WriteString("\n\n")
	}

	if check.Verdict == VerdictWrongSubject {
		b.WriteString("> The agent's commit subject did not match the expected convention.\n\n")
	}
	if deviated {
		fmt.Fprintf(&b, "> Base retargeted to `%s`: the branch below this one is not on the remote.\n\n", base)
	}

	fmt.Fprintf(&b, "---\nStacked on `%s` · %s · opened by Loop\n", base, storyID)
	return b.String()
}

func (s *Session) storySnapshotFor(r *run, storyID string) *StorySnap {
	doc, err := prd.LoadPRD(r.prdPath)
	if err != nil {
		return nil
	}
	for _, st := range doc.UserStories {
		if st.ID == storyID {
			snap := toStorySnap(st)
			return &snap
		}
	}
	return nil
}

func (s *Session) progressNoteFor(r *run, storyID string) string {
	entries := progressFor(r.prdPath)[storyID]
	if len(entries) == 0 {
		return ""
	}
	return strings.TrimSpace(entries[len(entries)-1].Content)
}

// ------------------------------------------------------------ branch names --

// branchName renders the configured template for a story.
//
// Determinism matters more than looks here. Because the name is a pure function
// of (PRD, story ID, title), the branch belonging to a story can always be
// recomputed — which is what makes the state file a cache that can be deleted
// and rebuilt rather than a source of truth that must never be lost.
func branchName(template, prdName, storyID, title string) string {
	r := strings.NewReplacer(
		"{prd}", slugify(prdName),
		"{story}", strings.ToLower(storyID),
		"{slug}", slugify(title),
	)
	return trimBranch(r.Replace(template))
}

// maxSlugLength keeps branch names readable in `git branch` output and well
// inside any ref-length limit.
const maxSlugLength = 48

// slugify makes a git-ref-safe, lowercase, hyphenated token.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are invalid in a ref component
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII, unicode.IsDigit(r):
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || unicode.IsSpace(r) || unicode.IsPunct(r):
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
		if b.Len() >= maxSlugLength {
			break
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "story"
	}
	return out
}

// trimBranch removes anything git would reject at the edges of a ref.
func trimBranch(s string) string {
	s = strings.Trim(s, "-/.")
	for strings.Contains(s, "//") {
		s = strings.ReplaceAll(s, "//", "/")
	}
	return s
}

func nextIncompleteStory(prdPath string) (id, title string) {
	doc, err := prd.LoadPRD(prdPath)
	if err != nil {
		return "", ""
	}
	if st := doc.NextStory(); st != nil {
		return st.ID, st.Title
	}
	return "", ""
}

func branchExists(ctx context.Context, dir, branch string) (bool, error) {
	err := gitRun(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil, nil
}

// fastForwardable reports whether HEAD is already contained in branch, i.e.
// checking it out cannot lose anything.
func fastForwardable(ctx context.Context, dir, branch string) bool {
	return gitRun(ctx, dir, "merge-base", "--is-ancestor", "HEAD", "refs/heads/"+branch) == nil
}

func prHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not logged") || strings.Contains(msg, "authentication"):
		return "gh auth login"
	case strings.Contains(msg, "draft") && strings.Contains(msg, "plan"):
		return "draft pull requests need a paid plan on private repositories; set git.draft to false"
	case strings.Contains(msg, "no such remote") || strings.Contains(msg, "does not appear to be a git repository"):
		return "add a remote named origin"
	default:
		return ""
	}
}

// ------------------------------------------------------------ stack state --

// stackState tracks the branch and pull request per story for one run.
type stackState struct {
	driver ghstack.Driver
	cfg    config.GitConfig
	prd    string

	bases map[string]string // storyID -> the branch below it
	prs   map[string]PRRef
}

func (s *Session) stackState(r *run) *stackState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.stack == nil {
		cfg := s.loopCfgLocked()
		r.stack = &stackState{
			driver: ghstack.Select(context.Background(), string(cfg.Git.StackDriver)),
			cfg:    cfg.Git,
			prd:    r.prdName,
			bases:  make(map[string]string),
			prs:    make(map[string]PRRef),
		}
	}
	return r.stack
}

func (st *stackState) branchFor(storyID, title string) string {
	return branchName(st.cfg.BranchTemplate, st.prd, storyID, title)
}

func (st *stackState) baseFor(storyID string) string {
	if b, ok := st.bases[storyID]; ok && b != "" {
		return b
	}
	if st.cfg.BaseBranch != "" {
		return st.cfg.BaseBranch
	}
	return "main"
}

func (st *stackState) setBase(storyID, base string) { st.bases[storyID] = base }

func (st *stackState) recordPR(storyID, branch string, pr ghstack.PR) {
	st.prs[storyID] = PRRef{
		Number: pr.Number, URL: pr.URL, State: pr.State,
		Draft: pr.Draft, Base: pr.Base,
	}
}

// resolveRemoteBase walks down the stack to the nearest base that exists on the
// remote, so one failed push does not cascade into every later pull request.
func (st *stackState) resolveRemoteBase(ctx context.Context, dir, base string) (string, bool) {
	trunk := st.cfg.BaseBranch
	if trunk == "" {
		trunk = "main"
	}
	if base == trunk || ghstack.RemoteBranchExists(ctx, dir, base) {
		return base, false
	}
	return trunk, true
}
