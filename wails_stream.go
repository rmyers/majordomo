package main

// Desktop-wrapper-only file — the one place that imports the Wails
// runtime. server.StreamManager (transport-agnostic) does the actual
// per-session queueing/cancellation and renders every fragment; this
// just relays whatever it renders onto a Wails event channel.

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// streamEnvelope is exactly what server.StreamSink.Emit passes through —
// target/html tell the frontend how to update the DOM, event is just a
// label for status-bar/button-state logic.
type streamEnvelope struct {
	Event  string `json:"event"`
	Target string `json:"target"`
	HTML   string `json:"html"`
}

// wailsSink implements server.StreamSink, emitting on a single
// per-session channel: "stream:<sessionID>".
type wailsSink struct {
	ctx       context.Context
	sessionID string
}

func newWailsSink(ctx context.Context, sessionID string) *wailsSink {
	return &wailsSink{ctx: ctx, sessionID: sessionID}
}

func (w *wailsSink) Emit(event, target, html string) {
	runtime.EventsEmit(w.ctx, "stream:"+w.sessionID, streamEnvelope{
		Event:  event,
		Target: target,
		HTML:   html,
	})
}

// SendMessage queues (or immediately starts) one agent turn for
// sessionID. Returns immediately — the actual work, and every chat-window
// update it produces, arrives asynchronously over "stream:<sessionID>".
// The frontend should have already called window.runtime.EventsOn for
// that channel before calling this, or the earliest updates (the user's
// own message bubble, in particular) can be missed.
//
// Callable from JS as: await window.go.main.App.SendMessage(query, sessionID)
func (a *App) SendMessage(query, sessionID string) error {
	sink := newWailsSink(a.ctx, sessionID)
	a.streams.Send(sessionID, query, sink)
	return nil
}

// CancelStream stops the currently-running turn for sessionID, if any.
// clearQueue additionally drops any turns that were queued behind it.
// Returns whether a turn was actually running (false just means there
// was nothing in flight to cancel).
//
// Callable from JS as: await window.go.main.App.CancelStream(sessionID, false)
func (a *App) CancelStream(sessionID string, clearQueue bool) bool {
	return a.streams.Cancel(sessionID, clearQueue)
}
