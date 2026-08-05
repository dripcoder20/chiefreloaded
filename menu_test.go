package main

import (
	"runtime"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// buildMenu exercises the item builder against a hand-built menu so the test
// stays hermetic — application.DefaultApplicationMenu reaches for the running
// application's name to label its macOS app menu, which does not exist here.
func buildMenu(onNewPRD func()) (*application.Menu, *application.MenuItem) {
	menu := application.NewMenu()
	menu.AddSubmenu(fileMenuLabel)
	item := addNewPRDItem(menu, onNewPRD)
	return menu, item
}

func TestFileMenuContainsNewPRD(t *testing.T) {
	menu, _ := buildMenu(func() {})

	item := menu.FindByLabel(newPRDMenuLabel)
	if item == nil {
		t.Fatalf("File menu has no %q item", newPRDMenuLabel)
	}
	if item.Label() != newPRDMenuLabel {
		t.Errorf("label = %q, want %q", item.Label(), newPRDMenuLabel)
	}
}

func TestNewPRDAcceleratorIsPlatformAppropriate(t *testing.T) {
	_, item := buildMenu(func() {})

	// CmdOrCtrl renders as Cmd on macOS and Ctrl on Windows and Linux.
	want := "Ctrl+N"
	if runtime.GOOS == "darwin" {
		want = "Cmd+N"
	}
	if got := item.GetAccelerator(); got != want {
		t.Errorf("accelerator = %q, want %q", got, want)
	}
}

func TestSelectingNewPRDRunsCommandOnce(t *testing.T) {
	calls := 0
	handler := menuCommand(func() { calls++ })

	// A single command event, however it arrives, runs the command exactly once
	// — the webview, not Go, guards against a duplicate session.
	handler(nil)
	if calls != 1 {
		t.Fatalf("selecting the item ran the command %d times, want 1", calls)
	}
}

// addNewPRDItem must reuse an existing File submenu rather than creating a
// second one, so the item lands where users expect it.
func TestNewPRDReusesExistingFileMenu(t *testing.T) {
	menu := application.NewMenu()
	fileMenu := menu.AddSubmenu(fileMenuLabel)
	addNewPRDItem(menu, func() {})

	if got := fileMenu.FindByLabel(newPRDMenuLabel); got == nil {
		t.Fatalf("New PRD item was not added to the existing File submenu")
	}
}

// --- Open Project ------------------------------------------------------------

func TestOpenProjectItemIsInTheFileMenu(t *testing.T) {
	menu := application.NewMenu()
	menu.AddSubmenu(fileMenuLabel)
	item := addOpenProjectItem(menu, func() {})

	if item.Label() != openProjectMenuLabel {
		t.Errorf("label = %q, want %q", item.Label(), openProjectMenuLabel)
	}
}

// ⌘O on macOS, Ctrl+O elsewhere — the platform convention for "open".
func TestOpenProjectUsesThePlatformAccelerator(t *testing.T) {
	menu := application.NewMenu()
	menu.AddSubmenu(fileMenuLabel)
	item := addOpenProjectItem(menu, func() {})

	want := "Ctrl+O"
	if runtime.GOOS == "darwin" {
		want = "Cmd+O"
	}
	if got := item.GetAccelerator(); got != want {
		t.Errorf("accelerator = %q, want %q", got, want)
	}
}

func TestSelectingOpenProjectRunsCommandOnce(t *testing.T) {
	calls := 0
	handler := menuCommand(func() { calls++ })

	handler(nil)
	if calls != 1 {
		t.Fatalf("selecting the item ran the command %d times, want 1", calls)
	}
}

// Both items must land in the same File submenu rather than each creating one.
func TestBothFileItemsShareOneSubmenu(t *testing.T) {
	menu := application.NewMenu()
	menu.AddSubmenu(fileMenuLabel)
	addNewPRDItem(menu, func() {})
	addOpenProjectItem(menu, func() {})

	file := menu.FindByLabel(fileMenuLabel)
	if file == nil || !file.IsSubmenu() {
		t.Fatal("the File submenu is missing")
	}
	labels := map[string]bool{}
	for i := 0; i < 2; i++ {
		if item := file.GetSubmenu().ItemAt(i); item != nil {
			labels[item.Label()] = true
		}
	}
	for _, want := range []string{newPRDMenuLabel, openProjectMenuLabel} {
		if !labels[want] {
			t.Errorf("%q is not in the File submenu (got %v)", want, labels)
		}
	}
}
