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
	// Stories maps a story ID to the branch its commit landed on.
	Stories map[string]string `json:"stories,omitempty"`
	// PullRequests caches the last pull request seen for a branch, keyed by
	// branch name. GitHub remains the source of truth; this exists so the links
	// still render when gh is missing, unauthenticated or offline, and every
	// entry carries when it was last confirmed so a stale state is never
	// presented as a live one.
	PullRequests map[string]PRRef `json:"pullRequests,omitempty"`
}

// recordBranch stores the branch a run used, for the PRD as a whole when storyID
// is empty, or for one story.
//
// Written as it happens rather than at the end of a run: a run that is stopped
// or crashes has still created the branch, and a UI that cannot name it leaves
// the user hunting through `git branch` for their own work.
func (s *Session) recordBranch(prd, storyID, branch string) error {
	return s.updatePRDMeta(prd, func(env *prdMetaEnvelope) {
		if storyID == "" {
			env.Git.Branch = branch
			return
		}
		if env.Git.Stories == nil {
			env.Git.Stories = map[string]string{}
		}
		env.Git.Stories[storyID] = branch
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
