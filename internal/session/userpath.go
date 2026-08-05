package session

// Recovering the user's PATH when Loop is launched as a desktop application.
//
// A GUI app started from Finder, the Dock or `open` inherits launchd's
// environment, not a shell's. On a typical macOS machine that means PATH is
// /usr/bin:/bin:/usr/sbin:/sbin — which contains none of the places agent CLIs
// are actually installed (/opt/homebrew/bin, /usr/local/bin, ~/.local/bin,
// a Node version manager's shims).
//
// The symptom is not subtle but it is misleading: the agent selectors come up
// empty and a run refuses to start with "claude is not installed", on a machine
// where `claude` works perfectly in a terminal. Everything else about the app
// looks fine, so the natural conclusion is that Loop is broken.
//
// The fix every desktop developer tool ends up with: ask the user's login shell
// what PATH it would have produced, and merge that in. It runs the user's own
// shell and their own startup files — the same ones a terminal would — so this
// grants no access they did not already have.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// loginShellTimeout bounds the shell startup. A slow profile must delay the
// window, not prevent it opening — an app that hangs on launch is worse than
// one that cannot find an agent and says so.
const loginShellTimeout = 3 * time.Second

// AdoptUserPath merges the login shell's PATH into this process's.
//
// It only ever adds: entries already present keep their position, so a PATH
// deliberately set by whoever launched Loop still wins. Safe to call more than
// once, and a no-op on platforms where GUI launches inherit a usable
// environment already.
func AdoptUserPath() {
	if runtime.GOOS == "windows" {
		return
	}
	shellPath, err := loginShellPath()
	if err != nil || shellPath == "" {
		return
	}
	if merged := mergePath(os.Getenv("PATH"), shellPath); merged != "" {
		_ = os.Setenv("PATH", merged)
	}
}

// loginShellPath asks the user's login shell to print its PATH.
//
// -l runs the login startup files, which is where PATH is usually assembled;
// -i is needed too because several shells only source the interactive rc file
// (.zshrc, .bashrc) where users in practice put their exports.
func loginShellPath() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginShellTimeout)
	defer cancel()

	// The command is a fixed literal; nothing user-supplied is interpolated.
	cmd := exec.CommandContext(ctx, shell, "-lic", "printf %s \"$PATH\"")
	// A profile that prints banners writes to stderr; only stdout is read.
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(lastLine(string(out))), nil
}

// lastLine takes the final non-empty line of output.
//
// Startup files print things — version notices, fortune, a greeting — and some
// of it lands on stdout ahead of the value we asked for.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// mergePath appends entries from extra that current does not already contain,
// preserving the order of both and dropping duplicates.
func mergePath(current, extra string) string {
	seen := map[string]bool{}
	var out []string

	add := func(list string) {
		for _, dir := range filepath.SplitList(list) {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	add(current)
	add(extra)

	return strings.Join(out, string(os.PathListSeparator))
}
