package session

// Per-PRD workflow metadata: which agent implements it, and what its runs have
// done with branches.
//
// It lives in a sidecar (.chief/prds/<name>/loop.json) rather than in prd.md
// for one reason: prd.md is authored and rewritten by the agent. Asking it to
// preserve a block of Loop's bookkeeping across every edit would be fragile,
// and losing that block silently would change how the PRD is implemented.
//
// A PRD with no sidecar is entirely normal — every PRD written before this
// existed. Missing means defaults, never an error.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dripcoder/loop/internal/chief/config"
	"github.com/dripcoder/loop/internal/chief/prd"
)

// prdMetaFile is the sidecar's name inside a PRD's directory.
const prdMetaFile = "loop.json"

// prdMetaVersion is the sidecar's schema version. A file written by a newer
// Loop is refused rather than silently misread.
const prdMetaVersion = 1

// PRDWorkflow is the per-PRD workflow configuration.
type PRDWorkflow struct {
	// ImplementationAgent is the agent CLI chosen to execute the stories. Empty
	// means "use the configured default", which is deliberately not resolved at
	// save time: the default may legitimately change later.
	ImplementationAgent string `json:"implementationAgent,omitempty"`
}

// prdMetaEnvelope is the on-disk shape, versioned so the schema can move.
type prdMetaEnvelope struct {
	Version  int         `json:"version"`
	Workflow PRDWorkflow `json:"workflow"`
	// Git records what runs have actually done with branches and pull requests.
	Git PRDGitState `json:"git,omitempty"`
}

// PRDGitState is what a PRD's runs have put into git.
//
// Branch names look derivable — branchName is a pure function of the template,
// the PRD and the story — but only until the template changes or the user edits
// the suggested branch at the safety question. What a run actually used is the
// only thing that stays true, so it is recorded rather than recomputed.
type PRDGitState struct {
	// Layout is how the PRD's runs arrange their commits, as chosen at the start
	// of the first run. Empty means no run has chosen yet.
	Layout BranchLayout `json:"layout,omitempty"`
	// Branch is the PRD's own run branch, in per-PRD mode.
	Branch string `json:"branch,omitempty"`
	// Base is the branch that run branch was cut from.
	Base string `json:"base,omitempty"`
	// Branches is every story branch a run created, in the order it created them.
	Branches []StoryBranch `json:"branches,omitempty"`
	// Stories maps a story ID to the branch its commit landed on. Written by
	// versions of Loop from before Branches existed; read through StoryBranches,
	// and no longer written.
	Stories map[string]string `json:"stories,omitempty"`
	// PullRequests caches the last pull request seen for a branch, keyed by
	// branch name. GitHub remains the source of truth; this exists so the links
	// still render when gh is missing, unauthenticated or offline, and every
	// entry carries when it was last confirmed so a stale state is never
	// presented as a live one.
	PullRequests map[string]PRRef `json:"pullRequests,omitempty"`
}

// StoryBranch is what a run recorded about one story: the branch it created for
// it, the branch that was cut from, and the pull-request description composed
// when the story was verified.
//
// An ordered slice rather than the story-to-branch map it supersedes, because
// publishing a stack has to walk the branches from the bottom upwards and the
// creation order is the only record of what "below" means. A JSON object has no
// order and Go randomises map iteration, so the map could not express a stack at
// all.
//
// Branch is empty for a story under a single-branch layout, where no story owns a
// branch and the entry exists to hold the description alone.
type StoryBranch struct {
	StoryID string `json:"storyId"`
	Branch  string `json:"branch"`
	// Base is the branch this one was cut from — the story below it in the stack,
	// or the trunk at the bottom. Empty means unknown, which is what a sidecar
	// written before bases were recorded reads back as.
	Base string `json:"base,omitempty"`
	// NoCommit records that the story produced nothing to publish. Written after
	// the attempt rather than with the branch, because whether a story commits is
	// not knowable when its branch is created.
	NoCommit bool `json:"noCommit,omitempty"`
	// PullRequestBody is the description composed for this story at the moment it
	// was verified, before the status write ticked its acceptance criteria.
	// Publishing uses it verbatim: recomposing it later would describe the status
	// write rather than the work.
	PullRequestBody string `json:"pullRequestBody,omitempty"`
}

// HasBranch reports whether a branch of this story's own was recorded.
func (b StoryBranch) HasBranch() bool { return b.Branch != "" }

// BaseIsKnown reports whether this branch says what it was cut from.
func (b StoryBranch) BaseIsKnown() bool { return b.Base != "" }

// HasSomethingToPublish reports whether there is a branch here worth a pull
// request. A story that committed nothing has a branch identical to the one below
// it, so publishing it would open an empty pull request.
func (b StoryBranch) HasSomethingToPublish() bool { return b.HasBranch() && !b.NoCommit }

// StoryBranches is every story branch a run recorded, in the order it created
// them.
//
// A sidecar written before the order and the bases existed has only the
// story-to-branch map, which carries neither. It is read rather than refused,
// ordered by story ID because that is the closest a map gets to creation order,
// and every entry reports its base as unknown.
func (g PRDGitState) StoryBranches() []StoryBranch {
	if len(g.Branches) > 0 {
		return g.Branches
	}
	ids := make([]string, 0, len(g.Stories))
	for id := range g.Stories {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]StoryBranch, 0, len(ids))
	for _, id := range ids {
		out = append(out, StoryBranch{StoryID: id, Branch: g.Stories[id]})
	}
	return out
}

// BasesAreKnown reports whether every recorded branch says what it was cut from.
// It is false for a sidecar written before bases were recorded, which is the one
// case where a stack cannot be reconstructed from the record alone.
//
// A story with no branch of its own is not a link in any stack, so it is not
// asked what it was based on.
func (g PRDGitState) BasesAreKnown() bool {
	for _, b := range g.StoryBranches() {
		if b.HasBranch() && !b.BaseIsKnown() {
			return false
		}
	}
	return true
}

// BranchFor is the branch a story's commit landed on, empty when the story has
// none recorded.
func (g PRDGitState) BranchFor(storyID string) string {
	for _, b := range g.StoryBranches() {
		if b.StoryID == storyID {
			return b.Branch
		}
	}
	return ""
}

// recordRunBranch stores the PRD's own run branch, and the branch it was cut from
// when this run is the one creating it.
//
// Written as it happens rather than at the end of a run: a run that is stopped or
// crashes has still created the branch, and a UI that cannot name it leaves the
// user hunting through `git branch` for their own work.
//
// An empty base leaves what is on disk alone. Adopting an existing branch — the
// ordinary resumed-run case — says nothing about where that branch came from, and
// overwriting the earlier run's answer with this run's checkout would be a guess.
func (s *Session) recordRunBranch(prd, branch, base string) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		env.Git.Branch = branch
		if base != "" {
			env.Git.Base = base
		}
	})
}

// recordStoryBranch appends a story's branch and its base, in the order the run
// creates them.
//
// Re-recording a story keeps its position: a resumed run walks the same stories
// again, and reordering the record would rearrange a stack that git has already
// built.
func (s *Session) recordStoryBranch(prd string, entry StoryBranch) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		// Migrated on the first write rather than on read, so a PRD that ran under
		// an older Loop keeps the branches it recorded then instead of having them
		// hidden by this one entry.
		env.Git.Branches = env.Git.StoryBranches()
		env.Git.Branches = upsertStoryBranch(env.Git.Branches, entry)
	})
}

// upsertStoryBranch replaces a story's entry in place, or appends it.
func upsertStoryBranch(branches []StoryBranch, entry StoryBranch) []StoryBranch {
	for i := range branches {
		if branches[i].StoryID != entry.StoryID {
			continue
		}
		// The description is carried over rather than overwritten. A resumed run
		// re-records a story's branch as it walks past it, and it has no
		// description to offer at that point — it was composed at verification
		// time and cannot be composed again.
		if entry.PullRequestBody == "" {
			entry.PullRequestBody = branches[i].PullRequestBody
		}
		branches[i] = entry
		return branches
	}
	return append(branches, entry)
}

// recordStoryBody stores the pull-request description composed for a story.
//
// A story with no branch of its own still gets an entry: under a single-branch
// layout no story owns a branch, and the description composed at verification
// time is exactly what a later publish assembles the PRD's own description from.
func (s *Session) recordStoryBody(prd, storyID, body string) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		env.Git.Branches = env.Git.StoryBranches()
		for i := range env.Git.Branches {
			if env.Git.Branches[i].StoryID == storyID {
				env.Git.Branches[i].PullRequestBody = body
				return
			}
		}
		env.Git.Branches = append(env.Git.Branches,
			StoryBranch{StoryID: storyID, PullRequestBody: body})
	})
}

// recordNoCommit marks a story's branch as having produced nothing to publish.
func (s *Session) recordNoCommit(prd, storyID string) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		env.Git.Branches = env.Git.StoryBranches()
		for i := range env.Git.Branches {
			if env.Git.Branches[i].StoryID == storyID {
				env.Git.Branches[i].NoCommit = true
				return
			}
		}
	})
}

// recordLayout stores the branch layout a run chose, so later runs of the same
// PRD preselect it rather than asking again from scratch.
//
// Written as the run starts, before any branch exists: what the commits are
// arranged into has to be known to whatever publishes them later, and a run that
// dies after its first commit has still settled the question.
func (s *Session) recordLayout(prd string, layout BranchLayout) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		env.Git.Layout = layout
	})
}

// recordPullRequest caches a pull request against its head branch.
func (s *Session) recordPullRequest(prd, branch string, ref PRRef) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		if env.Git.PullRequests == nil {
			env.Git.PullRequests = map[string]PRRef{}
		}
		env.Git.PullRequests[branch] = ref
	})
}

// updatePRDMeta applies a change to a PRD's sidecar as a read-modify-write.
//
// A PRD whose directory has gone is not written to. savePRDMeta creates the
// directory it needs — which is right when a PRD is being created, and wrong
// here: these writes come from a run, which outlives the PRD being deleted
// under it by however long the agent takes to notice. Recreating the directory
// would resurrect a PRD the user deleted, as an empty husk holding nothing but
// a branch name.
func (s *Session) updatePRDMeta(prd string, change func(*prdMetaEnvelope)) error {
	path, err := s.prdMetaPath(prd)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return nil
	}
	env, err := loadPRDMeta(path)
	if err != nil {
		return err
	}
	change(&env)
	return savePRDMeta(path, env)
}

// PRDGitFor returns the branches and cached pull requests recorded for a PRD.
// A PRD that has never run has none, which is not an error.
func (s *Session) PRDGitFor(name string) (PRDGitState, error) {
	path, err := s.prdMetaPath(name)
	if err != nil {
		return PRDGitState{}, err
	}
	env, err := loadPRDMeta(path)
	if err != nil {
		return PRDGitState{}, err
	}
	return env.Git, nil
}

// prdMetaPath resolves a PRD's sidecar path.
//
// It deliberately does not require prd.md to exist. The workflow settings are
// chosen when a PRD is created, which is before the authoring agent has written
// anything — resolving through PRDPath there would fail, and the user's choices
// would be lost at the one moment they were actually made.
//
// An existing PRD is located through PRDPath so the legacy .chief/prd.md layout
// keeps its sidecar beside it. Otherwise the standard location is used.
func (s *Session) prdMetaPath(name string) (string, error) {
	root, err := s.requireProject()
	if err != nil {
		return "", err
	}
	if err := validPRDName(name); err != nil {
		return "", err
	}
	if prdPath, err := s.PRDPath(name); err == nil {
		return filepath.Join(filepath.Dir(prdPath), prdMetaFile), nil
	}
	return filepath.Join(root, chiefDir, prdsDir, name, prdMetaFile), nil
}

// validPRDName rejects anything that is not a plain name, so a caller cannot
// steer the sidecar out of the PRD directory with "..".
func validPRDName(name string) error {
	if name == "" {
		return fmt.Errorf("the PRD needs a name")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("PRD name %q: use only letters, digits, - and _", name)
		}
	}
	return nil
}

// loadPRDMeta reads a PRD's sidecar. A missing file yields the zero value and
// no error — that is every PRD written before this existed.
func loadPRDMeta(path string) (prdMetaEnvelope, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return prdMetaEnvelope{Version: prdMetaVersion}, nil
	}
	if err != nil {
		return prdMetaEnvelope{}, err
	}
	if len(raw) == 0 {
		return prdMetaEnvelope{Version: prdMetaVersion}, nil
	}

	var env prdMetaEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return prdMetaEnvelope{}, fmt.Errorf("read %s: %w", path, err)
	}
	if env.Version > prdMetaVersion {
		return prdMetaEnvelope{}, fmt.Errorf(
			"%s was written by a newer version of Loop (schema %d)", path, env.Version)
	}
	return env, nil
}

// savePRDMeta writes a sidecar atomically.
//
// Temp file plus rename, so an interrupted write cannot leave a truncated file
// that would then fail to parse and block the PRD from opening.
func savePRDMeta(path string, env prdMetaEnvelope) error {
	env.Version = prdMetaVersion
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}

	// The directory may not exist yet: workflow settings are chosen as a PRD is
	// created, and nothing has written to it at that point.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".loop-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Rename(tmpName, path)
}

// PRDWorkflowFor returns a PRD's workflow configuration.
func (s *Session) PRDWorkflowFor(name string) (PRDWorkflow, error) {
	path, err := s.prdMetaPath(name)
	if err != nil {
		return PRDWorkflow{}, err
	}
	env, err := loadPRDMeta(path)
	if err != nil {
		return PRDWorkflow{}, err
	}
	return env.Workflow, nil
}

// SavePRDWorkflow writes a PRD's workflow configuration, leaving what its runs
// have recorded about git intact.
func (s *Session) SavePRDWorkflow(name string, w PRDWorkflow) error {
	path, err := s.prdMetaPath(name)
	if err != nil {
		return err
	}
	env, err := loadPRDMeta(path)
	if err != nil {
		return err
	}
	env.Workflow = w
	if err := savePRDMeta(path, env); err != nil {
		return err
	}
	s.publish(Event{Kind: EvPRDChanged, PRD: name})
	return nil
}

// ResolveImplementationAgent decides which agent will implement a PRD, and
// refuses rather than silently substituting when the saved one has gone.
//
// A saved agent that is no longer installed is exactly the case where falling
// back to the default would be wrong: the PRD was configured to be implemented
// by a particular agent, and quietly running a different one is a worse outcome
// than saying so.
func (s *Session) ResolveImplementationAgent(prd string) (string, error) {
	workflow, err := s.PRDWorkflowFor(prd)
	if err != nil {
		return "", err
	}
	if workflow.ImplementationAgent == "" {
		return s.defaultImplementationAgent(), nil
	}
	if !s.agentIsAvailable(workflow.ImplementationAgent) {
		return "", fmt.Errorf(
			"%s is set to be implemented by %s, which is not installed. Pick another agent to start the run.",
			prd, workflow.ImplementationAgent)
	}
	return workflow.ImplementationAgent, nil
}

// defaultImplementationAgent is the configured implementation agent, falling
// back to the general default when no phase-specific one is set.
func (s *Session) defaultImplementationAgent() string {
	cfg := s.LoopConfig()
	return cfg.ImplementationProvider()
}

// DefaultAuthoringAgent is the configured authoring agent, falling back to the
// general default. Exposed so the New PRD selectors can show the resolved
// default rather than an ambiguous blank meaning "whatever is configured".
func (s *Session) DefaultAuthoringAgent() string {
	cfg := s.LoopConfig()
	return cfg.AuthoringProvider()
}

// DefaultImplementationAgent is the configured implementation agent.
func (s *Session) DefaultImplementationAgent() string { return s.defaultImplementationAgent() }

// AgentDefaults is the resolved per-phase agent configuration, so the New PRD
// selectors can show the real default rather than an ambiguous blank.
type AgentDefaults struct {
	Authoring      string `json:"authoring"`
	Implementation string `json:"implementation"`
}

// layoutFor is the branch layout a run for this PRD uses.
//
// The PRD's recorded choice wins: it was made with the work in front of the user,
// at the start of a run, which is the whole point of asking there. Nothing
// recorded falls back to the project default, and git mode `off` overrides both —
// a project that has asked for git to be left alone gets no branches whatever a
// previous run recorded.
//
// An unreadable sidecar falls back rather than failing the run: the run is what
// the user asked for, and one branch is the conservative answer.
func (s *Session) layoutFor(prd string) BranchLayout {
	if s.LoopConfig().Git.Mode == config.GitModeOff {
		return LayoutOneBranch
	}
	state, err := s.PRDGitFor(prd)
	if err == nil && state.Layout.isKnown() {
		return state.Layout
	}
	return s.defaultLayout()
}

// defaultLayout is what the layout question preselects for a PRD that has never
// recorded one.
//
// One branch, unless the project has explicitly been configured for per-story
// mode. A user who has not engaged with the question never has one PRD turn into
// nine pull requests, and a user who configured per-story mode is not silently
// overruled.
func (s *Session) defaultLayout() BranchLayout {
	if s.LoopConfig().PerStory() {
		return LayoutBranchPerStory
	}
	return LayoutOneBranch
}

// layoutIsSettled reports whether the PRD has a commit behind its recorded
// layout, which puts the choice beyond this run's reach.
//
// Changing the layout after a story has committed would leave the PRD's commits
// arranged one way and its record saying another, and nothing could publish that
// coherently.
func (s *Session) layoutIsSettled(prd string) bool {
	state, err := s.PRDGitFor(prd)
	if err != nil || !state.Layout.isKnown() {
		return false
	}
	return s.hasCommittedStory(prd)
}

// hasCommittedStory reports whether any story of the PRD is done.
//
// The PRD document is the record that survives everything — a restart, a
// different machine, a deleted sidecar — so a completed story is what "has
// committed" is read from.
func (s *Session) hasCommittedStory(prdName string) bool {
	path, err := s.PRDPath(prdName)
	if err != nil {
		return false
	}
	doc, err := prd.LoadPRD(path)
	if err != nil {
		return false
	}
	for _, story := range doc.UserStories {
		if story.Passes {
			return true
		}
	}
	return false
}

// agentIsAvailable reports whether an agent CLI was found on this machine.
func (s *Session) agentIsAvailable(name string) bool {
	for _, tool := range s.Environment().Agents {
		if tool.Name == name && tool.Available {
			return true
		}
	}
	return false
}
