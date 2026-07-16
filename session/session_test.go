package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	sess, err := New(sessionsDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer sess.Close()

	// Verify session ID is non-empty.
	if sess.ID() == "" {
		t.Error("expected non-empty session ID")
	}

	// Verify the sessions directory was created.
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		t.Fatalf("expected sessions directory at %s", sessionsDir)
	}

	// Verify the .jsonl file is in the sessions directory.
	expectedFile := filepath.Join(sessionsDir, sess.ID()+".jsonl")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Errorf("expected session file at %s: %v", expectedFile, err)
	}

	// Verify the file does NOT exist in the temp directory root (i.e., not in CWD).
	cwdFile := filepath.Join(tmpDir, sess.ID()+".jsonl")
	if _, err := os.Stat(cwdFile); err == nil {
		t.Errorf("session file incorrectly placed in CWD: %s", cwdFile)
	}
}

func TestRecordMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	sess, err := New(sessionsDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer sess.Close()

	// Record a message.
	sess.RecordMessage("user", "hello world", nil, "")

	// Verify the message appears in History.
	sessionFile := filepath.Join(sessionsDir, sess.ID()+".jsonl")
	events, err := History(sessionFile)
	if err != nil {
		t.Fatalf("History() error: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (session header + message), got %d", len(events))
	}

	// The second event should be a user message.
	if events[1].Type != "message" {
		t.Errorf("expected second event type 'message', got '%s'", events[1].Type)
	}

	// Verify the title was set from the first user message.
	if sess.Title() == "" {
		t.Error("expected title to be set from first user message")
	}
	if !strings.Contains(sess.Title(), "hello") {
		t.Errorf("expected title to contain 'hello', got '%s'", sess.Title())
	}
}

func TestOpen(t *testing.T) {
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	// Create a session.
	sess, err := New(sessionsDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	sess.RecordMessage("user", "test message", nil, "")
	sessionID := sess.ID()
	sess.Close()

	// Open by ID.
	opened, err := Open(sessionID, sessionsDir)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer opened.Close()

	if opened.ID() != sessionID {
		t.Errorf("expected ID %s, got %s", sessionID, opened.ID())
	}

	// Verify history is preserved.
	sessionFile := filepath.Join(sessionsDir, sessionID+".jsonl")
	events, err := History(sessionFile)
	if err != nil {
		t.Fatalf("History() error: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events after reopening, got %d", len(events))
	}

	// Verify title was loaded from the file.
	if opened.Title() == "" {
		t.Error("expected title to be loaded from file")
	}
}

func TestList(t *testing.T) {
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	// Create multiple sessions.
	sessionIDs := []string{}
	for i := 0; i < 3; i++ {
		sess, err := New(sessionsDir)
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		sess.RecordMessage("user", "session message", nil, "")
		sessionIDs = append(sessionIDs, sess.ID())
		sess.Close()
	}

	// List sessions.
	summaries, err := List(sessionsDir)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(summaries) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(summaries))
	}

	// Verify all session IDs are present.
	foundIDs := make(map[string]bool)
	for _, summary := range summaries {
		foundIDs[summary.ID] = true
		// Verify title is present.
		if summary.Title == "" {
			t.Errorf("expected title for session %s", summary.ID)
		}
		// Verify timestamp is present.
		if summary.Timestamp == "" {
			t.Errorf("expected timestamp for session %s", summary.ID)
		}
	}

	for _, id := range sessionIDs {
		if !foundIDs[id] {
			t.Errorf("session %s not found in list", id)
		}
	}
}

func TestEmptyList(t *testing.T) {
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	// List sessions from an empty directory.
	summaries, err := List(sessionsDir)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if summaries == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(summaries))
	}
}

func TestTitleUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	sess, err := New(sessionsDir)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer sess.Close()

	// Initially no title.
	if sess.Title() != "" {
		t.Errorf("expected empty title initially, got '%s'", sess.Title())
	}

	// Record a message - should set title.
	sess.RecordMessage("user", "What is the meaning of life?", nil, "")

	if sess.Title() == "" {
		t.Error("expected title to be set after first user message")
	}

	// Verify the title was persisted to the file.
	sessionFile := filepath.Join(sessionsDir, sess.ID()+".jsonl")
	events, err := History(sessionFile)
	if err != nil {
		t.Fatalf("History() error: %v", err)
	}

	// First event should have the title.
	if events[0].Title == "" {
		t.Error("expected title in session header event")
	}
}
