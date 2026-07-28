package server

import (
	"context"
	"sync"
)

// pendingMessage is one queued turn waiting for its predecessor to finish.
type pendingMessage struct {
	turnID string
	query  string
	sink   StreamSink
}

// sessionStream tracks the in-flight state for a single session: at most
// one active StreamQuery call, plus a FIFO queue of turns that arrived
// while the previous one was still running.
type sessionStream struct {
	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
	queue  []pendingMessage
}

// ActivityFunc is called whenever a session transitions between idle and
// busy — once per transition, not once per event. It's meant for a
// lightweight "is anything running" indicator (e.g. an index page showing
// a dot next to every session), as opposed to StreamSink which carries
// the full detail of one session's turn.
type ActivityFunc func(sessionID string, active bool)

// StreamManager serializes agent turns per session — only one StreamQuery
// runs at a time for a given session ID, so concurrent sends can't race
// on session state or interleave on the same event channel. Sends for
// *different* sessions run fully concurrently; they don't share a lock
// except briefly to look up/create their own sessionStream.
type StreamManager struct {
	srv        *Server
	onActivity ActivityFunc

	mu       sync.Mutex
	sessions map[string]*sessionStream
}

// NewStreamManager builds a StreamManager. onActivity may be nil if
// nothing needs the aggregate "is this session busy" signal.
func NewStreamManager(srv *Server, onActivity ActivityFunc) *StreamManager {
	if onActivity == nil {
		onActivity = func(string, bool) {}
	}
	return &StreamManager{
		srv:        srv,
		onActivity: onActivity,
		sessions:   make(map[string]*sessionStream),
	}
}

func (m *StreamManager) stateFor(sessionID string) *sessionStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[sessionID]
	if !ok {
		st = &sessionStream{}
		m.sessions[sessionID] = st
	}
	return st
}

// Send accepts one user message for sessionID. The user's bubble is
// rendered and appended to the chat window immediately, regardless of
// whether it can start running right away or has to queue behind a turn
// already in progress — only the assistant's response is ever delayed.
func (m *StreamManager) Send(sessionID, query string, sink StreamSink) {
	turnID := nextTurnID()
	sink.Emit("user", "", userMessageHTML(turnID, query))

	st := m.stateFor(sessionID)

	st.mu.Lock()
	if st.busy {
		st.queue = append(st.queue, pendingMessage{turnID: turnID, query: query, sink: sink})
		position := len(st.queue)
		st.mu.Unlock()
		sink.Emit("queued", "", queuedPlaceholderHTML(turnID, position))
		return
	}
	st.busy = true
	st.mu.Unlock()

	sink.Emit("status", "", assistantHTML(turnID, true, typingDots))
	m.onActivity(sessionID, true)
	go m.run(sessionID, st, turnID, query, sink)
}

func (m *StreamManager) run(sessionID string, st *sessionStream, turnID, query string, sink StreamSink) {
	ctx, cancel := context.WithCancel(context.Background())

	st.mu.Lock()
	st.cancel = cancel
	st.mu.Unlock()

	// StreamQuery handles rendering the "(cancelled)" state itself if ctx
	// gets cancelled mid-turn — it's the only place that still has the
	// partial response text in scope.
	m.srv.StreamQuery(ctx, sessionID, turnID, query, sink)
	cancel()

	st.mu.Lock()
	st.cancel = nil
	var next *pendingMessage
	if len(st.queue) > 0 {
		nm := st.queue[0]
		st.queue = st.queue[1:]
		next = &nm
	} else {
		st.busy = false
	}
	st.mu.Unlock()

	if next != nil {
		// Flip this turn's bubble from "queued" to the typing placeholder
		// now that its turn has actually started.
		next.sink.Emit("status", responseTargetID(next.turnID), assistantHTML(next.turnID, true, typingDots))
		go m.run(sessionID, st, next.turnID, next.query, next.sink)
	} else {
		m.onActivity(sessionID, false)
	}
}

// Cancel stops the currently-running turn for sessionID, if any, and
// returns whether there was one to stop. Queued-but-not-started turns are
// left alone (and will run next) unless clearQueue is true.
func (m *StreamManager) Cancel(sessionID string, clearQueue bool) bool {
	st := m.stateFor(sessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	cancelled := st.cancel != nil
	if cancelled {
		st.cancel()
	}
	if clearQueue {
		st.queue = nil
	}
	return cancelled
}

// ActiveSessions returns the IDs of every session currently busy (running
// or queued). Meant for populating an index/sidebar view's initial state
// on load — after that, ActivityFunc events keep it current without
// needing to poll this again.
func (m *StreamManager) ActiveSessions() []string {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	sessions := make([]*sessionStream, 0, len(m.sessions))
	for id, st := range m.sessions {
		ids = append(ids, id)
		sessions = append(sessions, st)
	}
	m.mu.Unlock()

	var active []string
	for i, st := range sessions {
		st.mu.Lock()
		busy := st.busy
		st.mu.Unlock()
		if busy {
			active = append(active, ids[i])
		}
	}
	return active
}
