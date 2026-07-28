package server

import (
	"fmt"
	"html"
	"sync/atomic"
)

// turnCounter generates unique ids for message DOM elements. Plain
// atomic counter is enough here — StreamManager already serializes turns
// per session, so uniqueness (not ordering) is all this needs to
// guarantee.
var turnCounter uint64

func nextTurnID() string {
	return fmt.Sprintf("t%d", atomic.AddUint64(&turnCounter, 1))
}

const typingDots = `<span></span><span></span><span></span>`

// responseTargetID is the DOM id every update for one assistant turn
// replaces via outerHTML, from the initial placeholder through to the
// final rendered response.
func responseTargetID(turnID string) string {
	return "msg-response-" + turnID
}

// userMessageHTML renders the bubble for a just-submitted user message.
// Appended (never replaced) — target is always "" for this one.
func userMessageHTML(turnID, content string) string {
	return fmt.Sprintf(`<div class="message user" id="msg-user-%s">%s</div>`,
		turnID, html.EscapeString(content))
}

// assistantHTML renders (or re-renders) the assistant's bubble for one
// turn. innerHTML is caller-rendered (markdown output, typing dots, an
// error span, whatever belongs inside) — this just wraps it with the
// right id/class so later events can find and replace it.
func assistantHTML(turnID string, typing bool, innerHTML string) string {
	class := "message assistant"
	if typing {
		class += " typing"
	}
	return fmt.Sprintf(`<div class="%s" id="%s">%s</div>`, class, responseTargetID(turnID), innerHTML)
}

// queuedPlaceholderHTML is shown the moment a message is accepted but
// has to wait behind another turn already in progress.
func queuedPlaceholderHTML(turnID string, position int) string {
	return assistantHTML(turnID, false, fmt.Sprintf(`<span class="queued">queued (#%d)…</span>`, position))
}

// toolBadgeHTML is prepended above existing content while a tool call is
// running, so the user sees what's happening without losing the partial
// response so far.
func toolBadgeHTML(tool string) string {
	return fmt.Sprintf(`<div class="tool-badge">running %s…</div>`, html.EscapeString(tool))
}

func errorHTML(message string) string {
	return fmt.Sprintf(`<span class="error">%s</span>`, message)
}
