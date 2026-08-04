package session

import (
	"errors"
	"os/exec"
	"testing"
)

// openTestProject opens a fresh git repository and returns its root. The
// launchers resolve their path from the open project, so every launcher test
// needs one.
func openTestProject(t *testing.T, s *Session) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	if _, err := s.OpenProject(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	return root
}

// withInstalled describes a machine on which exactly the named executables are
// installed, restoring the real lookup when the test ends.
func withInstalled(t *testing.T, names ...string) {
	t.Helper()
	installed := make(map[string]bool, len(names))
	for _, n := range names {
		installed[n] = true
	}
	original := lookPath
	lookPath = func(file string) (string, error) {
		if installed[file] {
			return "/usr/local/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = original })
}

func TestGitHubWebURL_supportedRemoteForms(t *testing.T) {
	const want = "https://github.com/dripcoder/loop"
	for name, remote := range map[string]string{
		"ssh scp-style":       "git@github.com:dripcoder/loop.git",
		"ssh without suffix":  "git@github.com:dripcoder/loop",
		"https":               "https://github.com/dripcoder/loop.git",
		"https without .git":  "https://github.com/dripcoder/loop",
		"https with trailing": "https://github.com/dripcoder/loop/",
		"ssh:// url":          "ssh://git@github.com/dripcoder/loop.git",
		"git:// url":          "git://github.com/dripcoder/loop.git",
		"surrounding space":   "  git@github.com:dripcoder/loop.git\n",
	} {
		got, err := githubWebURL(remote)
		if err != nil {
			t.Errorf("%s (%q): unexpected error: %v", name, remote, err)
			continue
		}
		if got != want {
			t.Errorf("%s (%q): got %q, want %q", name, remote, got, want)
		}
	}
}

// A credential embedded in the remote must not survive into a URL that is handed
// to a browser and may be logged.
func TestGitHubWebURL_stripsEmbeddedCredentials(t *testing.T) {
	got, err := githubWebURL("https://someone:ghp_secrettoken@github.com/dripcoder/loop.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://github.com/dripcoder/loop" {
		t.Errorf("got %q, want the credential stripped", got)
	}
}

func TestGitHubWebURL_rejectsUnsupportedRemotes(t *testing.T) {
	for name, remote := range map[string]string{
		"empty":            "",
		"non-github host":  "git@gitlab.com:dripcoder/loop.git",
		"non-github https": "https://bitbucket.org/dripcoder/loop.git",
		"malformed":        "not a url at all",
		"no repo":          "git@github.com:dripcoder",
		"local path":       "/Users/someone/code/loop",
	} {
		if got, err := githubWebURL(remote); err == nil {
			t.Errorf("%s (%q): expected an error, got %q", name, remote, got)
		}
	}
}

func TestResolveApp_reportsInstalledAndMissing(t *testing.T) {
	withInstalled(t, "code", "cursor")

	if s := resolveApp(AppVSCode); !s.Available || s.Path != "/usr/local/bin/code" {
		t.Errorf("VS Code: got %+v, want available at the resolved path", s)
	}
	if s := resolveApp(AppCursor); !s.Available {
		t.Errorf("Cursor: got %+v, want available", s)
	}
	if s := resolveApp(AppClaude); s.Available {
		t.Errorf("Claude: got %+v, want unavailable", s)
	}
	if s := resolveApp(AppCodex); s.Available || s.Path != "" {
		t.Errorf("Codex: got %+v, want unavailable with no path", s)
	}
}

// An unavailable application must still be listed, or the user has no control to
// activate and therefore never sees the alert explaining what to install.
func TestLocalApps_listsEveryAppInstalledOrNot(t *testing.T) {
	withInstalled(t, "code")
	s := newTestSession(t)

	apps := s.LocalApps()
	if len(apps) != 4 {
		t.Fatalf("got %d apps, want all 4 listed", len(apps))
	}
	byApp := map[LocalApp]AppStatus{}
	for _, a := range apps {
		byApp[a.App] = a
	}
	for _, want := range []LocalApp{AppVSCode, AppClaude, AppCursor, AppCodex} {
		if _, ok := byApp[want]; !ok {
			t.Errorf("%s is missing from the list", want)
		}
	}
	if !byApp[AppVSCode].Available {
		t.Error("VS Code should be reported available")
	}
	if byApp[AppClaude].Available {
		t.Error("Claude should be reported unavailable")
	}
	if got := byApp[AppVSCode].Name; got != "VS Code" {
		t.Errorf("display name = %q, want %q", got, "VS Code")
	}
}

// A missing application is a distinct error from a launch failure, so the UI can
// say "install it" rather than "it would not open".
func TestOpenInApp_missingAppIsADistinctError(t *testing.T) {
	withInstalled(t)
	s := newTestSession(t)
	openTestProject(t, s)

	err := s.OpenInApp(t.Context(), AppCursor)
	var notInstalled *ErrAppNotInstalled
	if !errors.As(err, &notInstalled) {
		t.Fatalf("got %v, want an ErrAppNotInstalled", err)
	}
	if notInstalled.Name != "Cursor" {
		t.Errorf("error names %q, want Cursor", notInstalled.Name)
	}
}

// The repository path must reach the editor as a discrete argument, so a
// directory containing shell metacharacters is inert data.
func TestOpenInApp_passesTheRepositoryRootAsAnArgument(t *testing.T) {
	s := newTestSession(t)
	root := openTestProject(t, s)

	// "true" ignores its arguments and exits 0, which is enough to prove the
	// launch was attempted with an argv rather than a shell string: a directory
	// with a quote in it would break a shell command line and not this.
	original := lookPath
	lookPath = func(string) (string, error) { return exec.LookPath("true") }
	t.Cleanup(func() { lookPath = original })

	if err := s.OpenInApp(t.Context(), AppVSCode); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root == "" {
		t.Fatal("expected a project root")
	}
}

func TestGitHubURL_requiresAnOpenProject(t *testing.T) {
	s := newTestSession(t)
	if _, err := s.GitHubURL(t.Context()); err == nil {
		t.Error("expected an error with no project open")
	}
}

// A project with no origin remote must produce an actionable message rather than
// a URL pointing somewhere unrelated.
func TestGitHubURL_noRemoteIsAnActionableError(t *testing.T) {
	s := newTestSession(t)
	openTestProject(t, s)

	if url, err := s.GitHubURL(t.Context()); err == nil {
		t.Errorf("expected an error for a repository with no origin, got %q", url)
	}
}
