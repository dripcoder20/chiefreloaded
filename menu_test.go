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
	handler := onNewPRDSelected(func() { calls++ })

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
