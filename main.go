// Command loop is the Loop desktop application: a GUI over the chief engine.
//
// This file is deliberately thin. It owns the embedded frontend, the Wails
// application and window options, and the bridge that forwards session events to
// the webview. All behaviour lives in internal/session, which knows nothing
// about Wails and is driven identically by cmd/loopctl and by tests.
//
// It lives at the module root rather than under cmd/ because //go:embed cannot
// reference a parent directory, and the frontend bundle must be reachable from
// the package that embeds it.
package main

import (
	"context"
	"embed"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/dripcoder/loop/internal/services"
	"github.com/dripcoder/loop/internal/session"
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

// Event names crossing into the webview. Deliberately few.
//
// Wails' Emit offers no ordering guarantee across event names and no delivery
// confirmation, so several named channels would let the frontend observe a push
// before the story that caused it. One ordered, sequence-numbered firehose plus
// a snapshot fallback is the only shape that stays correct over a lossy
// transport.
const (
	eventBatch = "loop:events"
	eventReady = "loop:ready"

	// Authoring terminal output has its own channel. It is high-volume, binary
	// and interesting to exactly one component; putting it on the ordered
	// firehose would evict real events from the retention ring for no benefit.
	eventAuthor     = "loop:author"
	eventAuthorExit = "loop:author:exit"
)

// Batching parameters for the firehose.
//
// One Emit per log line is death by IPC: each crosses the bridge and schedules a
// microtask, and a busy agent produces hundreds of lines a second. Flushing on
// whichever of these comes first keeps the frontend at a steady ~20 batches a
// second under load while staying imperceptible when idle.
const (
	flushInterval = 50 * time.Millisecond
	flushSize     = 256
)

// init registers the event payload types.
//
// Events do not appear in any bound method signature, so the binding generator
// has no other way to learn their shape — without this the frontend would be
// hand-writing interfaces that silently drift from the Go structs.
func init() {
	application.RegisterEvent[session.AuthorEvent](eventAuthor)
	application.RegisterEvent[session.AuthorExit](eventAuthorExit)
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sess, err := session.New(session.Options{Logger: logger})
	if err != nil {
		log.Fatal(err)
	}

	app := application.New(application.Options{
		Name:        "Loop",
		Description: "Build big projects by looping a coding agent over a PRD",
		Services: []application.Service{
			application.NewService(services.NewProject(sess)),
			application.NewService(services.NewPRD(sess)),
			application.NewService(services.NewRun(sess)),
			application.NewService(services.NewAuthoring(sess)),
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

	sess.SetAuthoringSinks(
		func(ev session.AuthorEvent) { app.Event.Emit(eventAuthor, ev) },
		func(ev session.AuthorExit) { app.Event.Emit(eventAuthorExit, ev) },
	)

	go bridgeEvents(app, sess)
	openWorkingDirectory(sess, logger)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	_ = sess.Close()
}

// openWorkingDirectory opens the directory Loop was launched from.
//
// chief is run from inside the project, so `cd myapp && loop` should just work
// and not present an empty window with a file picker. A directory with no
// .chief/ is opened all the same — that is the first-run case, and the UI
// explains it better than a dialog would.
func openWorkingDirectory(sess *session.Session, logger *slog.Logger) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	if _, err := sess.OpenProject(context.Background(), cwd); err != nil {
		// Not fatal: the user can still pick a directory. Launched from a
		// Finder double-click the cwd is often "/", which is simply not a
		// project.
		logger.Info("no project in the working directory", "dir", cwd, "err", err)
	}
}

// bridgeEvents forwards the session's event stream into the webview in batches.
//
// It must never block on the session channel, or the backpressure travels all
// the way to the agent's stdout scanner. It never does: the only thing it waits
// on is a timer.
func bridgeEvents(app *application.App, sess *session.Session) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]session.Event, 0, flushSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		app.Event.Emit(eventBatch, batch)
		// A fresh slice, not batch[:0]: Emit hands the frontend a reference and
		// reusing the array would rewrite events it has not serialised yet.
		batch = make([]session.Event, 0, flushSize)
	}

	events := sess.Events()
	// Tell the frontend the stream is live so it can take its first snapshot
	// without polling for one.
	app.Event.Emit(eventReady, nil)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				flush()
				return
			}
			batch = append(batch, ev)
			if len(batch) >= flushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
