// Command loop is the Loop desktop application: a GUI over the chief engine.
//
// This file is deliberately thin. It owns exactly three things — the embedded
// frontend assets, the Wails application and window options, and (from M3) the
// bridge goroutine that forwards session events to the webview. All behaviour
// lives in internal/session, which knows nothing about Wails and is driven
// identically by cmd/loopctl and by tests.
//
// It lives at the module root rather than under cmd/ because //go:embed cannot
// reference a parent directory, and the frontend bundle must be reachable from
// the package that embeds it.
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

// Below these dimensions the three-column layout stops being usable: the rail
// collapses to icons and the inspector becomes an overlay, but the log and diff
// views still need a readable measure.
const (
	minWidth  = 940
	minHeight = 620

	defaultWidth  = 1440
	defaultHeight = 900
)

func main() {
	app := application.New(application.Options{
		Name:        "Loop",
		Description: "Build big projects by looping a coding agent over a PRD",
		Services:    []application.Service{
			// Registered from M2 on: project, prd, run, authoring, git, system.
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Loop",
		Width:     defaultWidth,
		Height:    defaultHeight,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		Mac: application.MacWindow{
			// The traffic lights sit inside our own title bar, which doubles as
			// the drag region and holds the command-palette affordance.
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 38,
		},
		// Matches --n-0 in frontend/src/styles/tokens.css. Setting it here stops
		// the window flashing white before the first paint.
		BackgroundColour: application.NewRGB(23, 24, 28),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
