// Application menu wiring.
//
// The File ▸ New PRD item is the desktop-native twin of the in-window "New PRD"
// tab. It does not itself begin authoring: selecting it (or pressing its
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

	// newPRDAccelerator resolves to ⌘N on macOS and Ctrl+N on Windows and Linux
	// — "CmdOrCtrl" is Command on darwin and Control elsewhere.
	newPRDAccelerator = "CmdOrCtrl+n"

	newPRDMenuLabel = "New PRD"
	fileMenuLabel   = "File"
)

// applicationMenu builds the platform's default application menu and adds a
// File ▸ New PRD item that runs onNewPRD when selected or shortcut-triggered.
func applicationMenu(onNewPRD func()) *application.Menu {
	menu := application.DefaultApplicationMenu()
	addNewPRDItem(menu, onNewPRD)
	return menu
}

// addNewPRDItem inserts the New PRD item into the menu's File submenu and
// returns it. It is the seam the tests exercise, so it must not depend on a
// running application.
func addNewPRDItem(menu *application.Menu, onNewPRD func()) *application.MenuItem {
	item := fileSubmenu(menu).Add(newPRDMenuLabel)
	item.SetAccelerator(newPRDAccelerator)
	item.OnClick(onNewPRDSelected(onNewPRD))
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

// onNewPRDSelected adapts the plain command callback to a menu click handler.
func onNewPRDSelected(onNewPRD func()) func(*application.Context) {
	return func(*application.Context) { onNewPRD() }
}
