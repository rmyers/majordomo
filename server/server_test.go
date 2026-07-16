package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"html/template"

	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/session"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")

	srv := New(":0", sessionsDir)

	// Parse templates (this is normally done in Run()).
	if templates == nil {
		var err error
		templates, err = template.ParseFS(webFS, "templates/*.html")
		if err != nil {
			t.Fatalf("failed to parse templates: %v", err)
		}
	}

	// Create a test config
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Provider: "ollama",
			Model:    "test-model",
			URL:      "http://localhost:11434",
		},
	}

	// Lock config
	srv.mu.Lock()
	srv.cfg = cfg
	srv.mu.Unlock()

	return srv, sessionsDir
}

func TestTemplateParsing(t *testing.T) {
	// Verify templates can be parsed from embedded FS
	tmpl, err := template.ParseFS(webFS, "templates/*.html")
	if err != nil {
		t.Fatalf("template.ParseFS() error: %v", err)
	}

	// Verify index template exists
	if tmpl.Lookup("index.html") == nil {
		t.Error("expected 'index.html' template to be parsed")
	}

	// Verify chat template exists
	if tmpl.Lookup("chat.html") == nil {
		t.Error("expected 'chat.html' template to be parsed")
	}

	// Verify layout template exists (used by index and chat)
	if tmpl.Lookup("layout.html") == nil {
		t.Error("expected 'layout.html' template to be parsed")
	}
}

func TestHandleRoot(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleRoot)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleRoot() status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Majordomo") {
		t.Errorf("handleRoot() body does not contain 'Majordomo': %s", body)
	}

	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("handleRoot() body is not HTML: %s", body)
	}

	if !strings.Contains(body, "Welcome to Majordomo") {
		t.Errorf("handleRoot() body missing welcome text: %s", body)
	}
}

func TestHandleChat(t *testing.T) {
	srv, sessionsDir := setupTestServer(t)

	// Create a session first
	sess, err := session.New(sessionsDir)
	if err != nil {
		t.Fatalf("session.New() error: %v", err)
	}
	sess.RecordMessage("user", "test message", nil, "")
	sessionID := sess.ID()
	sess.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/chat/{id}", srv.handleChat)

	// Test with valid session ID
	req := httptest.NewRequest("GET", "/chat/"+sessionID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleChat() status = %d, want %d", rec.Code, http.StatusOK)
		t.Logf("body: %s", rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Majordomo") {
		t.Errorf("handleChat() body does not contain 'Majordomo': %s", body)
	}

	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("handleChat() body is not HTML: %s", body)
	}

	// Test with invalid session ID (should return 404)
	req2 := httptest.NewRequest("GET", "/chat/invalid-session-id", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("handleChat() with invalid ID status = %d, want %d", rec2.Code, http.StatusNotFound)
	}

	// Test with empty session ID: /chat/ doesn't match /chat/{id} route
	// and falls through to root handler which returns 404.
	req3 := httptest.NewRequest("GET", "/chat/", nil)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)

	// Note: /chat/ falls through to root handler returning 404.
	// This is a known limitation — the server doesn't have a /chat/ route.
	if rec3.Code != http.StatusNotFound {
		t.Errorf("handleChat() with empty ID status = %d, want %d (404 from root handler)", rec3.Code, http.StatusNotFound)
	}
}

func TestHandleChatWithMessages(t *testing.T) {
	srv, sessionsDir := setupTestServer(t)

	// Create a session with messages
	sess, err := session.New(sessionsDir)
	if err != nil {
		t.Fatalf("session.New() error: %v", err)
	}
	sess.RecordMessage("user", "What is Go?", nil, "")
	sess.RecordMessage("assistant", "Go is a programming language.", nil, "")
	sessionID := sess.ID()
	sess.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/chat/{id}", srv.handleChat)

	req := httptest.NewRequest("GET", "/chat/"+sessionID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleChat() status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "What is Go?") {
		t.Errorf("handleChat() body does not contain user message: %s", body)
	}
	if !strings.Contains(body, "Go is a programming language") {
		t.Errorf("handleChat() body does not contain assistant message: %s", body)
	}
}

func TestHandleChatNonExistentSession(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/chat/{id}", srv.handleChat)

	// Test with a session ID that doesn't exist
	req := httptest.NewRequest("GET", "/chat/nonexistent-session-id", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("handleChat() with non-existent session status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleAPIConfig(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", srv.handleConfig)

	// Test GET
	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleConfig GET status = %d, want %d", rec.Code, http.StatusOK)
	}

	var cfg config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("failed to parse config response: %v", err)
	}

	if cfg.LLM.Provider != "ollama" {
		t.Errorf("expected provider 'ollama', got '%s'", cfg.LLM.Provider)
	}

	// Test POST
	newCfg := config.Config{
		LLM: config.LLMConfig{
			Provider: "lmstudio",
			Model:    "llama3.2",
			URL:      "http://localhost:1234",
		},
	}
	body, err := json.Marshal(newCfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	req2 := httptest.NewRequest("POST", "/api/config", io.NopCloser(strings.NewReader(string(body))))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("handleConfig POST status = %d, want %d", rec2.Code, http.StatusOK)
		t.Logf("body: %s", rec2.Body.String())
	}
}

func TestHandleAPISessions(t *testing.T) {
	srv, sessionsDir := setupTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", srv.handleSessions)

	// Test GET (empty)
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleSessions GET status = %d, want %d", rec.Code, http.StatusOK)
	}

	var summaries []session.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summaries); err != nil {
		t.Fatalf("failed to parse sessions response: %v", err)
	}

	if len(summaries) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(summaries))
	}

	// Create a session
	sess, err := session.New(sessionsDir)
	if err != nil {
		t.Fatalf("session.New() error: %v", err)
	}
	sess.RecordMessage("user", "hello", nil, "")
	sess.Close()

	// Test GET (with session)
	req2 := httptest.NewRequest("GET", "/api/sessions", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("handleSessions GET status = %d, want %d", rec2.Code, http.StatusOK)
	}

	if err := json.Unmarshal(rec2.Body.Bytes(), &summaries); err != nil {
		t.Fatalf("failed to parse sessions response: %v", err)
	}

	if len(summaries) != 1 {
		t.Errorf("expected 1 session, got %d", len(summaries))
	}

	// Test POST (create session)
	req3 := httptest.NewRequest("POST", "/api/sessions", nil)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Errorf("handleSessions POST status = %d, want %d", rec3.Code, http.StatusOK)
	}

	var created map[string]string
	if err := json.Unmarshal(rec3.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to parse create session response: %v", err)
	}

	if created["id"] == "" {
		t.Error("expected non-empty session ID in response")
	}
}

func TestHandleSessionHistory(t *testing.T) {
	srv, sessionsDir := setupTestServer(t)

	// Create a session with messages
	sess, err := session.New(sessionsDir)
	if err != nil {
		t.Fatalf("session.New() error: %v", err)
	}
	sess.RecordMessage("user", "test question", nil, "")
	sess.RecordMessage("assistant", "test answer", nil, "")
	sessionID := sess.ID()
	sess.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions/{id}/history", srv.handleSessionHistory)

	// Test GET history
	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/history", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleSessionHistory status = %d, want %d", rec.Code, http.StatusOK)
		t.Logf("body: %s", rec.Body.String())
	}

	var events []session.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("failed to parse history response: %v", err)
	}

	if len(events) < 2 {
		t.Errorf("expected at least 2 events (header + 2 messages), got %d", len(events))
	}

	// Test with invalid session ID
	req2 := httptest.NewRequest("GET", "/api/sessions/invalid-id/history", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("handleSessionHistory with invalid ID status = %d, want %d", rec2.Code, http.StatusNotFound)
	}
}

func TestStaticFilesServed(t *testing.T) {
	_, _ = setupTestServer(t)

	mux := http.NewServeMux()
	mux.Handle("/styles.css", http.FileServer(http.FS(webFS)))
	mux.Handle("/app.js", http.FileServer(http.FS(webFS)))

	// Test styles.css
	req := httptest.NewRequest("GET", "/styles.css", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("styles.css status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.Len() == 0 {
		t.Error("styles.css response body is empty")
	}

	// Test app.js
	req2 := httptest.NewRequest("GET", "/app.js", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("app.js status = %d, want %d", rec2.Code, http.StatusOK)
	}

	if rec2.Body.Len() == 0 {
		t.Error("app.js response body is empty")
	}
}

func TestRootNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleRoot)

	// Requesting a path other than "/" should return 404
	req := httptest.NewRequest("GET", "/some/other/path", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("handleRoot() with non-root path status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
