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

// storyDone is one verified story, as everything that runs after the agent has
// exited sees it.
type storyDone struct {
	ID    string
	Title string
	Check CommitCheck
}

// stackAfterStory closes out one story's place in the stack once it is verified
// and marked done, and hands its branch on as the base of the story above.
//
// It deliberately touches nothing outside the repository. A run pushes no branch
// and opens no pull request — publishing is an action the user takes once they
// have read the result — so all that is left here is the bookkeeping a later
// publish reads back off disk.
func (s *Session) stackAfterStory(r *run, done storyDone) error {
	// Recorded under either layout, because whether a story committed is what
	// decides if a later pull request should describe it, and this is the only
	// moment that is known. Under a single-branch layout no story owns a branch,
	// but the entry captureStoryBody just created is still there to mark.
	if done.Check.Verdict == VerdictNoCommit {
		_ = s.recordNoCommit(r.prdName, done.ID)
	}
	if s.layoutFor(r.prdName) != LayoutBranchPerStory {
		return nil
	}

	if done.Check.Verdict == VerdictNoCommit {
		s.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: done.ID,
			Text: "no commit for this story; it has nothing to publish",
			Git:  &GitEvent{Op: "stack", State: "warn", Fatal: false},
		})
		s.skipEmptyStory(r, done.ID)
		return nil
	}

	// The next story's branch is created when that story starts, not here, so
	// there is one place that guarantees "HEAD is this story's branch". All that
	// is needed now is to remember what it will stack on.
	st := s.stackState(r)
	if nextID, _ := nextIncompleteStory(r.prdPath); nextID != "" {
		st.setBase(nextID, st.branchFor(done.ID, done.Title))
	}
	return nil
}

// skipEmptyStory records that a story committed nothing and hands its base on to
// the story above it.
//
// A branch with no commit on it is the same commit as the branch below, so it has
// nothing to review and must not become anyone's base: the story above stacks on
// whatever this one was going to stack on, which is also what git does when its
// branch is cut from this unchanged checkout.
//
// The flag itself is recorded by the caller, under either layout.
func (s *Session) skipEmptyStory(r *run, storyID string) {
	st := s.stackState(r)
	if nextID, _ := nextIncompleteStory(r.prdPath); nextID != "" {
		st.setBase(nextID, st.baseFor(storyID))
	}
}

// ensureStoryBranch puts the worktree on the branch this story belongs to.
//
// This is the invariant the whole feature rests on: when the agent starts, HEAD
// is already the branch its commit should land on. Creating branches after the
// fact instead would mean the first story has nowhere to go — which is exactly
// what happened before this existed, and the stack driver rejected every
// command because no stack had been created.
func (s *Session) ensureStoryBranch(ctx context.Context, r *run, storyID, title string) error {
	if s.layoutFor(r.prdName) != LayoutBranchPerStory {
		return nil
	}

	st := s.stackState(r)
	branch := st.branchFor(storyID, title)
	// Recorded before the checkout, with the branch it will be cut from: the
	// branch belongs to this story from the moment it is named, a run that dies
	// mid-checkout has still claimed it, and the base is what lets whatever
	// publishes later rebuild the stack without the run's in-memory state.
	_ = s.recordStoryBranch(r.prdName, StoryBranch{
		StoryID: storyID, Branch: branch, Base: st.baseFor(storyID),
	})

	if currentBranch(ctx, r.workDir) == branch {
		return nil
	}

	if !st.initialised {
		// The bottom of the stack. `gh stack init` adopts the branch if it
		// already exists and creates it otherwise, so a resumed run is fine.
		if err := st.driver.Init(ctx, r.workDir, st.trunk, branch); err != nil {
			return fmt.Errorf("start the stack at %s: %w", branch, err)
		}
		st.initialised = true
		st.setBase(storyID, st.trunk)
		s.publish(Event{
			Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
			Git: &GitEvent{Op: "stack", Branch: branch, BaseBranch: st.trunk, State: "ok"},
		})
		return nil
	}

	if exists, _ := branchExists(ctx, r.workDir, branch); exists {
		if !fastForwardable(ctx, r.workDir, branch) {
			return fmt.Errorf("branch %s already exists and has diverged; resolve it before continuing", branch)
		}
		return gitRun(ctx, r.workDir, "checkout", branch)
	}

	if err := st.driver.AddBranch(ctx, r.workDir, branch); err != nil {
		return fmt.Errorf("create branch %s: %w", branch, err)
	}
	s.publish(Event{
		Kind: EvGit, RunID: r.id, PRD: r.prdName, StoryID: storyID,
		Git: &GitEvent{Op: "branch", Branch: branch, BaseBranch: st.baseFor(storyID), State: "ok"},
	})
	return nil
}

// prBodyDraft is everything a story's pull-request description is composed from,
// gathered at the one moment all of it is still true.
type prBodyDraft struct {
	Story StorySnap
	Check CommitCheck
	// Base is the branch this story's work sits on top of.
	Base string
	// Notes is the story's latest progress.md entry.
	Notes string
}

// captureStoryBody composes a story's pull-request description and stores it
// against the story's branch record.
//
// It must run before markDone writes the status. SetStoryStatus(id, "done") ticks
// every acceptance-criteria checkbox as a side effect, so a description composed
// after that write would present the write itself as evidence that each criterion
// was met. Nothing publishes during a run any more, which makes this the only
// moment the honest text exists — hence composing it now and keeping it, rather
// than rendering it whenever the user eventually publishes.
func (s *Session) captureStoryBody(r *run, done storyDone) {
	story := s.storySnapshotFor(r, done.ID)
	if story == nil {
		return
	}
	_ = s.recordStoryBody(r.prdName, done.ID, prBody(prBodyDraft{
		Story: *story,
		Check: done.Check,
		Base:  s.baseForStory(r, done.ID),
		Notes: s.progressNoteFor(r, done.ID),
	}))
}

// baseForStory is the branch a story's work sits on top of: the story below it
// under a branch per story, and whatever the run branch was cut from otherwise.
// Empty when nothing has recorded one, which a description simply omits.
func (s *Session) baseForStory(r *run, storyID string) string {
	if s.layoutFor(r.prdName) == LayoutBranchPerStory {
		return s.stackState(r).baseFor(storyID)
	}
	state, err := s.PRDGitFor(r.prdName)
	if err != nil {
		return ""
	}
	return state.Base
}

// prBody renders the pull request description for a story.
func prBody(d prBodyDraft) string {
	var b strings.Builder

	if d.Story.Description != "" {
		b.WriteString(d.Story.Description)
		b.WriteString("\n\n")
	}
	writeCriteria(&b, d.Story)
	writeCommits(&b, d.Check)

	if d.Notes != "" {
		b.WriteString("## Progress notes\n\n")
		b.WriteString(d.Notes)
		b.WriteString("\n\n")
	}
	if d.Check.Verdict == VerdictWrongSubject {
		b.WriteString("> The agent's commit subject did not match the expected convention.\n\n")
	}

	b.WriteString("---\n")
	if d.Base != "" {
		fmt.Fprintf(&b, "Based on `%s` · ", d.Base)
	}
	fmt.Fprintf(&b, "%s · prepared by Loop\n", d.Story.ID)
	return b.String()
}

// writeCriteria renders the acceptance criteria and says what they are worth.
//
// A story whose criteria are no longer authoritative has had every box ticked by
// the status write rather than by anything being verified, and a reviewer reading
// the description has no way to tell the two apart unless it says so.
func writeCriteria(b *strings.Builder, story StorySnap) {
	if len(story.Criteria) == 0 {
		return
	}
	b.WriteString("## Acceptance criteria\n\n")
	for _, c := range story.Criteria {
		fmt.Fprintf(b, "- [ ] %s\n", c)
	}
	b.WriteString("\n")
	b.WriteString(criteriaNote(story))
	b.WriteString("\n")
}

func criteriaNote(story StorySnap) string {
	if story.CriteriaAreAuthoritative {
		return "These are the criteria as the PRD stated them when the story was verified. Loop does not verify them.\n"
	}
	return "These criteria were read after the story was marked done, by which point the status write had already ticked every box.\n"
}

func writeCommits(b *strings.Builder, check CommitCheck) {
	if len(check.NewCommits) == 0 {
		return
	}
	b.WriteString("## Commits\n\n")
	for _, h := range check.NewCommits {
		short := h
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Fprintf(b, "- `%s`\n", short)
	}
	b.WriteString("\n")
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

// ------------------------------------------------------------ stack state --

// stackState tracks the branch and pull request per story for one run.
type stackState struct {
	driver ghstack.Driver
	cfg    config.GitConfig
	prd    string
	// trunk is what the bottom of the stack targets: the configured base branch
	// or the repository's default.
	trunk string
	// initialised records whether `gh stack init` has run. The driver refuses
	// every other command until a stack exists, so this cannot be skipped.
	initialised bool

	bases map[string]string // storyID -> the branch below it
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
			trunk:  s.trunkBranchLocked(),
			bases:  make(map[string]string),
		}
	}
	return r.stack
}

// fallbackTrunk is what a repository with no configured base branch and no
// detected default branch is assumed to target.
const fallbackTrunk = "main"

// trunkBranch is what the bottom of a PRD's work targets: the configured base
// branch, then the repository's own default. It is what a pull request opened for
// a PRD is based on, and what the bottom of a stack was cut from.
func (s *Session) trunkBranch() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trunkBranchLocked()
}

func (s *Session) trunkBranchLocked() string {
	if trunk := s.loopCfgLocked().Git.BaseBranch; trunk != "" {
		return trunk
	}
	if s.project != nil && s.project.DefaultBase != "" {
		return s.project.DefaultBase
	}
	return fallbackTrunk
}

func (st *stackState) branchFor(storyID, title string) string {
	return branchName(st.cfg.BranchTemplate, st.prd, storyID, title)
}

func (st *stackState) baseFor(storyID string) string {
	if b, ok := st.bases[storyID]; ok && b != "" {
		return b
	}
	return st.trunk
}

func (st *stackState) setBase(storyID, base string) { st.bases[storyID] = base }

// resolveRemoteBase walks down the stack to the nearest base that exists on the
// remote, so one failed push does not cascade into every later pull request.
//
// Nothing during a run needs this — a run reaches no remote at all. It is the
// stack's own rule about what a published base may be, and publishing applies it.
func (st *stackState) resolveRemoteBase(ctx context.Context, dir, base string) (string, bool) {
	if base == st.trunk || ghstack.RemoteBranchExists(ctx, dir, base) {
		return base, false
	}
	return st.trunk, true
}
