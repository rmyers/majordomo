// Package session provides JSONL-based session storage, recording the full
// agentic loop (messages, tool calls, tool results) to disk for replay.
//
// Session files are stored as <session-id>.jsonl in the sessions directory.
// The first line contains session metadata (id, title, timestamp).
package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/llm"
)

// Session records the events of an agentic interaction to a JSONL file.
// It is safe for concurrent use by multiple goroutines.
type Session struct {
	id          string
	file        *os.File
	mu          sync.Mutex
	iteration   int    // monotonically increasing ID counter
	title       string // summarized title from the first user message
	timestamp   time.Time
	sessionsDir string // directory where this session is stored
}

// Event is a single line in the JSONL session file.
type Event struct {
	Type       string           `json:"type"`
	ID         string           `json:"id"`
	ParentID   *string          `json:"parentId,omitempty"`
	Timestamp  string           `json:"timestamp"`
	Message    *json.RawMessage `json:"message,omitempty"`
	ToolCall   *json.RawMessage `json:"tool_call,omitempty"`
	ToolResult *json.RawMessage `json:"tool_result,omitempty"`
	// Metadata about the LLM used for this event.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Session metadata (only in first line)
	Title string `json:"title,omitempty"`
}

// Message represents a single message in the session (user, assistant, or tool).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Thinking   string     `json:"thinking,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation by the LLM.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// ToolResult represents the result of executing a tool.
type ToolCallResult struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Summary is a lightweight representation of a session for listing.
type Summary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Timestamp string `json:"timestamp"`
}

const (
	// sessionFileVersion is the current JSONL format version.
	sessionFileVersion = 3
)

// New creates a new session in the specified sessions directory.
// The session file is created as <session-id>.jsonl.
// Returns the Session and any filesystem error.
func New(sessionsDir string) (*Session, error) {
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions directory: %w", err)
	}

	id := generateSessionID()
	ts := time.Now().UTC()
	filename := filepath.Join(sessionsDir, id+".jsonl")

	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("create session file: %w", err)
	}

	s := &Session{
		id:          id,
		file:        file,
		title:       "", // title will be set from the first user message
		timestamp:   ts,
		sessionsDir: sessionsDir,
	}

	return s, nil
}

// writeSessionHeader writes the session header line to the file.
// This is called when the first user message is recorded.
func (s *Session) writeSessionHeader(title string) {
	s.title = title
	ts := s.timestamp.Format(time.RFC3339Nano)
	header, _ := json.Marshal(&Message{
		Role: "system",
		Content: fmt.Sprintf(`{"version":%d,"id":"%s","timestamp":"%s","cwd":"%s"}`,
			sessionFileVersion, s.id, ts, pwd()),
	})
	s.writeEvent(Event{
		Type:      "session",
		ID:        s.id,
		Timestamp: ts,
		Message:   (*json.RawMessage)(&header),
		Title:     title,
	})
}

// Open resumes an existing session by its ID from the specified sessions directory.
// Returns the Session and any filesystem error.
func Open(id string, sessionsDir string) (*Session, error) {
	filename := filepath.Join(sessionsDir, id+".jsonl")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, fmt.Errorf("session %s not found", id)
	}

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}

	s := &Session{
		id:          id,
		file:        file,
		sessionsDir: sessionsDir,
	}

	// Read existing events to populate title and timestamp.
	events, err := History(filename)
	if err == nil && len(events) > 0 {
		// First event is the session header
		s.title = events[0].Title
		if ts, err := time.Parse(time.RFC3339Nano, events[0].Timestamp); err == nil {
			s.timestamp = ts
		}

		// If title is empty, extract from first user message
		if s.title == "" {
			for _, ev := range events {
				if ev.Type == "message" && ev.Message != nil {
					var msg Message
					if err := json.Unmarshal(*ev.Message, &msg); err == nil {
						if msg.Role == "user" {
							s.title = summarize(msg.Content)
							break
						}
					}
				}
			}
		}
	}

	return s, nil
}

// Close flushes and closes the session file.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		err := s.file.Close()
		s.file = nil
		return err
	}
	return nil
}

// RecordMessage records a user or assistant message to the session.
// For assistant messages, pass the tool calls as well.
// parentId is the ID of the message this one responds to (nil for the first user message).
func (s *Session) RecordMessage(role string, content string, toolCalls []llm.ToolCall, parentId string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.iteration++
	id := fmt.Sprintf("%08x", s.iteration)
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	var parentID *string
	if parentId != "" {
		parentID = &parentId
	}

	// Build tool calls for the event.
	var eventToolCalls []ToolCall
	for _, tc := range toolCalls {
		eventToolCalls = append(eventToolCalls, ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	msg, _ := json.Marshal(&Message{
		Role:       role,
		Content:    content,
		ToolCalls:  eventToolCalls,
		ToolCallID: "",
	})

	// If this is the first user message, write the session header with the title before the message.
	if role == "user" && s.title == "" {
		s.title = summarize(content)
		s.writeSessionHeader(s.title)
	}

	msgCopy := json.RawMessage(msg)
	s.writeEvent(Event{
		Type:      "message",
		ID:        id,
		ParentID:  parentID,
		Timestamp: ts,
		Message:   &msgCopy,
	})
}

// RecordToolResult records the result of a tool execution.
// callID is the ID of the tool call this result corresponds to.
// parentId is the ID of the assistant message that made the tool call.
func (s *Session) RecordToolResult(callID string, output string, errStr string, parentId string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.iteration++
	id := fmt.Sprintf("%08x", s.iteration)
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	toolMsg, _ := json.Marshal(&Message{
		Role:       "tool",
		Content:    output,
		ToolCallID: callID,
	})
	toolMsgCopy := json.RawMessage(toolMsg)

	tr, _ := json.Marshal(&ToolCallResult{
		Output: output,
		Error:  errStr,
	})
	trCopy := json.RawMessage(tr)

	s.writeEvent(Event{
		Type:       "message",
		ID:         id,
		ParentID:   &parentId,
		Timestamp:  ts,
		Message:    &toolMsgCopy,
		ToolResult: &trCopy,
	})
}

// RecordModel records which LLM provider and model were used for this session.
func (s *Session) RecordModel(provider string, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.writeEvent(Event{
		Type:      "model_change",
		ID:        fmt.Sprintf("%08x", s.iteration+1),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Provider:  provider,
		Model:     model,
	})
}

// SetTitle updates the session title (from a summarized first message).
func (s *Session) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.title = title
}

// ID returns the session's unique identifier.
func (s *Session) ID() string {
	return s.id
}

// Dir returns the session file path (for compatibility with old API).
func (s *Session) Dir() string {
	return filepath.Join(s.sessionsDir, s.id+".jsonl")
}

// Title returns the session's title (summarized first message).
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// History reads all events from a session file and returns them.
func History(filepath string) ([]Event, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}

	var events []Event
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		events = append(events, ev)
	}

	return events, nil
}

// summaryWithModTime is used internally for sorting sessions by modification time.
type summaryWithModTime struct {
	Summary
	modTime time.Time
}

// List returns summaries of all sessions in the specified directory, newest first (by file modification time).
func List(sessionsDir string) ([]Summary, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Summary{}, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var summariesWithTime []summaryWithModTime
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		// Extract session ID from filename (remove .jsonl extension)
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		filepath := filepath.Join(sessionsDir, e.Name())

		// Get file info for timestamp
		info, err := e.Info()
		if err != nil {
			continue
		}

		// Read first line to get title
		events, err := History(filepath)
		if err != nil || len(events) == 0 {
			continue
		}

		title := events[0].Title
		timestamp := events[0].Timestamp

		// If title is empty in the header, extract from first user message
		if title == "" {
			for _, ev := range events {
				if ev.Type == "message" && ev.Message != nil {
					var msg Message
					if err := json.Unmarshal(*ev.Message, &msg); err == nil {
						if msg.Role == "user" {
							title = summarize(msg.Content)
							break
						}
					}
				}
			}
		}

		summariesWithTime = append(summariesWithTime, summaryWithModTime{
			Summary: Summary{
				ID:        id,
				Title:     title,
				Timestamp: timestamp,
			},
			modTime: info.ModTime(),
		})
	}

	// Sort newest first by modification time
	sort.Slice(summariesWithTime, func(i, j int) bool {
		return summariesWithTime[i].modTime.After(summariesWithTime[j].modTime)
	})

	// Extract just the summaries
	summaries := make([]Summary, len(summariesWithTime))
	for i, s := range summariesWithTime {
		summaries[i] = s.Summary
	}

	return summaries, nil
}

// writeEvent appends a single JSONL line to the session file.
func (s *Session) writeEvent(ev Event) {
	if s.file == nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line := append(data, '\n')
	slog.Debug("writing event to file", "event", string(data))
	if _, err := s.file.Write(line); err != nil {
		slog.Error("failed to write event", "error", err)
		return
	}
	if err := s.file.Sync(); err != nil {
		slog.Error("failed to sync event file", "error", err)
	}
}

// generateSessionID creates a short unique identifier (8 hex chars).
func generateSessionID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%08x", b)
}

// pwd returns the current working directory.
func pwd() string {
	cwd, _ := os.Getwd()
	return cwd
}

// summarize creates a short title from a message's content.
func summarize(content string) string {
	// Take the first sentence up to ~60 chars.
	const maxLen = 60
	content = strings.TrimSpace(content)
	// Find the first sentence boundary (period + whitespace or end).
	idx := strings.IndexFunc(content, func(r rune) bool {
		return r == '.'
	})
	if idx > 0 && idx < maxLen {
		title := strings.TrimSpace(content[:idx+1])
		if len(title) > maxLen {
			title = title[:maxLen-1] + "…"
		}
		return title
	}
	// Fall back to first word(s) up to maxLen.
	if len(content) > maxLen {
		return content[:maxLen-1] + "…"
	}
	return content
}

// SessionService provides a high-level interface to session operations.
// It owns the config reference and derives the sessions directory from it.
type SessionService struct {
	cfg *config.Config
}

// NewSessionService creates a new SessionService from the given config.
func NewSessionService(cfg *config.Config) *SessionService {
	return &SessionService{cfg: cfg}
}

// sessionsDir returns the sessions directory from the config.
func (svc *SessionService) sessionsDir() (string, error) {
	return config.SessionsDir()
}

// CreateSession creates a new session with the given content as the first user message.
// It writes the session header with a title derived from the content, then writes
// the user message event. Returns the created session.
// When content is empty, only the session header is written (no user message).
func (svc *SessionService) CreateSession(content string) (*Session, error) {
	sessionsDir, err := svc.sessionsDir()
	if err != nil {
		return nil, fmt.Errorf("get sessions directory: %w", err)
	}

	sess, err := New(sessionsDir)
	if err != nil {
		return nil, err
	}

	sess.writeSessionHeader(summarize(content))
	if content != "" {
		sess.RecordMessage("user", content, nil, "")
	}

	return sess, nil
}

// ListSessions returns summaries of all sessions.
func (svc *SessionService) ListSessions() ([]Summary, error) {
	sessionsDir, err := svc.sessionsDir()
	if err != nil {
		return nil, fmt.Errorf("get sessions directory: %w", err)
	}
	return List(sessionsDir)
}

// OpenSession opens an existing session by ID.
func (svc *SessionService) OpenSession(id string) (*Session, error) {
	sessionsDir, err := svc.sessionsDir()
	if err != nil {
		return nil, fmt.Errorf("get sessions directory: %w", err)
	}
	return Open(id, sessionsDir)
}

// SessionHistory returns all events from a session file by session ID.
func (svc *SessionService) SessionHistory(id string) ([]Event, error) {
	sessionsDir, err := svc.sessionsDir()
	if err != nil {
		return nil, fmt.Errorf("get sessions directory: %w", err)
	}
	sessionFile := filepath.Join(sessionsDir, id+".jsonl")
	return History(sessionFile)
}
