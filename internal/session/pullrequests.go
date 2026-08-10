package session

// Finding the pull request that belongs to a branch.
//
// GitHub is the source of truth, not Loop. A pull request opened by hand, or
// merged, or closed while the application was not running, is still the real
// state of that branch — so the answer is re-derived from `gh` rather than kept
// in a ledger Loop maintains. Two consequences shape this file:
//
//   - One query per refresh, never one per branch. gh takes the better part of
//     a second, and a PRD with twenty stories would otherwise make this too
//     slow to do on selection.
//   - The last successful answer is cached on disk. Without gh — not installed,
//     not authenticated, no network — the links still render, marked stale and
//     dated, because a link you can follow beats no link at all. The cached
//     state is never presented as current.

import (
	"context"

	"github.com/dripcoder/loop/internal/ghstack"
)

// prListLimit bounds the single gh query. Repositories accumulate thousands of
// pull requests and the branches Loop cares about are recent ones; asking for
// everything would trade a slow query for matches nobody is looking for.
const prListLimit = 200

// PullRequestSet is the pull request state for one PRD, keyed by branch.
type PullRequestSet struct {
	// ByBranch holds one pull request per head branch that has one.
	ByBranch map[string]PRRef `json:"byBranch"`
	// CheckedAt is when GitHub was last reached, in unix milliseconds. Zero
	// means it never has been and everything here came off the cache.
	CheckedAt int64 `json:"checkedAt,omitempty"`
	// Unavailable explains why the live query could not run, when it could not.
	// Empty on success. It is a status to display, not an error to raise: a
	// project with no GitHub remote is a normal project.
	Unavailable string `json:"unavailable,omitempty"`
}

// RefreshPullRequests re-reads pull request state for a PRD's branches.
//
// It never fails on account of GitHub. A repository with no remote, a machine
// without gh, an expired token — each leaves the cached answer in place and is
// reported through Unavailable, because none of them is a reason to stop showing
// the user which branch their work is on.
func (s *Session) RefreshPullRequests(ctx context.Context, prd string) (PullRequestSet, error) {
	root, err := s.requireProject()
	if err != nil {
		return PullRequestSet{}, err
	}
	git, err := s.PRDGitFor(prd)
	if err != nil {
		return PullRequestSet{}, err
	}

	branches := branchesOf(git)
	if len(branches) == 0 {
		return PullRequestSet{}, nil
	}

	live, err := ghstack.ListPRs(ctx, root, prListLimit)
	if err != nil {
		return cachedSet(git, branches, err.Error()), nil
	}

	now := s.now().UnixMilli()
	set := PullRequestSet{ByBranch: map[string]PRRef{}, CheckedAt: now}
	for _, pr := range live {
		if !branches[pr.Head] {
			continue
		}
		ref := prRefFrom(pr, now)
		set.ByBranch[pr.Head] = ref
		// Cached as it is matched, so the next start already has it. A sidecar
		// that will not accept the write costs freshness, never correctness.
		_ = s.recordPullRequest(prd, pr.Head, ref)
	}
	return set, nil
}

// PullRequestsFor returns the cached pull request state without touching the
// network, for callers rendering before a refresh has landed.
func (s *Session) PullRequestsFor(prd string) (PullRequestSet, error) {
	git, err := s.PRDGitFor(prd)
	if err != nil {
		return PullRequestSet{}, err
	}
	return cachedSet(git, branchesOf(git), ""), nil
}

// cachedSet builds the answer from what is on disk. Every entry keeps the
// CheckedAt it was cached with, so nothing here can pass for a fresh reading.
func cachedSet(git PRDGitState, branches map[string]bool, unavailable string) PullRequestSet {
	set := PullRequestSet{ByBranch: map[string]PRRef{}, Unavailable: unavailable}
	for branch := range branches {
		ref, ok := git.PullRequests[branch]
		if !ok {
			continue
		}
		set.ByBranch[branch] = ref
	}
	return set
}

// branchesOf is every branch a PRD has claimed: its own, and one per story.
func branchesOf(git PRDGitState) map[string]bool {
	branches := map[string]bool{}
	if git.Branch != "" {
		branches[git.Branch] = true
	}
	for _, story := range git.StoryBranches() {
		if story.HasBranch() {
			branches[story.Branch] = true
		}
	}
	return branches
}

// prRefFrom converts a driver pull request into the read model's shape.
func prRefFrom(pr ghstack.PR, checkedAt int64) PRRef {
	return PRRef{
		Number: pr.Number, URL: pr.URL, State: pr.State, Draft: pr.Draft,
		Base: pr.Base, Head: pr.Head, CheckedAt: checkedAt,
	}
}
