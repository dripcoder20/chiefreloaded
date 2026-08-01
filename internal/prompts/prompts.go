// Package prompts resolves the instructions handed to an agent, letting a
// project override the ones chief ships with.
//
// The prompts that matter here are the authoring ones — the brief the agent gets
// when creating or editing a PRD. chief's are fixed at compile time, which means
// the shape of every PRD it produces is fixed too. A project that wants its own
// conventions, or that wants the agent to invoke a particular skill while it has
// the PRD in context, has nowhere to say so.
//
// Overrides are plain markdown files under .chief/prompts/, so they are editable
// anywhere, reviewable in a diff, and shared with the team through the
// repository. chief ignores the directory entirely.
package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minicodemonkey/chief/embed"
)

// Kind identifies a prompt Loop can override.
type Kind string

const (
	// KindNew is the brief for creating a PRD, used by `chief new`.
	KindNew Kind = "new"
	// KindEdit is the brief for revising an existing PRD.
	KindEdit Kind = "edit"
)

// Dir is where overrides live, relative to the project root.
const Dir = ".chief/prompts"

// Placeholders substituted before the prompt reaches the agent. Kept identical
// to chief's so an override can start as a copy of the built-in and stay
// recognisable.
const (
	VarPRDDir  = "{{PRD_DIR}}"
	VarContext = "{{CONTEXT}}"
)

// Prompt is a resolved prompt and where it came from.
type Prompt struct {
	Kind Kind   `json:"kind"`
	Body string `json:"body"`
	// Custom is true when the project has an override on disk. The UI needs to
	// distinguish "this is chief's default, shown for reference" from "this is
	// yours" — otherwise Reset has no meaning and neither does the editor.
	Custom bool `json:"custom"`
	// Path is where an override lives, or would live.
	Path string `json:"path"`
}

func (k Kind) valid() bool { return k == KindNew || k == KindEdit }

func (k Kind) filename() string { return string(k) + ".md" }

// PathFor returns where an override for kind lives in a project.
func PathFor(root string, kind Kind) string {
	return filepath.Join(root, Dir, kind.filename())
}

// Builtin returns chief's prompt for kind, placeholders intact.
//
// The placeholders are deliberately left unsubstituted: this is the text a user
// edits, and they need to see where the PRD directory and their context will be
// spliced in.
func Builtin(kind Kind) string {
	switch kind {
	case KindNew:
		return embed.GetInitPrompt(VarPRDDir, VarContext)
	case KindEdit:
		return embed.GetEditPrompt(VarPRDDir)
	default:
		return ""
	}
}

// Load returns the project's prompt for kind, falling back to chief's.
func Load(root string, kind Kind) (Prompt, error) {
	if !kind.valid() {
		return Prompt{}, fmt.Errorf("unknown prompt %q", kind)
	}

	p := Prompt{Kind: kind, Path: PathFor(root, kind)}

	body, err := os.ReadFile(p.Path)
	switch {
	case err == nil && strings.TrimSpace(string(body)) != "":
		p.Body = string(body)
		p.Custom = true
	case err == nil:
		// An override that is present but empty is almost certainly a mistake —
		// a cleared editor, a botched save — and sending an empty brief to the
		// agent would produce nonsense. Treat it as absent.
		p.Body = Builtin(kind)
	case os.IsNotExist(err):
		p.Body = Builtin(kind)
	default:
		return Prompt{}, fmt.Errorf("read %s: %w", p.Path, err)
	}

	return p, nil
}

// Save writes an override. A body that is empty or identical to the built-in
// removes the file instead, so "no override" has exactly one representation on
// disk rather than two that behave the same.
func Save(root string, kind Kind, body string) error {
	if !kind.valid() {
		return fmt.Errorf("unknown prompt %q", kind)
	}

	path := PathFor(root, kind)
	if strings.TrimSpace(body) == "" || normalise(body) == normalise(Builtin(kind)) {
		return Reset(root, kind)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", Dir, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Reset removes a project's override, restoring chief's prompt.
func Reset(root string, kind Kind) error {
	if !kind.valid() {
		return fmt.Errorf("unknown prompt %q", kind)
	}
	if err := os.Remove(PathFor(root, kind)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", PathFor(root, kind), err)
	}
	return nil
}

// Render substitutes the placeholders, producing the text sent to the agent.
func Render(body, prdDir, context string) string {
	if strings.TrimSpace(context) == "" {
		// chief's own wording for "the user did not say anything up front", so a
		// copied prompt behaves the same when the field is left blank.
		context = "The user has not provided additional context yet. Ask them what they want to build."
	}
	return strings.NewReplacer(
		VarPRDDir, prdDir,
		VarContext, context,
	).Replace(body)
}

// Resolve loads a project's prompt and renders it in one step.
func Resolve(root string, kind Kind, prdDir, context string) (string, error) {
	p, err := Load(root, kind)
	if err != nil {
		return "", err
	}
	return Render(p.Body, prdDir, context), nil
}

// normalise makes comparison insensitive to trailing whitespace, so a prompt
// that only differs by a stray newline is still recognised as the default.
func normalise(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
