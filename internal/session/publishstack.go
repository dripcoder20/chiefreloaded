package session

// Publishing a PRD as a stack of pull requests, one per story.
//
// The same explicit action as publishing one pull request, for the layout that
// can express more: a run that gave every story its own branch produced a stack,
// and reviewing it story by story is the reason the user chose that layout.
//
// What makes this a stack rather than a batch:
//
//   - The order is the record's order, bottom first. Each pull request is based
//     on the branch below it and the bottom one on the trunk, so every layer's
//     diff is that story's work alone.
//   - A base has to exist on the remote before anything can be based on it, which
//     is why the layers are published one at a time and a failure stops the ones
//     above rather than opening them against a branch GitHub cannot see.
//   - A story that committed nothing has a branch at the same commit as the one
//     below it. It contributes no pull request, and the story above is based on
//     the nearest branch below that does have one.
//   - Publishing again is a retry, not a second pass. Every layer is asked what
//     it already has before anything is pushed, so a stack that failed half way
//     is finished by publishing it again rather than by unpicking what exists.

import (
	"context"
	"errors"
	"fmt"

	"github.com/dripcoder/loop/internal/ghstack"
)

// StackReport is the outcome of publishing a stack: one entry per story, in the
// order the stack was published, whether or not that story produced a pull
// request.
//
// Per story rather than one verdict, because a stack partly fails in a way a
// single pull request cannot: the fourth layer can fail with three already open,
// and a report that said only "failed" would hide what exists.
type StackReport struct {
	PRD     string         `json:"prd"`
	Stories []StoryPublish `json:"stories,omitempty"`
	// Failed is why the pass stopped before it reached the top, empty when it did
	// not stop. It rides on the report rather than only being returned as an
	// error, because a call that merely rejects arrives at the interface as a
	// sentence with no per-story outcome attached — and which pull requests exist
	// is the thing a partial failure most has to say.
	Failed string `json:"failed,omitempty"`
}

// StoryPublish is what publishing did about one story.
type StoryPublish struct {
	StoryID string `json:"storyId"`
	Branch  string `json:"branch,omitempty"`
	// Base is the branch this pull request was actually opened against, which is
	// not always the one below it in the record — see Rebased.
	Base string `json:"base,omitempty"`
	PR   *PRRef `json:"pr,omitempty"`
	// AlreadyOpen reports that this story's pull request was open before this
	// attempt, so nothing was pushed and nothing was created for it. That is what
	// makes publishing again a retry: it adds what is missing and reports what is
	// already there rather than opening a second pull request for the same branch.
	AlreadyOpen bool `json:"alreadyOpen,omitempty"`
	// Skipped is why this story contributes no pull request. Empty when it does.
	Skipped string `json:"skipped,omitempty"`
	// Error is why this story's pull request could not be opened.
	Error string `json:"error,omitempty"`
	// Rebased reports that the branch below was not on the remote, so this pull
	// request was opened against the trunk instead and shows more than this
	// story's work. Reported rather than silently corrected: the diff a reviewer
	// sees is not the one the stack describes.
	Rebased bool `json:"rebased,omitempty"`
}

// HasPullRequest reports whether this story ended up with a pull request.
func (p StoryPublish) HasPullRequest() bool { return p.PR != nil }

// PublishStack pushes each of a PRD's story branches from the bottom of the
// stack upwards and opens a pull request for each.
//
// A layer that fails stops the layers above it: each is based on the branch
// below, and opening one against a base that is not on the remote produces a
// pull request whose diff is not the story's work. What was created before the
// failure stays created and is in the report.
func (s *Session) PublishStack(ctx context.Context, req PublishRequest) (StackReport, error) {
	plan, err := s.planStack(req)
	if err != nil {
		return StackReport{}, err
	}
	return s.submitStack(ctx, plan)
}

// stackPlan is the whole stack, resolved before anything is pushed.
type stackPlan struct {
	root   string
	prd    string
	trunk  string
	draft  bool
	layers []stackLayer
}

// stackLayer is one story's place in the stack.
type stackLayer struct {
	storyID string
	branch  string
	// base is the nearest branch below this one with a commit on it, or the trunk
	// at the bottom.
	base  string
	title string
	body  string
	// skipped is why this story contributes no pull request, empty when it does.
	skipped string
}

// planStack resolves what the stack is, or says why there is none to publish.
func (s *Session) planStack(req PublishRequest) (stackPlan, error) {
	root, err := s.publishableProject(req.PRD)
	if err != nil {
		return stackPlan{}, err
	}
	if reason := stackRefusal(s.layoutFor(req.PRD)); reason != "" {
		return stackPlan{}, errors.New(reason)
	}
	git, err := s.PRDGitFor(req.PRD)
	if err != nil {
		return stackPlan{}, err
	}
	detail, err := s.PRD(req.PRD)
	if err != nil {
		return stackPlan{}, err
	}

	trunk := s.trunkBranch()
	layers := stackLayers(git, detail, trunk)
	if !anyPublishable(layers) {
		return stackPlan{}, errors.New("no story of this PRD has a branch with a commit on it")
	}
	return stackPlan{root: root, prd: req.PRD, trunk: trunk, draft: req.Draft, layers: layers}, nil
}

// stackLayers turns the recorded branches into the stack to publish, in the order
// the run created them.
//
// Each layer's base is the nearest branch below it with a commit, rather than the
// base the record names. The two agree for a run that finished, and they differ
// exactly where it matters: a sidecar written before bases were recorded names
// none at all, and a base can name a branch that turned out to have nothing on
// it. What a pull request may be based on is a branch worth reviewing.
func stackLayers(git PRDGitState, detail PRDDetail, trunk string) []stackLayer {
	stories := storiesByID(detail)
	layers := make([]stackLayer, 0, len(git.StoryBranches()))
	base := trunk

	for _, recorded := range git.StoryBranches() {
		if reason := skipReason(recorded); reason != "" {
			layers = append(layers, stackLayer{
				storyID: recorded.StoryID, branch: recorded.Branch, skipped: reason,
			})
			continue
		}
		story := stories[recorded.StoryID]
		layers = append(layers, stackLayer{
			storyID: recorded.StoryID,
			branch:  recorded.Branch,
			base:    base,
			title:   storyPullRequestTitle(detail.Name, recorded.StoryID, story.Title),
			body:    storyPullRequestBody(recorded, story),
		})
		base = recorded.Branch
	}
	return layers
}

// skipReason says why a recorded branch produces no pull request.
func skipReason(recorded StoryBranch) string {
	if !recorded.HasBranch() {
		return "no branch was recorded for this story"
	}
	if recorded.NoCommit {
		return "the story produced no commit"
	}
	return ""
}

func storiesByID(detail PRDDetail) map[string]StorySnap {
	out := make(map[string]StorySnap, len(detail.Stories))
	for _, story := range detail.Stories {
		out[story.ID] = story
	}
	return out
}

// storyPullRequestTitle names the story and the PRD it belongs to, which is what
// a stack of otherwise similar pull requests has to be told apart by. It is the
// same convention the automatic per-story pull requests used before publishing
// became something the user does.
func storyPullRequestTitle(prdName, storyID, title string) string {
	if title == "" {
		return fmt.Sprintf("feat(%s): %s", prdName, storyID)
	}
	return fmt.Sprintf("feat(%s): %s %s", prdName, storyID, title)
}

// storyPullRequestBody is the description stored when the story was verified,
// used verbatim. A story implemented before descriptions were stored falls back
// to what the PRD says about it.
func storyPullRequestBody(recorded StoryBranch, story StorySnap) string {
	if recorded.PullRequestBody != "" {
		return recorded.PullRequestBody
	}
	return story.Description
}

func anyPublishable(layers []stackLayer) bool {
	for _, layer := range layers {
		if layer.skipped == "" {
			return true
		}
	}
	return false
}

// submitStack publishes the layers from the bottom upwards.
//
// A layer that fails stops the ones above it, and every story keeps its own entry
// in the report either way — created, skipped, failed, or blocked by the failure
// below. Which of those it is, is what tells the user what a retry has left to do.
func (s *Session) submitStack(ctx context.Context, plan stackPlan) (StackReport, error) {
	report := StackReport{PRD: plan.prd}
	var failed error

	for _, layer := range plan.layers {
		if failed != nil {
			report.Stories = append(report.Stories, layer.blocked())
			continue
		}
		if layer.skipped != "" {
			report.Stories = append(report.Stories, layer.notPublished(layer.skipped))
			continue
		}
		entry, err := s.submitLayer(ctx, plan, layer)
		report.Stories = append(report.Stories, entry)
		failed = err
	}
	if failed != nil {
		report.Failed = failed.Error()
	}
	return report, failed
}

// blocked is what a layer reports when the one below it never reached the remote.
// Attempting it anyway would open a pull request against a branch GitHub cannot
// see, which GitHub either refuses or answers by basing it on the trunk — a diff
// containing every story below, presented as this one's work.
func (l stackLayer) blocked() StoryPublish {
	if l.skipped != "" {
		return l.notPublished(l.skipped)
	}
	return l.notPublished("the branch below it was not published")
}

func (l stackLayer) notPublished(reason string) StoryPublish {
	return StoryPublish{StoryID: l.storyID, Branch: l.branch, Skipped: reason}
}

// submitLayer publishes one story's branch, unless its pull request is already
// open.
//
// What exists is asked for before anything is pushed. That is what makes a second
// press a retry rather than a second pass: the stories that failed are attempted,
// the ones that succeeded are reported back unchanged, and no branch can end up
// with two pull requests.
func (s *Session) submitLayer(ctx context.Context, plan stackPlan, layer stackLayer) (StoryPublish, error) {
	existing, found, _ := ghstack.Manual{}.Find(ctx, plan.root, layer.branch)
	if found && existing.State == "OPEN" {
		return s.reportOpenLayer(plan, layer, existing), nil
	}
	return s.openLayer(ctx, plan, layer)
}

// reportOpenLayer reports the pull request a previous attempt opened, without
// pushing or creating anything.
//
// The base is the one GitHub holds rather than the one this pass would have
// chosen: that is what the pull request is actually against, and a retry that
// reported the intended base would describe a stack nobody can see.
func (s *Session) reportOpenLayer(plan stackPlan, layer stackLayer, existing ghstack.PR) StoryPublish {
	base := existing.Base
	if base == "" {
		base = layer.base
	}
	ref := prRefFrom(existing, s.now().UnixMilli())
	// Re-recorded so a retry refreshes the link and its state even when it creates
	// nothing — the sidecar is what the story rows render from.
	_ = s.recordPullRequest(plan.prd, layer.branch, ref)

	target := publishTarget{prd: plan.prd, storyID: layer.storyID, branch: layer.branch, base: base}
	s.publishSuccess(target, ref, "already open")
	return StoryPublish{
		StoryID: layer.storyID, Branch: layer.branch, Base: base,
		PR: &ref, AlreadyOpen: true,
	}
}

// openLayer pushes one story's branch and opens its pull request.
//
// ghstack.Manual rather than whatever ghstack.Select returns, for the same reason
// as a single pull request: `gh stack submit` publishes every branch in one
// invocation and reports one outcome, which cannot say which story failed.
func (s *Session) openLayer(ctx context.Context, plan stackPlan, layer stackLayer) (StoryPublish, error) {
	base, rebased := resolveRemoteBase(ctx, plan.root, layer.base, plan.trunk)
	entry := StoryPublish{StoryID: layer.storyID, Branch: layer.branch, Base: base, Rebased: rebased}
	target := publishTarget{prd: plan.prd, storyID: layer.storyID, branch: layer.branch, base: base}

	s.publishStep(target, "push", fmt.Sprintf("pushing %s to origin", layer.branch))
	s.publishStep(target, "pr-create", "opening a pull request against "+base)
	pr, err := ghstack.Manual{}.Submit(ctx, ghstack.Spec{
		Dir: plan.root, Head: layer.branch, Base: base,
		Title: layer.title, Body: layer.body, Draft: plan.draft,
	})
	if err != nil {
		entry.Error = err.Error()
		s.publishFailure(target, err)
		return entry, fmt.Errorf("%s: %w", layer.storyID, err)
	}

	ref := prRefFrom(pr, s.now().UnixMilli())
	entry.PR = &ref
	// Recorded as it is created, so a failure above it leaves this link on disk
	// rather than only in a report the process may not outlive.
	_ = s.recordPullRequest(plan.prd, layer.branch, ref)
	s.publishSuccess(target, ref, "opened")
	return entry, nil
}
