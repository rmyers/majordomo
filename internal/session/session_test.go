package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	SetConfigDir(tmpDir)
	defer SetConfigDir("")

	sess, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer sess.Close()

	// Verify session ID is non-empty.
	if sess.ID() == "" {
		t.Error("expected non-empty session ID")
	}

	// Verify the session directory was created in the correct location.
	expectedDir := filepath.Join(tmpDir, configDirName, sessionsSubDir)
	entries, err := os.ReadDir(expectedDir)
	if err != nil {
		t.Fatalf("expected sessions directory at %s: %v", expectedDir, err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one session directory")
	}

	// Verify the .jsonl file is inside the session directory, not in CWD.
	sessionDir := filepath.Join(expectedDir, entries[0].Name())
	files, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatalf("read session directory: %v", err)
	}
	var jsonlFile string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			jsonlFile = f.Name()
			break
		}
	}
	if jsonlFile == "" {
		t.Fatal("expected a .jsonl file in the session directory")
	}

	// Verify the file exists at the expected path.
	expectedPath := filepath.Join(sessionDir, jsonlFile)
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("expected session file at %s: %v", expectedPath, err)
	}

	// Verify the file does NOT exist in the temp directory root (i.e., not in CWD).
	cwdFile := filepath.Join(tmpDir, jsonlFile)
	if _, err := os.Stat(cwdFile); err == nil {
		t.Errorf("session file incorrectly placed in CWD: %s", cwdFile)
	}
}

func TestRecordMessage(t *testing.T) {
	tmpDir := t.TempDir()
	SetConfigDir(tmpDir)
	defer SetConfigDir("")

	sess, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer sess.Close()

	// Record a message.
	sess.RecordMessage("user", "hello world", nil, "")

	// Verify the message appears in History.
	events, err := History(sess.Dir())
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
}

func TestOpen(t *testing.T) {
	tmpDir := t.TempDir()
	SetConfigDir(tmpDir)
	defer SetConfigDir("")

	// Create a session.
	sess, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	sess.RecordMessage("user", "test message", nil, "")
	sess.Close()

	// Open by ID.
	opened, err := Open(sess.ID())
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer opened.Close()

	if opened.ID() != sess.ID() {
		t.Errorf("expected ID %s, got %s", sess.ID(), opened.ID())
	}

	// Verify history is preserved.
	events, err := History(opened.Dir())
	if err != nil {
		t.Fatalf("History() error: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events after reopening, got %d", len(events))
	}
}

func TestList(t *testing.T) {
	tmpDir := t.TempDir()
	SetConfigDir(tmpDir)
	defer SetConfigDir("")

	// Create multiple sessions.
	for i := 0; i < 3; i++ {
		sess, err := New()
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		sess.RecordMessage("user", "session message", nil, "")
		sess.Close()
	}

	// List sessions.
	summaries, err := List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(summaries) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(summaries))
	}

	// Verify ordering: newest first (by timestamp in directory name).
	for i := 0; i < len(summaries)-1; i++ {
		if summaries[i].Timestamp < summaries[i+1].Timestamp {
			t.Errorf("sessions not ordered newest first: %s < %s", summaries[i].Timestamp, summaries[i+1].Timestamp)
		}
	}
}

func TestConfigDirFallback(t *testing.T) {
	// When SetConfigDir is not called, New() should fall back to os.UserConfigDir().
	// Clear the config dir and verify it still works (just uses the system default).
	SetConfigDir("")

	sess, err := New()
	if err != nil {
		// May fail if UserConfigDir is not available in the test environment,
		// but should not panic or error with a path-related message.
		t.Logf("New() fallback error (expected in some environments): %v", err)
		return
	}
	sess.Close()
}
