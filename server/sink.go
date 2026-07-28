package server

// StreamSink receives fully-rendered updates for one session's chat
// window. Every call is self-contained: html is a ready-to-insert
// fragment (already includes its own element id and CSS classes),
// target is the DOM id to replace via outerHTML (empty means "append as
// a new node"), and event just labels the kind of update for whatever
// lightweight status-bar/button-state logic the frontend wants — the
// frontend never needs to build or reason about message DOM itself.
//
// StreamQuery doesn't know or care what's on the other side; right now
// that's wailsSink (in the desktop wrapper module), emitting on a
// per-session Wails event channel.
type StreamSink interface {
	Emit(event, target, html string)
}
