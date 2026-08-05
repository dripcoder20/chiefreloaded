package session

// Repository launchers: opening the current project on GitHub, or in a local
// editor.
//
// The policy lives here rather than in services so it is testable without a
// window and reachable from loopctl. Two rules shape it:
//
//   - Detection and launch must resolve the same installation. Reporting an
//     application as installed and then launching a different binary is worse
//     than reporting it missing, so both go through resolveApp.
//   - A path never becomes shell text. Every launch is an argv, so a repository
//     directory containing a quote or a semicolon is inert data.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalApp identifies an editor the repository can be opened in.
type LocalApp string

const (
	AppVSCode LocalApp = "vscode"
	AppClaude LocalApp = "claude"
	AppCursor LocalApp = "cursor"
	AppCodex  LocalApp = "codex"
)

// DisplayName is the human name to put in an alert, so an error never says
// "cursor is not installed" in a UI that calls it Cursor.
func (a LocalApp) DisplayName() string {
	switch a {
	case AppVSCode:
		return "VS Code"
	case AppClaude:
		return "Claude"
	case AppCursor:
		return "Cursor"
	case AppCodex:
		return "Codex"
	}
	return string(a)
}

// launchCandidates lists the executables that count as an installation of an
// app, most specific first. They are looked up on PATH; on macOS the CLI
// helpers ("code", "cursor") are what a user installs deliberately, which is a
// better signal than an .app bundle they may never have opened.
var launchCandidates = map[LocalApp][]string{
	AppVSCode: {"code", "codium", "code-insiders"},
	AppClaude: {"claude"},
	AppCursor: {"cursor"},
	AppCodex:  {"codex"},
}

// AppStatus reports whether one editor can be launched.
type AppStatus struct {
	App LocalApp `json:"app"`
	// Name is the display name, so the frontend never maps ids to labels itself.
	Name string `json:"name"`
	// Available reports whether an installation was resolved.
	Available bool `json:"available"`
	// Path is the resolved executable, empty when unavailable. Shown nowhere;
	// carried so a launch uses exactly what detection found.
	Path string `json:"path,omitempty"`
}

// lookPath is exec.LookPath, indirected so tests can describe a machine with a
// particular set of editors installed rather than depending on the real one.
var lookPath = exec.LookPath

// resolveApp finds the executable backing an app, if any.
func resolveApp(app LocalApp) AppStatus {
	status := AppStatus{App: app, Name: app.DisplayName()}
	for _, candidate := range launchCandidates[app] {
		path, err := lookPath(candidate)
		if err != nil {
			continue
		}
		status.Available = true
		status.Path = path
		return status
	}
	return status
}

// LocalApps reports the availability of every supported editor.
//
// Every app is listed whether or not it is installed: an entry that vanished
// when the application was missing would leave the user with no control to
// activate and therefore no explanation of what to install.
func (s *Session) LocalApps() []AppStatus {
	apps := []LocalApp{AppVSCode, AppClaude, AppCursor, AppCodex}
	out := make([]AppStatus, 0, len(apps))
	for _, app := range apps {
		out = append(out, resolveApp(app))
	}
	return out
}

// ErrAppNotInstalled reports that a launch target could not be found. It is
// distinct from a launch failure so the UI can tell the user to install the
// application rather than to look at why it refused to open.
type ErrAppNotInstalled struct{ Name string }

func (e *ErrAppNotInstalled) Error() string {
	return fmt.Sprintf("%s is not installed. Install it, then try again.", e.Name)
}

// OpenInApp opens the project root in a local editor.
//
// The repository root comes from the open project, never from the selected PRD:
// a PRD path points inside .chief/, and every launcher must agree on one
// directory.
func (s *Session) OpenInApp(ctx context.Context, app LocalApp) error {
	root, err := s.requireProject()
	if err != nil {
		return err
	}
	status := resolveApp(app)
	if !status.Available {
		return &ErrAppNotInstalled{Name: status.Name}
	}
	return launchApp(ctx, status, root, "this project")
}

// launchApp runs a resolved application against one path.
//
// subject names what failed to open in the user's terms — "this project", or a
// file name — because "code would not open /Users/…/.chief/prds/x/prd.md" makes
// the reader do the work of recognising their own PRD.
func launchApp(ctx context.Context, status AppStatus, target, subject string) error {
	// The path is a discrete argument, never interpolated into a command line.
	cmd := exec.CommandContext(ctx, status.Path, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s is installed but would not open %s. %s",
			status.Name, subject, firstLine(string(out)))
	}
	return nil
}

// ErrNoEditor reports that none of the editors Loop knows how to launch is
// installed. It is a signal to fall back to whatever the operating system
// considers the default for the file, not a failure worth showing anyone.
var ErrNoEditor = errors.New("no preferred editor is installed")

// OpenPRDFile opens a PRD's markdown in VS Code.
//
// A PRD is a document people edit, and handing markdown to the system default
// tends to open a previewer or whatever last claimed the extension — rarely the
// editor the project is being written in. VS Code is preferred when it is
// installed; ErrNoEditor asks the caller for the system default instead, which
// is what this used to do unconditionally.
func (s *Session) OpenPRDFile(ctx context.Context, name string) error {
	path, err := s.PRDPath(name)
	if err != nil {
		return err
	}
	editor := resolveApp(AppVSCode)
	if !editor.Available {
		return ErrNoEditor
	}
	return launchApp(ctx, editor, path, filepath.Base(path))
}

// ------------------------------------------------------------------ github ----

// GitHubURL returns the https page for the project's GitHub remote.
//
// Both remote forms in normal use are accepted — scp-style SSH
// (git@github.com:owner/repo.git) and https — and anything else is refused
// rather than guessed at, because opening the wrong page is worse than opening
// none.
func (s *Session) GitHubURL(ctx context.Context) (string, error) {
	root, err := s.requireProject()
	if err != nil {
		return "", err
	}
	remote, err := gitRemoteURL(ctx, root)
	if err != nil {
		return "", err
	}
	return githubWebURL(remote)
}

// gitRemoteURL reads the origin remote of a repository.
func gitRemoteURL(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf(
			"No GitHub repo is set up for this project. Add a remote named 'origin' to open it on GitHub.")
	}
	return strings.TrimSpace(string(out)), nil
}

// githubWebURL normalizes a git remote to a GitHub repository page.
//
// Credentials embedded in an https remote (https://user:token@github.com/...)
// are dropped: the URL is handed to a browser and may be logged.
func githubWebURL(remote string) (string, error) {
	slug, err := githubSlug(remote)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + slug, nil
}

// githubSlug extracts "owner/repo" from a supported remote form.
func githubSlug(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", fmt.Errorf(
			"No GitHub repo is set up for this project. Add a remote named 'origin' to open it on GitHub.")
	}
	if rest, ok := strings.CutPrefix(remote, "git@github.com:"); ok {
		return validateSlug(strings.TrimSuffix(rest, ".git"))
	}
	if rest, ok := cutGitHubHTTPS(remote); ok {
		return validateSlug(strings.TrimSuffix(rest, ".git"))
	}
	return "", fmt.Errorf(
		"This project's remote is not on GitHub (%s). Point 'origin' at a GitHub repo to open it there.", remote)
}

// cutGitHubHTTPS matches the https/ssh URL forms, tolerating an embedded
// credential and either host spelling.
func cutGitHubHTTPS(remote string) (string, bool) {
	for _, scheme := range []string{"https://", "http://", "ssh://git@", "git://"} {
		rest, ok := strings.CutPrefix(remote, scheme)
		if !ok {
			continue
		}
		// Drop any user[:password]@ prefix before matching the host.
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if path, ok := strings.CutPrefix(rest, "github.com/"); ok {
			return path, true
		}
		if path, ok := strings.CutPrefix(rest, "github.com:"); ok {
			return path, true
		}
	}
	return "", false
}

// firstLine trims a subprocess's output down to something worth showing. The
// rest is usually a stack trace or a usage banner, which tells a user nothing.
func firstLine(out string) string {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return "It reported no reason."
	}
	if at := strings.IndexByte(trimmed, '\n'); at >= 0 {
		trimmed = trimmed[:at]
	}
	return trimmed
}

// validateSlug rejects anything that is not exactly owner/repo, so a malformed
// remote cannot produce a URL that points somewhere unrelated.
func validateSlug(slug string) (string, error) {
	slug = strings.Trim(slug, "/")
	parts := strings.Split(slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf(
			"This project's GitHub remote does not name a repository. Check that 'origin' looks like owner/repo.")
	}
	return slug, nil
}
