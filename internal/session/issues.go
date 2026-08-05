package session

// Publishing a PRD's user stories as external issues, and writing the resulting
// references back into the PRD.
//
// The ordering is deliberate and load-bearing: the PRD is saved first, then
// issues are created, then each reference is written back. A tracker outage
// therefore cannot cost the user the generated PRD, which is the expensive
// artefact — it only costs them the links, which a retry restores.
//
// Every create is guarded by what is already recorded in the PRD's sidecar, so
// retrying after a partial failure creates issues only for the stories that do
// not have one. Duplicated issues cannot be un-created, which is why this is
// checked rather than assumed.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dripcoder/loop/internal/tracker"
)

// AgentDefaults is the resolved per-phase agent configuration, so the New PRD
// selectors can show the real default rather than an ambiguous blank.
type AgentDefaults struct {
	Authoring      string `json:"authoring"`
	Implementation string `json:"implementation"`
}

// DestinationStatus reports whether one tracker can be published to.
type DestinationStatus struct {
	Destination IssueDestination `json:"destination"`
	Name        string           `json:"name"`
	Available   bool             `json:"available"`
	// Reason says what configuration is missing when Available is false, so the
	// UI can explain the setup instead of only disabling the control.
	Reason string `json:"reason,omitempty"`
}

// StoryPublishResult is what happened to one story.
type StoryPublishResult struct {
	StoryID string `json:"storyId"`
	// Ref is the issue for this story: the one just created, or the one that
	// already existed.
	Ref *IssueRef `json:"ref,omitempty"`
	// Error is why this story has no issue. The other stories are unaffected.
	Error string `json:"error,omitempty"`
	// Skipped is true when an issue already existed and none was created.
	Skipped bool `json:"skipped"`
}

// PublishReport is the outcome of one publishing pass.
type PublishReport struct {
	PRD     string               `json:"prd"`
	Results []StoryPublishResult `json:"results"`
	// Failed lists the stories that still have no issue, which is exactly the
	// set a retry should attempt.
	Failed []string `json:"failed,omitempty"`
}

// trackerFor returns the client for a destination.
//
// Injectable through Options so tests publish against a fake rather than
// creating real issues in somebody's tracker.
func (s *Session) trackerFor(dest IssueDestination) (tracker.Client, error) {
	if s.opts.Tracker != nil {
		return s.opts.Tracker(dest)
	}
	switch dest {
	case IssueGitHub:
		return tracker.GitHub{}, nil
	case IssueLinear:
		return tracker.Linear{}, nil
	default:
		return nil, fmt.Errorf("no issue destination is selected")
	}
}

// IssueDestinations reports which trackers this project can publish to.
//
// An unavailable destination is still listed: the UI disables it in place and
// shows the reason, which is more use than an option that silently is not there.
func (s *Session) IssueDestinations(ctx context.Context) []DestinationStatus {
	root, err := s.requireProject()
	if err != nil {
		root = ""
	}

	out := make([]DestinationStatus, 0, 2)
	for _, dest := range []IssueDestination{IssueLinear, IssueGitHub} {
		client, err := s.trackerFor(dest)
		if err != nil {
			continue
		}
		status := DestinationStatus{Destination: dest, Name: client.Name()}
		status.Available, status.Reason = client.Available(ctx, root)
		out = append(out, status)
	}
	return out
}

// PublishIssues creates one issue per user story and records each reference.
//
// It publishes only stories that do not already have one, so calling it again
// after a partial failure is a retry rather than a second publish. Each
// reference is written to the sidecar and into prd.md as it is created: if the
// application stops mid-way, what is on disk already says which issues exist.
func (s *Session) PublishIssues(ctx context.Context, prdName string) (PublishReport, error) {
	workflow, err := s.PRDWorkflowFor(prdName)
	if err != nil {
		return PublishReport{}, err
	}
	if workflow.IssueDestination == IssueNone {
		return PublishReport{}, fmt.Errorf("%q is not configured to publish issues", prdName)
	}

	detail, err := s.PRD(prdName)
	if err != nil {
		return PublishReport{}, err
	}
	client, err := s.trackerFor(workflow.IssueDestination)
	if err != nil {
		return PublishReport{}, err
	}
	root, err := s.requireProject()
	if err != nil {
		return PublishReport{}, err
	}
	existing, err := s.PublishedIssues(prdName)
	if err != nil {
		return PublishReport{}, err
	}

	report := PublishReport{PRD: prdName}
	for _, story := range detail.Stories {
		result := s.publishStory(ctx, publishRequest{
			client: client, root: root, prd: prdName,
			dest: workflow.IssueDestination, story: story, existing: existing,
		})
		report.Results = append(report.Results, result)
		if result.Ref == nil {
			report.Failed = append(report.Failed, story.ID)
		}
	}
	s.publish(Event{Kind: EvPRDChanged, PRD: prdName})
	return report, nil
}

// publishRequest carries what publishing one story needs, keeping the function
// below within the project's parameter limit.
type publishRequest struct {
	client   tracker.Client
	root     string
	prd      string
	dest     IssueDestination
	story    StorySnap
	existing map[string]IssueRef
}

// publishStory creates the issue for one story, or reports why it could not.
func (s *Session) publishStory(ctx context.Context, req publishRequest) StoryPublishResult {
	// An issue recorded for this story is the idempotency check. It is consulted
	// per story rather than once up front so a retry cannot race its own writes.
	if ref, ok := req.existing[req.story.ID]; ok {
		cp := ref
		return StoryPublishResult{StoryID: req.story.ID, Ref: &cp, Skipped: true}
	}

	created, err := req.client.Create(ctx, req.root, tracker.Issue{
		Title: fmt.Sprintf("%s: %s", req.story.ID, req.story.Title),
		Body:  issueBody(req.story),
	})
	if err != nil {
		return StoryPublishResult{StoryID: req.story.ID, Error: err.Error()}
	}

	ref := IssueRef{Destination: req.dest, Identifier: created.Identifier, URL: created.URL}
	// Recorded before the markdown is touched: the sidecar is the idempotency
	// key, and an issue that exists but is not recorded would be created twice.
	if err := s.recordIssue(req.prd, req.story.ID, ref); err != nil {
		return StoryPublishResult{StoryID: req.story.ID, Error: err.Error()}
	}
	if err := s.writeIssueReference(req.prd, req.story.ID, ref); err != nil {
		return StoryPublishResult{StoryID: req.story.ID, Ref: &ref, Error: err.Error()}
	}
	return StoryPublishResult{StoryID: req.story.ID, Ref: &ref}
}

// issueBody renders a story's description and acceptance criteria.
func issueBody(story StorySnap) string {
	var b strings.Builder
	if story.Description != "" {
		b.WriteString(story.Description)
		b.WriteString("\n\n")
	}
	if len(story.Criteria) > 0 {
		b.WriteString("## Acceptance Criteria\n\n")
		for _, c := range story.Criteria {
			fmt.Fprintf(&b, "- [ ] %s\n", c)
		}
	}
	return strings.TrimSpace(b.String())
}

// issueReferenceLine is the stable metadata line written into a story.
func issueReferenceLine(ref IssueRef) string {
	return fmt.Sprintf("**External Issue:** [%s](%s)", ref.Identifier, ref.URL)
}

// writeIssueReference inserts the reference into a story in prd.md.
//
// It is placed below the description and above the acceptance criteria, which
// is where the PRD says it belongs, and mapped by story ID rather than by
// position — tracker responses can complete out of order, and writing a
// reference onto the wrong story is worse than not writing one.
//
// The whole file is rewritten atomically so an interrupted write cannot
// truncate a PRD that took an agent a long conversation to produce.
func (s *Session) writeIssueReference(prdName, storyID string, ref IssueRef) error {
	path, err := s.PRDPath(prdName)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated, ok := insertIssueReference(string(raw), storyID, ref)
	if !ok {
		return fmt.Errorf("story %s was not found in %s", storyID, prdName)
	}
	return writeFileAtomic(path, []byte(updated))
}

// insertIssueReference returns the document with the reference written into the
// named story, replacing any line already there so a re-publish does not stack
// duplicates. The second return is false when the story is not in the document.
func insertIssueReference(doc, storyID string, ref IssueRef) (string, bool) {
	lines := strings.Split(doc, "\n")
	start := storyHeadingIndex(lines, storyID)
	if start < 0 {
		return doc, false
	}
	end := nextStoryHeadingIndex(lines, start)

	body := lines[start:end]
	body = removeIssueReference(body)
	at := insertionPoint(body)

	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:start]...)
	out = append(out, body[:at]...)
	out = append(out, issueReferenceLine(ref), "")
	out = append(out, body[at:]...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), true
}

// storyHeadingIndex finds the "### US-00N:" heading for a story.
func storyHeadingIndex(lines []string, storyID string) int {
	prefix := "### " + storyID
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix+":") || trimmed == prefix {
			return i
		}
	}
	return -1
}

// nextStoryHeadingIndex finds where a story's block ends: the next story, the
// next top-level section, or the end of the document.
func nextStoryHeadingIndex(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
			return i
		}
	}
	return len(lines)
}

// removeIssueReference strips an existing reference line and the blank line
// that follows it, so re-publishing replaces rather than accumulates.
func removeIssueReference(body []string) []string {
	out := make([]string, 0, len(body))
	skipBlank := false
	for _, line := range body {
		if strings.HasPrefix(strings.TrimSpace(line), "**External Issue:**") {
			skipBlank = true
			continue
		}
		if skipBlank && strings.TrimSpace(line) == "" {
			skipBlank = false
			continue
		}
		skipBlank = false
		out = append(out, line)
	}
	return out
}

// insertionPoint is the index within a story's block to write the reference at:
// just before the acceptance criteria, which puts it under the description.
func insertionPoint(body []string) int {
	for i, line := range body {
		if strings.HasPrefix(strings.TrimSpace(line), "**Acceptance Criteria:**") {
			return i
		}
	}
	return len(body)
}

// writeFileAtomic replaces a file's contents via a temp file and a rename, so an
// interrupted write leaves the original intact rather than a truncated file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".prd-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
