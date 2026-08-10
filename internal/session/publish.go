package session

// Publishing a finished PRD as one pull request.
//
// Nothing here runs during a run. A run's whole effect is local, and this is the
// separate, explicit action that turns those commits into something other people
// can see — which is the point: the user reads the result first, then decides.
//
// Three things shape this file:
//
//   - The branch is read off the sidecar, never recomputed. Under a single-branch
//     layout it is the run branch; under a branch per story it is the top of the
//     stack, which contains every branch below it because each was cut from the
//     one below. Either way, one branch holds all the PRD's commits.
//   - The driver is ghstack.Manual rather than whatever ghstack.Select returns.
//     `gh stack submit` publishes a whole stack, which is US-005's job; one pull
//     request against the trunk is not a stack operation, and Manual already
//     pushes, then updates an existing pull request rather than opening a second.
//   - The description is assembled from the bodies stored when each story was
//     verified. Recomposing it now would describe the status write that ticked
//     every acceptance-criteria box, not the work.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/ghstack"
)

// PublishRequest is one press of the pull-request control.
type PublishRequest struct {
	PRD string `json:"prd"`
	// Draft opens the pull request as a draft. It is chosen at publish time
	// rather than configured, because it is a statement about this particular
	// piece of work being ready.
	Draft bool `json:"draft"`
}

// PublishOffer is what the PRD header may offer for a PRD.
//
// Reason is filled in whether or not publishing is available: unavailable it is
// why the control is absent, and available it is nothing. The control is hidden
// rather than disabled — a button that can never work is worse than no button —
// so Reason exists for the refusal message, not for a tooltip on a dead control.
type PublishOffer struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Layout is how this PRD's commits are arranged, which decides what may be
	// offered. Empty when publishing is unavailable.
	Layout BranchLayout `json:"layout,omitempty"`
	// Stacked reports whether one pull request per story may be offered as well
	// as one for the whole PRD. Only a run that gave each story its own branch
	// produced a stack to publish.
	Stacked bool `json:"stacked"`
	// StackReason says why a stack cannot be published, when it cannot. Unlike
	// Reason it is shown: the whole control is present, one of its items is not,
	// and "the layout does not allow it" is the thing worth saying.
	StackReason string `json:"stackReason,omitempty"`
}

// PublishReport is the outcome of one publish.
type PublishReport struct {
	PRD    string `json:"prd"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	// Stories are the stories whose commits the pull request carries, in the
	// order the run reached them.
	Stories []string `json:"stories,omitempty"`
	PR      *PRRef   `json:"pr,omitempty"`
	// Updated reports that a pull request already existed for the branch and was
	// updated rather than a second one opened.
	Updated bool `json:"updated"`
}

// PublishOfferFor reports whether a PRD can be published, and why not.
//
// The three refusals are the ones that make publishing impossible rather than
// merely unwise: no git repository to push from, a project that has asked for git
// to be left alone, and a PRD with no commit to publish.
func (s *Session) PublishOfferFor(prdName string) PublishOffer {
	project := s.Project()
	if project == nil || !project.IsGitRepo {
		return PublishOffer{Reason: "this project is not a git repository, so there is nothing to open a pull request from"}
	}
	if s.LoopConfig().Git.Mode == config.GitModeOff {
		return PublishOffer{Reason: "git mode is off for this project, so Loop creates no branches and opens no pull requests"}
	}
	if !s.hasCommittedStory(prdName) {
		return PublishOffer{Reason: fmt.Sprintf("no story of %s has committed yet, so there is nothing to publish", prdName)}
	}
	layout := s.layoutFor(prdName)
	return PublishOffer{
		Available:   true,
		Layout:      layout,
		Stacked:     layout == LayoutBranchPerStory,
		StackReason: stackRefusal(layout),
	}
}

// stackRefusal says why a PRD has no stack to publish, and is empty when it has
// one. A layout that put every story on one branch produced a single sequence of
// commits: there is no branch per story to base a pull request on, and inventing
// one after the fact would mean rewriting the history the user has already read.
func stackRefusal(layout BranchLayout) string {
	if layout == LayoutBranchPerStory {
		return ""
	}
	return "this run put every story on one branch, so there is no stack to publish"
}

// PublishPullRequest pushes the branch holding a PRD's commits and opens one
// pull request against the trunk for all of them.
//
// Pressing again when a pull request already exists updates that one. Opening a
// second is an error on GitHub's side and noise on the reviewer's, and a
// duplicate pull request cannot be un-created — so what exists is consulted
// before anything is created.
func (s *Session) PublishPullRequest(ctx context.Context, req PublishRequest) (PublishReport, error) {
	plan, err := s.planPullRequest(req.PRD)
	if err != nil {
		return PublishReport{}, err
	}
	return s.submitPullRequest(ctx, plan, req.Draft)
}

// pullRequestPlan is everything the pull request is opened from, resolved before
// anything is pushed.
type pullRequestPlan struct {
	root    string
	prd     string
	branch  string
	base    string
	title   string
	body    string
	stories []string
}

// planPullRequest resolves what to publish, or says why it cannot be.
func (s *Session) planPullRequest(prdName string) (pullRequestPlan, error) {
	root, err := s.publishableProject(prdName)
	if err != nil {
		return pullRequestPlan{}, err
	}
	git, err := s.PRDGitFor(prdName)
	if err != nil {
		return pullRequestPlan{}, err
	}
	branch, err := publishableBranch(git, s.layoutFor(prdName))
	if err != nil {
		return pullRequestPlan{}, err
	}
	detail, err := s.PRD(prdName)
	if err != nil {
		return pullRequestPlan{}, err
	}

	included := includedStories(git, detail)
	return pullRequestPlan{
		root: root, prd: prdName, branch: branch, base: s.trunkBranch(),
		title:   pullRequestTitle(detail),
		body:    pullRequestBody(detail, included),
		stories: storyIDs(included),
	}, nil
}

// publishableProject refuses the cases where publishing must not proceed.
//
// A live run is refused rather than raced: publishing pushes the same branches
// the run is still moving, and a push half way through a story would publish a
// state the user has not seen and did not ask for.
func (s *Session) publishableProject(prdName string) (string, error) {
	root, err := s.requireProject()
	if err != nil {
		return "", err
	}
	if s.runIsLiveFor(prdName) {
		return "", fmt.Errorf(
			"%s is running. Stop or finish the run before publishing — publishing pushes the branches it is still committing to.",
			prdName)
	}
	if offer := s.PublishOfferFor(prdName); !offer.Available {
		return "", errors.New(offer.Reason)
	}
	return root, nil
}

// runIsLiveFor reports whether a run for this PRD is still going.
func (s *Session) runIsLiveFor(prdName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.runs {
		if r.PRD == prdName && isLive(r.State) {
			return true
		}
	}
	return false
}

// publishableBranch is the branch holding every commit the PRD produced.
//
// Under a single-branch layout that is the PRD's run branch. Under a branch per
// story it is the top of the stack: each branch was cut from the one below it, so
// the highest branch that has something to publish already contains all of them.
func publishableBranch(git PRDGitState, layout BranchLayout) (string, error) {
	if layout == LayoutBranchPerStory {
		return topOfStack(git)
	}
	if git.Branch == "" {
		return "", errors.New("no branch is recorded for this PRD, so there is nothing to push")
	}
	return git.Branch, nil
}

// topOfStack is the highest recorded branch that has a commit on it. A story that
// committed nothing sits at the same commit as the branch below it, so publishing
// its branch would open a pull request with no changes in it.
func topOfStack(git PRDGitState) (string, error) {
	branches := git.StoryBranches()
	for i := len(branches) - 1; i >= 0; i-- {
		if branches[i].HasSomethingToPublish() {
			return branches[i].Branch, nil
		}
	}
	return "", errors.New("no story of this PRD has a branch with a commit on it")
}

// publishedStory is one story the pull request carries, with the description
// composed when it was verified.
type publishedStory struct {
	story StorySnap
	body  string
}

// includedStories is the stories whose commits the pull request carries, in the
// order the run reached them.
//
// A story is included when it is done in the PRD — the record that survives a
// deleted sidecar — and its branch record does not say it committed nothing.
func includedStories(git PRDGitState, detail PRDDetail) []publishedStory {
	done := map[string]StorySnap{}
	for _, story := range detail.Stories {
		if story.Status == StatusDone {
			done[story.ID] = story
		}
	}

	out := make([]publishedStory, 0, len(done))
	for _, branch := range git.StoryBranches() {
		story, ok := done[branch.StoryID]
		if !ok || branch.NoCommit {
			continue
		}
		out = append(out, publishedStory{story: story, body: branch.PullRequestBody})
	}
	return out
}

func storyIDs(stories []publishedStory) []string {
	out := make([]string, 0, len(stories))
	for _, s := range stories {
		out = append(out, s.story.ID)
	}
	return out
}

// pullRequestTitle names the PRD, which is what the pull request is for. The
// document's own title where it has one, since that is what the user called it.
func pullRequestTitle(detail PRDDetail) string {
	if detail.Title != "" {
		return detail.Title
	}
	return detail.Name
}

// pullRequestBody describes the PRD and the stories the branch carries.
//
// Each story's section is the description stored when that story was verified,
// used verbatim. Recomposing it here would read the acceptance criteria after
// SetStoryStatus ticked every box, and present that write as evidence.
func pullRequestBody(detail PRDDetail, stories []publishedStory) string {
	var b strings.Builder
	if detail.Description != "" {
		b.WriteString(strings.TrimSpace(detail.Description))
		b.WriteString("\n\n")
	}

	b.WriteString("## Stories in this pull request\n\n")
	for _, s := range stories {
		fmt.Fprintf(&b, "- **%s** %s\n", s.story.ID, s.story.Title)
	}
	b.WriteString("\n")

	for _, s := range stories {
		writeStorySection(&b, s)
	}
	fmt.Fprintf(&b, "---\n%s · prepared by Loop\n", detail.Name)
	return b.String()
}

// writeStorySection renders one story, falling back to the PRD's own description
// of it for a story implemented before descriptions were stored.
func writeStorySection(b *strings.Builder, s publishedStory) {
	fmt.Fprintf(b, "### %s: %s\n\n", s.story.ID, s.story.Title)
	body := s.body
	if body == "" {
		body = s.story.Description
	}
	if body != "" {
		b.WriteString(strings.TrimSpace(body))
		b.WriteString("\n\n")
	}
}

// submitPullRequest pushes the branch and opens or updates its pull request,
// reporting progress as it goes.
func (s *Session) submitPullRequest(ctx context.Context, plan pullRequestPlan, draft bool) (PublishReport, error) {
	driver := ghstack.Manual{}
	report := PublishReport{PRD: plan.prd, Branch: plan.branch, Base: plan.base, Stories: plan.stories}

	target := plan.target()
	s.publishStep(target, "push", fmt.Sprintf("pushing %s to origin", plan.branch))
	// Asked before anything is created, so a second press is reported as the
	// update it is rather than looking like a fresh pull request.
	existing, found, _ := driver.Find(ctx, plan.root, plan.branch)
	report.Updated = found && existing.State == "OPEN"

	s.publishStep(target, "pr-create", publishingNote(report.Updated, plan.base))
	pr, err := driver.Submit(ctx, plan.spec(draft))
	if err != nil {
		s.publishFailure(target, err)
		return report, err
	}

	ref := prRefFrom(pr, s.now().UnixMilli())
	report.PR = &ref
	// Recorded before the event, so the link survives the process that opened it.
	_ = s.recordPullRequest(plan.prd, plan.branch, ref)
	s.publishSuccess(target, ref, publishedVerb(report.Updated))
	return report, nil
}

// publishedVerb is what publishing one pull request did.
func publishedVerb(updated bool) string {
	if updated {
		return "updated"
	}
	return "opened"
}

// publishTarget is what a publishing event is about: one branch of one PRD, and
// the story it belongs to where publishing works story by story.
type publishTarget struct {
	prd     string
	storyID string
	branch  string
	base    string
}

func (p pullRequestPlan) target() publishTarget {
	return publishTarget{prd: p.prd, branch: p.branch, base: p.base}
}

func (p pullRequestPlan) spec(draft bool) ghstack.Spec {
	return ghstack.Spec{
		Dir: p.root, Head: p.branch, Base: p.base,
		Title: p.title, Body: p.body, Draft: draft,
	}
}

func publishingNote(updated bool, base string) string {
	if updated {
		return "updating the existing pull request"
	}
	return "opening a pull request against " + base
}

// publishStep reports progress while publishing runs. Publishing takes the better
// part of a minute against a real remote, and a control that goes quiet for that
// long reads as broken.
func (s *Session) publishStep(t publishTarget, op, text string) {
	s.publish(Event{
		Kind: EvGit, PRD: t.prd, StoryID: t.storyID, Text: text,
		Git: &GitEvent{Op: op, Branch: t.branch, BaseBranch: t.base, State: "running"},
	})
}

// publishSuccess reports the pull request with its link, and tells the interface
// the PRD has changed so the link is picked up everywhere it is shown.
//
// The verb is what publishing did — opened it, updated it, or found it already
// open. "Already open" is a success: a retry that reports the pull request a
// previous attempt created has done exactly what was wanted.
func (s *Session) publishSuccess(t publishTarget, ref PRRef, verb string) {
	s.publish(Event{
		Kind: EvGit, PRD: t.prd, StoryID: t.storyID,
		Text: fmt.Sprintf("pull request #%d for %s was %s", ref.Number, t.branch, verb),
		Git: &GitEvent{
			Op: "pr-create", Branch: t.branch, BaseBranch: t.base,
			PRNumber: ref.Number, PRURL: ref.URL, State: "ok",
		},
	})
	s.publish(Event{Kind: EvPRDChanged, PRD: t.prd})
}

// publishFailure reports why publishing stopped. Fatal, unlike a run's git
// failures: publishing is one action the user took, and it either happened or it
// did not.
func (s *Session) publishFailure(t publishTarget, err error) {
	s.publish(Event{
		Kind: EvGit, PRD: t.prd, StoryID: t.storyID, Text: err.Error(),
		Git: &GitEvent{
			Op: "pr-create", Branch: t.branch, BaseBranch: t.base,
			State: "error", Fatal: true,
		},
		Error: errInfo(err, ""),
	})
}
