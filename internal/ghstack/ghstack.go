// Package ghstack creates stacked pull requests.
//
// Two drivers, one interface:
//
//   - GH drives the `gh stack` extension, which GitHub shipped to public
//     preview in July 2026. It produces a native GitHub Stack, so merging one
//     layer automatically retargets and rebases the layers above it. That
//     behaviour is why this package is thin — the hard part is GitHub's now.
//   - Manual chains PRs with `gh pr create --draft --base <previous>`. The same
//     branch topology and the same review experience per PR, minus the native
//     stack object and its auto-retargeting.
//
// Manual is not vestigial. `gh stack` is a preview feature days old at time of
// writing and may not be installed; the fallback has to stay tested.
//
// Verified against real GitHub: three stories produce three draft pull requests
// stacked bottom-up, and merging the bottom one retargets the next onto the
// trunk automatically. Two constraints found doing that, both of which will bite
// anyone extending this:
//
//   - `gh pr merge` refuses a stacked pull request outright: GitHub requires the
//     asynchronous merge API. Use `gh stack merge`, not `gh pr merge`.
//   - `gh pr edit` fails on gh 2.68 regardless of arguments, because it queries
//     projectCards, a field removed with Projects (classic). Titles and bodies go
//     through the REST API instead — see editPR.
package ghstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// commandTimeout bounds any single gh or git invocation. Network calls to
// GitHub can hang, and a hung push must not wedge a run that is otherwise
// making progress.
const commandTimeout = 2 * time.Minute

// PR is a pull request.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Base   string `json:"base,omitempty"`
	Head   string `json:"head,omitempty"`
}

// Spec describes the pull request to open for one story.
type Spec struct {
	Dir   string // worktree or repository directory
	Head  string // the story's branch
	Base  string // the branch below it in the stack, or the trunk
	Title string
	Body  string
	Draft bool
}

// Driver creates and inspects stacked pull requests.
type Driver interface {
	// Name identifies the driver in events and diagnostics.
	Name() string
	// Init prepares a stack rooted at base with first as its bottom branch.
	Init(ctx context.Context, dir, base, first string) error
	// Submit pushes head and opens or updates its pull request.
	Submit(ctx context.Context, s Spec) (PR, error)
	// AddBranch creates the next branch at HEAD and checks it out.
	AddBranch(ctx context.Context, dir, branch string) error
	// Find returns the existing pull request for a branch, if any.
	Find(ctx context.Context, dir, head string) (PR, bool, error)
}

// Available reports whether the `gh stack` extension is installed.
func Available(ctx context.Context) bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	out, err := run(ctx, "", "gh", "extension", "list")
	return err == nil && strings.Contains(out, "github/gh-stack")
}

// Select returns the driver matching the requested mode. "auto" prefers
// gh-stack and falls back to manual.
func Select(ctx context.Context, requested string) Driver {
	switch requested {
	case "manual":
		return Manual{}
	case "gh-stack":
		return GH{}
	default:
		if Available(ctx) {
			return GH{}
		}
		return Manual{}
	}
}

// ---------------------------------------------------------------- gh stack --

// GH drives the `gh stack` extension.
type GH struct{}

func (GH) Name() string { return "gh-stack" }

// Init adopts or creates the bottom branch of a stack.
//
// `gh stack init` is idempotent for our purposes: given explicit branch names it
// adopts existing branches and creates missing ones, so re-running it on a
// resumed run is safe.
func (GH) Init(ctx context.Context, dir, base, first string) error {
	_, err := run(ctx, dir, "gh", "stack", "init", "--base", base, first)
	return err
}

// Submit pushes every branch in the stack and opens pull requests for those that
// lack one.
//
// `--auto` is what makes this usable unattended: it skips the interactive editor
// and — importantly — creates new PRs as drafts unless `--open` is passed. The
// cost is auto-generated titles, so the title and body we actually want are
// applied immediately afterwards with `gh pr edit`.
func (d GH) Submit(ctx context.Context, s Spec) (PR, error) {
	args := []string{"stack", "submit", "--auto"}
	if !s.Draft {
		args = append(args, "--open")
	}
	if _, err := run(ctx, s.Dir, "gh", args...); err != nil {
		return PR{}, fmt.Errorf("gh stack submit: %w", err)
	}

	pr, found, err := d.Find(ctx, s.Dir, s.Head)
	if err != nil {
		return PR{}, err
	}
	if !found {
		return PR{}, fmt.Errorf("gh stack submit reported success but no pull request exists for %s", s.Head)
	}

	if err := editPR(ctx, s.Dir, pr.Number, s.Title, s.Body); err != nil {
		// The PR exists and is stacked correctly; only its prose is wrong. That
		// is not worth failing the story over.
		return pr, fmt.Errorf("pull request #%d created, but updating its title and body failed: %w", pr.Number, err)
	}
	pr.Base, pr.Head = s.Base, s.Head
	return pr, nil
}

// Merge merges a pull request and everything below it in the stack.
//
// Through `gh stack merge` because plain `gh pr merge` is rejected for stacked
// pull requests — GitHub requires the asynchronous merge API, and the extension
// is what speaks it.
func (GH) Merge(ctx context.Context, dir string, number int) error {
	_, err := run(ctx, dir, "gh", "stack", "merge", strconv.Itoa(number), "--yes")
	return err
}

// AddBranch puts the next story's branch on top of the stack.
func (GH) AddBranch(ctx context.Context, dir, branch string) error {
	_, err := run(ctx, dir, "gh", "stack", "add", branch)
	return err
}

func (GH) Find(ctx context.Context, dir, head string) (PR, bool, error) {
	return findPR(ctx, dir, head)
}

// View returns the stack as gh sees it. Used to reconcile our own state against
// the truth after a crash.
func (GH) View(ctx context.Context, dir string) (map[string]any, error) {
	out, err := run(ctx, dir, "gh", "stack", "view", "--json")
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, fmt.Errorf("parse gh stack view --json: %w", err)
	}
	return v, nil
}

// ------------------------------------------------------------------ manual --

// Manual chains pull requests without the extension.
type Manual struct{}

func (Manual) Name() string { return "manual" }

// Init puts the checkout on the bottom branch, creating it if it is not there.
//
// There is no stack object to create without the extension, but the branch the
// first story commits on still has to exist, and with no extension nothing else
// is going to create it — the session's caller relies on Init for exactly that.
// An existing branch is adopted rather than refused, which is the ordinary
// resumed-run case.
func (Manual) Init(ctx context.Context, dir, base, first string) error {
	if localBranchExists(ctx, dir, first) {
		_, err := run(ctx, dir, "git", "checkout", first)
		return err
	}
	_, err := run(ctx, dir, "git", "checkout", "-b", first)
	return err
}

// localBranchExists reports whether a branch exists in this repository.
func localBranchExists(ctx context.Context, dir, branch string) bool {
	_, err := run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func (Manual) Submit(ctx context.Context, s Spec) (PR, error) {
	if _, err := run(ctx, s.Dir, "git", "push", "-u", "origin", s.Head); err != nil {
		return PR{}, fmt.Errorf("push %s: %w", s.Head, err)
	}

	// Opening a second pull request for the same branch is an error on GitHub's
	// side and noise on the reviewer's, so update rather than duplicate.
	if existing, found, err := findPR(ctx, s.Dir, s.Head); err == nil && found {
		if existing.State != "OPEN" {
			return existing, fmt.Errorf("pull request #%d for %s is %s; not reopening",
				existing.Number, s.Head, strings.ToLower(existing.State))
		}
		if err := editPR(ctx, s.Dir, existing.Number, s.Title, s.Body); err != nil {
			return existing, err
		}
		existing.Base, existing.Head = s.Base, s.Head
		return existing, nil
	}

	args := []string{"pr", "create", "--base", s.Base, "--head", s.Head, "--title", s.Title, "--body-file", "-"}
	if s.Draft {
		args = append(args, "--draft")
	}
	// The body goes over stdin, never argv: it contains newlines and backticks
	// and can exceed ARG_MAX on a long story.
	if _, err := runStdin(ctx, s.Dir, s.Body, "gh", args...); err != nil {
		return PR{}, fmt.Errorf("gh pr create: %w", err)
	}

	pr, found, err := findPR(ctx, s.Dir, s.Head)
	if err != nil || !found {
		return PR{}, fmt.Errorf("pull request for %s was created but could not be read back", s.Head)
	}
	pr.Base, pr.Head = s.Base, s.Head
	return pr, nil
}

func (Manual) AddBranch(ctx context.Context, dir, branch string) error {
	_, err := run(ctx, dir, "git", "checkout", "-b", branch)
	return err
}

func (Manual) Find(ctx context.Context, dir, head string) (PR, bool, error) {
	return findPR(ctx, dir, head)
}

// ------------------------------------------------------------------ shared --

func findPR(ctx context.Context, dir, head string) (PR, bool, error) {
	out, err := run(ctx, dir, "gh", "pr", "list",
		"--head", head, "--state", "all", "--limit", "1",
		"--json", "number,url,state,isDraft,baseRefName,headRefName")
	if err != nil {
		return PR{}, false, fmt.Errorf("gh pr list: %w", err)
	}

	var rows []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		State       string `json:"state"`
		IsDraft     bool   `json:"isDraft"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return PR{}, false, fmt.Errorf("parse gh pr list: %w", err)
	}
	if len(rows) == 0 {
		return PR{}, false, nil
	}

	r := rows[0]
	return PR{
		Number: r.Number, URL: r.URL, State: r.State, Draft: r.IsDraft,
		Base: r.BaseRefName, Head: r.HeadRefName,
	}, true, nil
}

// ListPRs returns every pull request in the repository, newest first.
//
// One call rather than one per branch. A PRD with twenty stories would
// otherwise mean twenty round trips to GitHub every time the interface wanted
// to show a link, and gh is slow enough that the difference is the difference
// between doing this on selection and not doing it at all. Matching a head
// branch to a story is local work.
func ListPRs(ctx context.Context, dir string, limit int) ([]PR, error) {
	out, err := run(ctx, dir, "gh", "pr", "list",
		"--state", "all", "--limit", strconv.Itoa(limit),
		"--json", "number,url,state,isDraft,baseRefName,headRefName")
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}

	var rows []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		State       string `json:"state"`
		IsDraft     bool   `json:"isDraft"`
		BaseRefName string `json:"baseRefName"`
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}

	prs := make([]PR, 0, len(rows))
	for _, r := range rows {
		prs = append(prs, PR{
			Number: r.Number, URL: r.URL, State: r.State, Draft: r.IsDraft,
			Base: r.BaseRefName, Head: r.HeadRefName,
		})
	}
	return prs, nil
}

// editPR sets a pull request's title and body.
//
// Through the REST API rather than `gh pr edit`, which unconditionally queries
// projectCards — a field GitHub removed with Projects (classic). On gh 2.68 that
// makes every `gh pr edit` fail regardless of the arguments, so the PR would be
// opened correctly and then left with an auto-generated title. PATCH on the
// pulls endpoint touches none of that and behaves the same across gh versions.
func editPR(ctx context.Context, dir string, number int, title, body string) error {
	payload, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		return err
	}
	// The body arrives on stdin: it contains newlines and backticks and can
	// exceed ARG_MAX on a long story.
	_, err = runStdin(ctx, dir, string(payload), "gh", "api",
		"--method", "PATCH",
		"repos/{owner}/{repo}/pulls/"+strconv.Itoa(number),
		"--input", "-")
	return err
}

// RemoteBranchExists reports whether a branch is on the remote. The base of a
// stacked pull request has to exist there, so this is how a cascade of push
// failures is detected before it turns into a confusing gh error.
func RemoteBranchExists(ctx context.Context, dir, branch string) bool {
	_, err := run(ctx, dir, "git", "ls-remote", "--exit-code", "--heads", "origin", branch)
	return err == nil
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return runStdin(ctx, dir, "", name, args...)
}

func runStdin(ctx context.Context, dir, stdin, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return out.String(), fmt.Errorf("%s: %s", err, firstLines(msg, 3))
		}
		return out.String(), err
	}
	return out.String(), nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "; ")
}
