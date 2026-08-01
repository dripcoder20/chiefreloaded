package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dripcoder/loop/internal/chief/git"
	"github.com/dripcoder/loop/internal/chief/prd"
)

const (
	chiefDir  = ".chief"
	prdsDir   = "prds"
	prdFile   = "prd.md"
	legacyPRD = "prd.md" // pre-0.7 layout: .chief/prd.md, treated as PRD "main"
)

// openProject inspects root and returns what Loop needs to know about it.
//
// It never fails on a directory that merely lacks a .chief/ — that is the
// first-run case, and reporting it as an error would force the caller to
// distinguish "broken" from "new".
func openProject(root string) (Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Project{}, fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Project{}, fmt.Errorf("open project: %w", err)
	}
	if !info.IsDir() {
		return Project{}, fmt.Errorf("open project: %s is not a directory", abs)
	}

	p := Project{
		Root: abs,
		Name: filepath.Base(abs),
	}

	if st, err := os.Stat(filepath.Join(abs, chiefDir)); err == nil && st.IsDir() {
		p.HasChiefDir = true
	}

	if !git.IsGitRepo(abs) {
		return p, nil
	}
	p.IsGitRepo = true
	p.ChiefIgnored = git.IsChiefIgnored(abs)

	// Both of these shell out and can legitimately fail — a repo with no commits
	// has no current branch, and one with no remote has no discoverable default.
	// Neither is a reason to refuse to open the project.
	if branch, err := git.GetCurrentBranch(abs); err == nil {
		p.Branch = branch
	}
	if base, err := git.GetDefaultBranch(abs); err == nil {
		p.DefaultBase = base
	}

	return p, nil
}

// discoverPRDs finds every PRD under the project root.
//
// Two layouts are supported: the current .chief/prds/<name>/prd.md, and the
// pre-0.7 .chief/prd.md, which is surfaced as a PRD named "main" and flagged
// Legacy. Users who have been running chief for a while will have the old one,
// and silently ignoring their work would look like data loss.
//
// A PRD whose markdown fails to parse is still returned, with ParseError set.
// Hiding it would leave no way to open and repair the file.
func discoverPRDs(root string) []PRDSummary {
	var out []PRDSummary

	base := filepath.Join(root, chiefDir, prdsDir)
	entries, err := os.ReadDir(base)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(base, e.Name(), prdFile)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			out = append(out, summarisePRD(root, e.Name(), path, false))
		}
	}

	// Only fall back to the legacy location when no PRD called "main" already
	// exists in the current layout, so a migrated project does not show both.
	if !containsName(out, "main") {
		legacy := filepath.Join(root, chiefDir, legacyPRD)
		if _, err := os.Stat(legacy); err == nil {
			out = append(out, summarisePRD(root, "main", legacy, true))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func containsName(list []PRDSummary, name string) bool {
	for _, p := range list {
		if p.Name == name {
			return true
		}
	}
	return false
}

func summarisePRD(root, name, path string, legacy bool) PRDSummary {
	s := PRDSummary{
		Name:   name,
		Path:   path,
		Title:  name,
		Legacy: legacy,
		State:  StateIdle,
	}

	doc, err := prd.LoadPRD(path)
	if err != nil {
		s.ParseError = err.Error()
		return s
	}

	if doc.Project != "" {
		s.Title = doc.Project
	}
	s.Total = len(doc.UserStories)
	for _, st := range doc.UserStories {
		switch {
		case st.Passes:
			s.Completed++
		case st.InProgress:
			s.InProgress++
		}
	}

	// A worktree is only reported when it actually exists on disk. chief's
	// convention is .chief/worktrees/<prd>, but the directory is created lazily,
	// so its absence is normal rather than an error.
	wt := git.WorktreePathForPRD(root, name)
	if git.IsWorktree(wt) {
		s.Worktree = wt
		if branch, err := git.GetCurrentBranch(wt); err == nil {
			s.Branch = branch
		}
	}

	return s
}

// loadPRDDetail parses a PRD in full and merges in its progress journal.
func loadPRDDetail(root, name string) (PRDDetail, error) {
	summaries := discoverPRDs(root)
	var summary *PRDSummary
	for i := range summaries {
		if summaries[i].Name == name {
			summary = &summaries[i]
			break
		}
	}
	if summary == nil {
		return PRDDetail{}, fmt.Errorf("no PRD named %q under %s", name, root)
	}
	if summary.ParseError != "" {
		return PRDDetail{PRDSummary: *summary}, nil
	}

	doc, err := prd.LoadPRD(summary.Path)
	if err != nil {
		return PRDDetail{}, fmt.Errorf("load PRD %q: %w", name, err)
	}

	detail := PRDDetail{
		PRDSummary:  *summary,
		Description: doc.Description,
		Stories:     make([]StorySnap, 0, len(doc.UserStories)),
	}
	for _, st := range doc.UserStories {
		detail.Stories = append(detail.Stories, toStorySnap(st))
	}
	return detail, nil
}

// toStorySnap flattens chief's Passes/InProgress pair into a single status.
func toStorySnap(st prd.UserStory) StorySnap {
	status := StatusTodo
	switch {
	case st.Passes:
		status = StatusDone
	case st.InProgress:
		status = StatusInProgress
	}

	return StorySnap{
		ID:          st.ID,
		Title:       st.Title,
		Description: st.Description,
		Criteria:    st.AcceptanceCriteria,
		Priority:    st.Priority,
		Status:      status,
		// chief's SetStoryStatus(id, "done") ticks every acceptance-criteria box
		// as a side effect of the status write, so once a story is done its
		// checklist records that write and nothing else. Say so rather than let
		// the UI present it as verified.
		CriteriaAreAuthoritative: status != StatusDone,
	}
}

// progressFor returns the progress.md journal entries for a PRD, keyed by story
// ID. A missing journal is normal — it does not exist until the first story runs.
func progressFor(prdPath string) map[string][]prd.ProgressEntry {
	entries, err := prd.ParseProgress(prd.ProgressPath(prdPath))
	if err != nil {
		return nil
	}
	return entries
}
