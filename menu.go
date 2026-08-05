// Application menu wiring.
//
// The File ▸ New PRD and File ▸ Open Project… items are the desktop-native twins
// of the in-window "New PRD" tab and project picker. It does not itself begin authoring: selecting it (or pressing its
// accelerator) asks the webview to reveal and focus the New PRD tab, which is
// where a session is actually started. Routing through one event rather than
// starting a session in Go keeps a single entry point, so the same command
// delivered twice can never leave two sessions behind — the webview handler is
// idempotent.
package main

import "github.com/wailsapp/wails/v3/pkg/application"

const (
	// eventMenuNewPRD asks the webview to open and focus the New PRD tab.
	eventMenuNewPRD = "loop:menu:new-prd"

	// eventMenuOpenProject asks the webview to run the project picker. It is
	// routed through the frontend rather than opening the native dialog here so
	// that opening a project is one code path, and so the handler can decline
	// while a modal question is waiting.
	eventMenuOpenProject = "loop:menu:open-project"

	// newPRDAccelerator resolves to ⌘N on macOS and Ctrl+N on Windows and Linux
	// — "CmdOrCtrl" is Command on darwin and Control elsewhere.
	newPRDAccelerator = "CmdOrCtrl+n"

	// openProjectAccelerator is ⌘O / Ctrl+O, the platform convention for "open".
	openProjectAccelerator = "CmdOrCtrl+o"

	newPRDMenuLabel      = "New PRD"
	openProjectMenuLabel = "Open Project…"
	fileMenuLabel        = "File"
)

// MenuCommands are the callbacks the application menu invokes. A struct rather
// than positional parameters so adding an item later does not change every
// call site.
type MenuCommands struct {
	NewPRD      func()
	OpenProject func()
}

// applicationMenu builds the platform's default application menu and adds the
// File items that run cmds when selected or shortcut-triggered.
func applicationMenu(cmds MenuCommands) *application.Menu {
	menu := application.DefaultApplicationMenu()
	addNewPRDItem(menu, cmds.NewPRD)
	addOpenProjectItem(menu, cmds.OpenProject)
	return menu
}

// addOpenProjectItem inserts the Open Project… item into the File submenu and
// returns it. Like addNewPRDItem it is the seam the tests exercise, so it must
// not depend on a running application.
func addOpenProjectItem(menu *application.Menu, onOpenProject func()) *application.MenuItem {
	item := fileSubmenu(menu).Add(openProjectMenuLabel)
	item.SetAccelerator(openProjectAccelerator)
	item.OnClick(menuCommand(onOpenProject))
	return item
}

// addNewPRDItem inserts the New PRD item into the menu's File submenu and
// returns it. It is the seam the tests exercise, so it must not depend on a
// running application.
func addNewPRDItem(menu *application.Menu, onNewPRD func()) *application.MenuItem {
	item := fileSubmenu(menu).Add(newPRDMenuLabel)
	item.SetAccelerator(newPRDAccelerator)
	item.OnClick(menuCommand(onNewPRD))
	return item
}

// fileSubmenu returns the menu's existing File submenu, creating one if the
// platform's default menu has none.
func fileSubmenu(menu *application.Menu) *application.Menu {
	item := menu.FindByLabel(fileMenuLabel)
	if item != nil && item.IsSubmenu() {
		return item.GetSubmenu()
	}
	return menu.AddSubmenu(fileMenuLabel)
}

// menuCommand adapts a plain command callback to a menu click handler.
func menuCommand(run func()) func(*application.Context) {
	return func(*application.Context) { run() }
}
