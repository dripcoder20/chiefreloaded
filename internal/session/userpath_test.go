package session

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A GUI launch inherits launchd's PATH, so the directories agent CLIs actually
// live in are missing. Merging must add them without disturbing what is there.
func TestMergePathAddsMissingEntries(t *testing.T) {
	sep := string(os.PathListSeparator)
	current := strings.Join([]string{"/usr/bin", "/bin"}, sep)
	shell := strings.Join([]string{"/opt/homebrew/bin", "/usr/bin", "/usr/local/bin"}, sep)

	got := mergePath(current, shell)
	want := strings.Join([]string{"/usr/bin", "/bin", "/opt/homebrew/bin", "/usr/local/bin"}, sep)
	if got != want {
		t.Errorf("merged PATH =\n  %q\nwant\n  %q", got, want)
	}
}

// Whatever launched Loop may have set PATH deliberately; those entries keep
// their position, so the shell's copy cannot reorder them.
func TestMergePathPreservesExistingPrecedence(t *testing.T) {
	sep := string(os.PathListSeparator)
	current := strings.Join([]string{"/custom/first", "/usr/bin"}, sep)
	shell := strings.Join([]string{"/usr/bin", "/custom/first", "/opt/homebrew/bin"}, sep)

	got := mergePath(current, shell)
	if !strings.HasPrefix(got, "/custom/first"+sep+"/usr/bin") {
		t.Errorf("existing precedence was not preserved: %q", got)
	}
	if strings.Count(got, "/custom/first") != 1 {
		t.Errorf("duplicate entry in %q", got)
	}
}

func TestMergePathHandlesEmptyInputs(t *testing.T) {
	if got := mergePath("", "/opt/homebrew/bin"); got != "/opt/homebrew/bin" {
		t.Errorf("got %q", got)
	}
	if got := mergePath("/usr/bin", ""); got != "/usr/bin" {
		t.Errorf("got %q", got)
	}
}

// Startup files print banners and greetings; the value asked for is the last
// thing on stdout.
func TestLastLineIgnoresProfileChatter(t *testing.T) {
	out := "Welcome back!\nnvm: using v22\n/opt/homebrew/bin:/usr/bin\n"
	if got := lastLine(out); got != "/opt/homebrew/bin:/usr/bin" {
		t.Errorf("got %q", got)
	}
	if got := lastLine("   \n\n"); got != "" {
		t.Errorf("expected empty for blank output, got %q", got)
	}
}

// AdoptUserPath must never remove anything, whatever the shell reports.
func TestAdoptUserPathOnlyEverAdds(t *testing.T) {
	before := os.Getenv("PATH")
	t.Setenv("PATH", before)

	AdoptUserPath()

	for _, dir := range strings.Split(before, string(os.PathListSeparator)) {
		if dir != "" && !strings.Contains(os.Getenv("PATH"), dir) {
			t.Errorf("%s was dropped from PATH", dir)
		}
	}
}

// The behaviour that matters: from the minimal PATH a GUI launch actually gets,
// an agent CLI becomes findable again.
//
// Asserting on directories is the wrong test — a login shell inherits whatever
// PATH it is given and its output varies with it. What the app needs is simply
// that exec.LookPath finds the agent, which is the call that was failing.
//
// Skipped where no agent is installed, since there is then nothing to recover.
func TestAdoptUserPathMakesAgentsFindable(t *testing.T) {
	const launchdPath = "/usr/bin:/bin:/usr/sbin:/sbin"

	var installed string
	for _, name := range knownAgents {
		if _, err := exec.LookPath(name); err == nil {
			installed = name
			break
		}
	}
	if installed == "" {
		t.Skip("no agent CLI installed here")
	}

	t.Setenv("PATH", launchdPath)
	if _, err := exec.LookPath(installed); err == nil {
		t.Skipf("%s is on the launchd PATH already; nothing to recover", installed)
	}

	AdoptUserPath()

	if _, err := exec.LookPath(installed); err != nil {
		t.Errorf("%s is still not findable after recovering the user PATH: %v", installed, err)
	}
}
