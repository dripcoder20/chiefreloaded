package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFallsBackToTheBuiltin(t *testing.T) {
	root := t.TempDir()

	p, err := Load(root, KindNew)
	if err != nil {
		t.Fatal(err)
	}
	if p.Custom {
		t.Error("a project with no override should not report a custom prompt")
	}
	if p.Body != Builtin(KindNew) {
		t.Error("body should be chief's built-in prompt")
	}
	if p.Path == "" {
		t.Error("Path should say where an override would go, even when absent")
	}
}

// The editable text has to keep its placeholders, or the user cannot see where
// the PRD directory and their context get spliced in.
func TestBuiltinKeepsItsPlaceholders(t *testing.T) {
	body := Builtin(KindNew)
	for _, v := range []string{VarPRDDir, VarContext} {
		if !strings.Contains(body, v) {
			t.Errorf("built-in prompt is missing %s", v)
		}
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	custom := "# My prompt\n\nWrite the PRD to {{PRD_DIR}}/prd.md, then run /grilling.\n"

	if err := Save(root, KindNew, custom); err != nil {
		t.Fatal(err)
	}

	p, err := Load(root, KindNew)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Custom {
		t.Error("Custom should be true once an override exists")
	}
	if p.Body != custom {
		t.Errorf("body = %q, want it byte-identical", p.Body)
	}

	// A plain markdown file in the repository, so it diffs and reviews normally.
	if _, err := os.Stat(filepath.Join(root, ".chief", "prompts", "new.md")); err != nil {
		t.Errorf("override should be a file at .chief/prompts/new.md: %v", err)
	}
}

// "No override" should have exactly one representation on disk. Two states that
// behave identically is how a Reset button stops making sense.
func TestSavingTheBuiltinRemovesTheOverride(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, KindNew, "something custom {{PRD_DIR}}"); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, KindNew, Builtin(KindNew)); err != nil {
		t.Fatal(err)
	}

	if p, _ := Load(root, KindNew); p.Custom {
		t.Error("saving the built-in verbatim should clear the override, not store a copy")
	}
}

func TestTrailingWhitespaceDoesNotCountAsCustom(t *testing.T) {
	root := t.TempDir()
	// What an editor does to a file on save.
	if err := Save(root, KindNew, Builtin(KindNew)+"\n\n"); err != nil {
		t.Fatal(err)
	}
	if p, _ := Load(root, KindNew); p.Custom {
		t.Error("a stray trailing newline should not make the prompt count as customised")
	}
}

// A cleared editor is a mistake, not an instruction to send the agent nothing.
func TestEmptyOverrideIsIgnored(t *testing.T) {
	root := t.TempDir()
	path := PathFor(root, KindNew)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("   \n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := Load(root, KindNew)
	if err != nil {
		t.Fatal(err)
	}
	if p.Body != Builtin(KindNew) {
		t.Error("an empty override should fall back rather than send an empty brief")
	}
}

func TestSaveEmptyClearsTheOverride(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, KindNew, "custom"); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, KindNew, ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := Load(root, KindNew); p.Custom {
		t.Error("saving an empty body should clear the override")
	}
}

func TestResetIsSafeWhenNothingIsThere(t *testing.T) {
	if err := Reset(t.TempDir(), KindNew); err != nil {
		t.Errorf("resetting with no override should not fail: %v", err)
	}
}

func TestRenderSubstitutesPlaceholders(t *testing.T) {
	got := Render("dir={{PRD_DIR}} ctx={{CONTEXT}}", "/p/.chief/prds/main", "add search")
	if want := "dir=/p/.chief/prds/main ctx=add search"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An empty context must not leave a bare placeholder in the brief; chief
// substitutes a sentence telling the agent to ask.
func TestRenderHandlesAnEmptyContext(t *testing.T) {
	got := Render("ctx={{CONTEXT}}", "/p", "  ")
	if strings.Contains(got, VarContext) {
		t.Error("an unsubstituted placeholder reached the agent")
	}
	if !strings.Contains(strings.ToLower(got), "ask") {
		t.Errorf("empty context should tell the agent to ask; got %q", got)
	}
}

func TestResolveUsesTheOverride(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, KindNew, "custom for {{PRD_DIR}}"); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve(root, KindNew, "/p/.chief/prds/main", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "custom for /p/.chief/prds/main" {
		t.Errorf("Resolve did not use the override: %q", got)
	}
}

func TestUnknownKindIsRejected(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root, Kind("nonsense")); err == nil {
		t.Error("Load should reject an unknown kind")
	}
	if err := Save(root, Kind("nonsense"), "x"); err == nil {
		t.Error("Save should reject an unknown kind")
	}
}

func TestBothKindsAreIndependent(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, KindNew, "new override {{PRD_DIR}}"); err != nil {
		t.Fatal(err)
	}

	if p, _ := Load(root, KindEdit); p.Custom {
		t.Error("overriding the new prompt must not affect the edit prompt")
	}
}
