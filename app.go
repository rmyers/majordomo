package main

import (
	"context"

	"github.com/rmyers/majordomo/server"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App holds the Wails runtime context plus a reference to your real
// majordomo server, so bound methods (native dialogs, SendMessage, etc.)
// can call straight into existing server logic.
type App struct {
	ctx     context.Context
	srv     *server.Server
	streams *server.StreamManager
}

func NewApp(srv *server.Server) *App {
	app := &App{srv: srv}
	// StreamManager gets a callback into this same App so it can
	// broadcast on the global "sessions:activity" channel whenever any
	// session starts or finishes being busy. This is separate from each
	// session's own "stream:<id>" channel — it's just a lightweight
	// signal for things like the index page's activity dots.
	app.streams = server.NewStreamManager(srv, app.broadcastActivity)
	return app
}

// startup is called once by Wails when the app launches. Stashing ctx
// here is what lets runtime.* calls (dialogs, events, etc.) work later.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) broadcastActivity(sessionID string, active bool) {
	if a.ctx == nil {
		// Shouldn't happen in practice — StreamManager is only driven by
		// SendMessage, which the frontend can't call before startup has
		// run — but guard anyway rather than panic on a nil context.
		return
	}
	runtime.EventsEmit(a.ctx, "sessions:activity", map[string]any{
		"sessionId": sessionID,
		"active":    active,
	})
}

// ActiveSessions returns the IDs of every session currently busy (running
// or queued), for the index page to seed its activity dots on load.
// Callable from JS as: await window.go.main.App.ActiveSessions()
func (a *App) ActiveSessions() []string {
	return a.streams.ActiveSessions()
}
