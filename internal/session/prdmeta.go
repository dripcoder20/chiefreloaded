package session

// Per-PRD workflow metadata: which agent implements it, whether it stacks a
// pull request per story, and where its user stories are published as issues.
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
)

// prdMetaFile is the sidecar's name inside a PRD's directory.
const prdMetaFile = "loop.json"

// prdMetaVersion is the sidecar's schema version. A file written by a newer
// Loop is refused rather than silently misread.
const prdMetaVersion = 1

// IssueDestination is where a PRD's generated user stories are published.
type IssueDestination string

const (
	IssueNone   IssueDestination = ""
	IssueLinear IssueDestination = "linear"
	IssueGitHub IssueDestination = "github"
)

// PRDWorkflow is the per-PRD workflow configuration.
type PRDWorkflow struct {
	// ImplementationAgent is the agent CLI chosen to execute the stories. Empty
	// means "use the configured default", which is deliberately not resolved at
	// save time: the default may legitimately change later.
	ImplementationAgent string `json:"implementationAgent,omitempty"`
	// StackPerStory opens a stacked pull request per user story. Off unless a
	// saved preference says otherwise; it only configures the later run and
	// creates no branches or pull requests by itself.
	StackPerStory bool `json:"stackPerStory,omitempty"`
	// IssueDestination is the tracker stories are published to, if any.
	IssueDestination IssueDestination `json:"issueDestination,omitempty"`
}

// prdMetaEnvelope is the on-disk shape, versioned so the schema can move.
type prdMetaEnvelope struct {
	Version  int         `json:"version"`
	Workflow PRDWorkflow `json:"workflow"`
	// Issues records what has already been published, keyed by story ID. It is
	// the idempotency key: a retry after a partial failure consults this rather
	// than creating a second issue for a story that already has one.
	Issues map[string]IssueRef `json:"issues,omitempty"`
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
func (s *Session) updatePRDMeta(prd string, change func(*prdMetaEnvelope)) error {
	path, err := s.prdMetaPath(prd)
	if err != nil {
		return err
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

// IssueRef is an external issue created for one user story.
type IssueRef struct {
	Destination IssueDestination `json:"destination"`
	// Identifier is the human-readable id, e.g. DEV-123 or #42.
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
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

// SavePRDWorkflow writes a PRD's workflow configuration, leaving any recorded
// issue references intact.
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

// PublishedIssues returns the external issues already created for a PRD's
// stories, keyed by story ID.
func (s *Session) PublishedIssues(name string) (map[string]IssueRef, error) {
	path, err := s.prdMetaPath(name)
	if err != nil {
		return nil, err
	}
	env, err := loadPRDMeta(path)
	if err != nil {
		return nil, err
	}
	if env.Issues == nil {
		return map[string]IssueRef{}, nil
	}
	return env.Issues, nil
}

// recordIssue stores the issue created for one story.
//
// Written per story rather than in one batch at the end: if the application
// stops between creating an issue and finishing the run, what is on disk must
// already say that issue exists, or a retry creates a duplicate.
func (s *Session) recordIssue(prd, storyID string, ref IssueRef) error {
	path, err := s.prdMetaPath(prd)
	if err != nil {
		return err
	}
	env, err := loadPRDMeta(path)
	if err != nil {
		return err
	}
	if env.Issues == nil {
		env.Issues = map[string]IssueRef{}
	}
	env.Issues[storyID] = ref
	return savePRDMeta(path, env)
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

// stacksPerStory reports whether a run for this PRD should give each story its
// own branch and pull request.
//
// The PRD's own choice wins over the project default. That is the point of
// offering it when the PRD is created: one PRD in a project can be worth
// stacking without turning every PRD into a stack, and the reverse.
//
// An unreadable sidecar falls back to the project setting rather than failing
// the run — the run is the thing the user asked for, and this is a preference.
func (s *Session) stacksPerStory(prd string) bool {
	workflow, err := s.PRDWorkflowFor(prd)
	if err == nil && workflow.StackPerStory {
		return true
	}
	return s.LoopConfig().PerStory()
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
