package session

import (
	"context"
	"os/exec"
	"strings"

	"github.com/dripcoder/loop/internal/chief/git"
)

// Verdict is what we concluded about an agent's attempt at a story.
type Verdict string

const (
	// VerdictCommitted: a new commit exists with the conventional subject.
	VerdictCommitted Verdict = "committed"
	// VerdictWrongSubject: new commits exist but none match the expected subject.
	// Accepted anyway — the subject is a convention, not a contract, and failing
	// here over a formatting detail would be maddening.
	VerdictWrongSubject Verdict = "committed-wrong-subject"
	// VerdictNoCommit: nothing was committed.
	VerdictNoCommit Verdict = "no-commit"
)

// CommitCheck is the evidence behind a Verdict.
type CommitCheck struct {
	HeadBefore string   `json:"headBefore"`
	HeadAfter  string   `json:"headAfter"`
	NewCommits []string `json:"newCommits,omitempty"`
	// Matched is the commit whose subject is "feat: <ID> - <title>", if any.
	Matched string `json:"matched,omitempty"`
	// Dirty lists paths still uncommitted afterwards. Never auto-committed: they
	// carry forward onto the next story's branch, which is the right outcome, but
	// the user should be told.
	Dirty   []string `json:"dirty,omitempty"`
	Verdict Verdict  `json:"verdict"`
}

// verifyCommit decides whether the agent actually committed the work.
//
// The range check is the load-bearing part. chief's FindCommitForStory greps the
// entire history for the subject, so re-running an already-completed story
// matches an ancestor commit and reports success having done nothing. Confirming
// the match lies within headBefore..HEAD is what makes it trustworthy.
func verifyCommit(ctx context.Context, dir, headBefore, storyID, title string) CommitCheck {
	c := CommitCheck{HeadBefore: headBefore, Verdict: VerdictNoCommit}

	c.HeadAfter = revParseHead(ctx, dir)
	if c.HeadAfter == "" || c.HeadAfter == headBefore {
		c.Dirty = dirtyPaths(ctx, dir)
		return c
	}

	c.NewCommits = revList(ctx, dir, headBefore, c.HeadAfter)
	c.Dirty = dirtyPaths(ctx, dir)
	if len(c.NewCommits) == 0 {
		return c
	}

	// Only trust a subject match that is one of the commits this attempt made.
	if hash, err := git.FindCommitForStory(dir, storyID, title); err == nil && hash != "" {
		full := expandHash(ctx, dir, hash)
		for _, h := range c.NewCommits {
			if h == full {
				c.Matched = h
				c.Verdict = VerdictCommitted
				return c
			}
		}
	}

	c.Verdict = VerdictWrongSubject
	return c
}

// currentBranch reports the checked-out branch, or "" when HEAD is detached.
func currentBranch(ctx context.Context, dir string) string {
	out, err := gitOutput(ctx, dir, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func revParseHead(ctx context.Context, dir string) string {
	out, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func revList(ctx context.Context, dir, from, to string) []string {
	if from == "" {
		return nil
	}
	out, err := gitOutput(ctx, dir, "rev-list", from+".."+to)
	if err != nil {
		return nil
	}
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			hashes = append(hashes, line)
		}
	}
	return hashes
}

func expandHash(ctx context.Context, dir, hash string) string {
	out, err := gitOutput(ctx, dir, "rev-parse", hash)
	if err != nil {
		return hash
	}
	return strings.TrimSpace(out)
}

// dirtyPaths lists uncommitted changes, including untracked files.
func dirtyPaths(ctx context.Context, dir string) []string {
	out, err := gitOutput(ctx, dir, "status", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if len(line) > 3 {
			paths = append(paths, strings.TrimSpace(line[3:]))
		}
	}
	return paths
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func gitRun(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
