// Package session provides JSONL-based session storage, recording the full
// agentic loop (messages, tool calls, tool results) to disk for replay.
//
// Session files follow the PI JSONL format: each line is a JSON object
// representing one event in the conversation. The directory layout mirrors
// PI's: ~/.majordomo/sessions/<session-id>/<timestamp>_<uuid>.jsonl
package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/julython/majordomo/internal/llm"
)

// Session records the events of an agentic interaction to a JSONL file.
// It is safe for concurrent use by multiple goroutines.
type Session struct {
	id        string
	dir       string // session directory path
	file      *os.File
	mu        sync.Mutex
	iteration int  // monotonically increasing ID counter
	title     string // summarized title from the first user message
}

// Event is a single line in the JSONL session file.
type Event struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	ParentID   *string         `json:"parentId,omitempty"`
	Timestamp  string          `json:"timestamp"`
	Message    *json.RawMessage `json:"message,omitempty"`
	ToolCall   *json.RawMessage `json:"tool_call,omitempty"`
	ToolResult *json.RawMessage `json:"tool_result,omitempty"`
	// Metadata about the LLM used for this event.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Message represents a single message in the session (user, assistant, or tool).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
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
	// configDirName is the root directory under ~/.majordomo/ where sessions live.
	configDirName = "majordomo"
	// sessionsSubDir is the subdirectory for session files.
	sessionsSubDir = "sessions"
	// sessionFileVersion is the current JSONL format version.
	sessionFileVersion = 3
)

// configDir is the configured base directory for sessions.
// When empty, New/Open/List fall back to os.UserConfigDir().
var configDir string

// SetConfigDir sets the base directory for session storage.
// Call this from the server before creating or opening sessions.
func SetConfigDir(dir string) {
	configDir = dir
}

// New creates a new session under the default config directory (~/.majordomo/sessions/).
// The session directory is created, and the JSONL file is opened for appending.
// Returns the Session and any filesystem error.
func New() (*Session, error) {
	baseDir := configDir
	if baseDir == "" {
		var err error
		baseDir, err = os.UserConfigDir()
		if err != nil {
			baseDir = "."
		}
	}

	sessionsDir := filepath.Join(baseDir, configDirName, sessionsSubDir)
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions directory: %w", err)
	}

	id := generateSessionID()
	ts := time.Now().UTC()
	dirName := ts.Format("2006-01-02T15-04-05.000Z") + "_" + id

	dir := filepath.Join(sessionsDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}

	filename := dirName + ".jsonl"
	file, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		return nil, fmt.Errorf("create session file: %w", err)
	}

	s := &Session{
		id:    id,
		dir:   dir,
		file:  file,
		title: "", // title will be set from the first user message
	}

	// Write the session header record.
	header, _ := json.Marshal(&Message{
		Role: "system",
		Content: fmt.Sprintf(`{"version":%d,"id":"%s","timestamp":"%s","cwd":"%s"}`,
			sessionFileVersion, id, ts.Format(time.RFC3339Nano), pwd()),
	})
	s.writeEvent(Event{
		Type:      "session",
		ID:        id,
		Timestamp: ts.Format(time.RFC3339Nano),
		Message:   (*json.RawMessage)(&header),
	})

	return s, nil
}

// Open resumes an existing session by its ID.
// Returns the Session and any filesystem error.
func Open(id string) (*Session, error) {
	baseDir := configDir
	if baseDir == "" {
		var err error
		baseDir, err = os.UserConfigDir()
		if err != nil {
			baseDir = "."
		}
	}

	sessionsDir := filepath.Join(baseDir, configDirName, sessionsSubDir)

	// Find the directory whose name contains this session ID.
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, err
	}

	var targetDir string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "_"+id) {
			targetDir = filepath.Join(sessionsDir, e.Name())
			break
		}
	}
	if targetDir == "" {
		return nil, fmt.Errorf("session %s not found", id)
	}

	// Find the JSONL file.
	files, err := filepath.Glob(filepath.Join(targetDir, "*.jsonl"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no session file in %s", targetDir)
	}

	file, err := os.OpenFile(files[0], os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}

	s := &Session{
		id:    id,
		dir:   targetDir,
		file:  file,
		title: "", // will be populated from existing events
	}

	// Read existing events to populate title.
	sessions, err := History(targetDir)
	if err == nil {
		for _, ev := range sessions {
			if ev.Type == "message" && ev.Message != nil {
				var msg Message
				if err := json.Unmarshal(*ev.Message, &msg); err == nil {
					if msg.Role == "user" && s.title == "" {
						s.title = summarize(msg.Content)
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

	s.writeEvent(Event{
		Type:      "message",
		ID:        id,
		ParentID:  parentID,
		Timestamp: ts,
		Message:   (*json.RawMessage)(&msg),
	})

	// If this is the first user message and no title yet, summarize it.
	if role == "user" && s.title == "" {
		s.title = summarize(content)
	}
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
	tr, _ := json.Marshal(&ToolCallResult{
		Output: output,
		Error:  errStr,
	})

	s.writeEvent(Event{
		Type:       "message",
		ID:         id,
		ParentID:   &parentId,
		Timestamp:  ts,
		Message:    (*json.RawMessage)(&toolMsg),
		ToolResult: (*json.RawMessage)(&tr),
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

// Dir returns the session directory path.
func (s *Session) Dir() string {
	return s.dir
}

// Title returns the session's title (summarized first message).
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// History reads all events from a session file and returns them.
func History(dir string) ([]Event, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no session file in %s", dir)
	}

	data, err := os.ReadFile(files[0])
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

		var entry historyEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		ev := Event{
			Type:      entry.Type,
			ID:        entry.ID,
			Timestamp: entry.Timestamp,
			Provider:  entry.Provider,
			Model:     entry.Model,
		}
		if entry.ParentID != nil {
			ev.ParentID = entry.ParentID
		}

		// Reconstruct the appropriate typed field.
		switch entry.Type {
		case "session":
			if entry.Message != nil {
				var msg Message
				if err := json.Unmarshal(*entry.Message, &msg); err == nil {
					ev.Message = &json.RawMessage{}
					*ev.Message = *entry.Message
				}
			}
		case "message":
			if entry.Message != nil {
				ev.Message = entry.Message
			}
			if entry.ToolResult != nil {
				var tr ToolCallResult
				if err := json.Unmarshal(*entry.ToolResult, &tr); err == nil {
					ev.ToolResult = &json.RawMessage{}
					*ev.ToolResult = *entry.ToolResult
				}
			}
		}

		events = append(events, ev)
	}

	return events, nil
}

// List returns summaries of all sessions, newest first.
func List() ([]Summary, error) {
	baseDir := configDir
	if baseDir == "" {
		var err error
		baseDir, err = os.UserConfigDir()
		if err != nil {
			baseDir = "."
		}
	}

	sessionsDir := filepath.Join(baseDir, configDirName, sessionsSubDir)
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var summaries []Summary
	for _, e := range entries {
		if e.IsDir() {
			name := e.Name()
			if idx := strings.Index(name, "_"); idx > 0 {
				id := name[idx+1:]
				dir := filepath.Join(sessionsDir, name)
				title := ""
				ts := ""

				// Read the JSONL to extract title and timestamp.
				sessions, histErr := History(dir)
				if histErr == nil {
					for _, ev := range sessions {
						if ev.Type == "message" && ev.Message != nil {
							var msg Message
							if err := json.Unmarshal(*ev.Message, &msg); err == nil {
								if msg.Role == "user" && title == "" {
									title = summarize(msg.Content)
								}
							}
						}
						if ev.Type == "session" && ts == "" {
							ts = ev.Timestamp
						}
					}
				}

				summaries = append(summaries, Summary{
					ID:        id,
					Title:     title,
					Timestamp: ts,
				})
			}
		}
	}

	// Sort newest first (directories are named with timestamps).
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Timestamp > summaries[j].Timestamp
	})

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
	s.file.Write(append(data, '\n'))
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

// historyEntry is a single deserialized JSONL line.
type historyEntry struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	ParentID   *string         `json:"parentId,omitempty"`
	Timestamp  string          `json:"timestamp"`
	Message    *json.RawMessage `json:"message,omitempty"`
	ToolCall   *json.RawMessage `json:"tool_call,omitempty"`
	ToolResult *json.RawMessage `json:"tool_result,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	Model      string          `json:"model,omitempty"`
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
