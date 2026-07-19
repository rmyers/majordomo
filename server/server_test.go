package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmyers/majordomo/agent"
	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/llm"
	"github.com/rmyers/majordomo/session"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	os.MkdirAll(sessionsDir, 0o755)

	cfg := config.New(tmpDir)
	cfg.SetModel("test-model")
	cfg.SetURL("http://localhost:11434")

	llmManager := llm.NewManager()
	llmManager.SetInitial(cfg, "")
	agent := agent.New(llmManager)
	svc := session.NewSessionService(cfg)
	srv := New(cfg, svc, agent)

	return srv, tmpDir
}

func TestHandleRoot(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

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
	srv, _ := setupTestServer(t)

	sess, err := srv.sessionSrv.CreateSession("test message")
	if err != nil {
		t.Fatalf("sessionSrv.CreateSession() error: %v", err)
	}
	sessionID := sess.ID()
	sess.Close()

	req := httptest.NewRequest("GET", "/chat/"+sessionID, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

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

	req2 := httptest.NewRequest("GET", "/chat/invalid-session-id", nil)
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("handleChat() with invalid ID status = %d, want %d", rec2.Code, http.StatusNotFound)
	}

	req3 := httptest.NewRequest("GET", "/chat/", nil)
	rec3 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusNotFound {
		t.Errorf("handleChat() with empty ID status = %d, want %d (404 from root handler)", rec3.Code, http.StatusNotFound)
	}
}

func TestHandleChatWithMessages(t *testing.T) {
	srv, _ := setupTestServer(t)

	sess, err := srv.sessionSrv.CreateSession("What is Go?")
	if err != nil {
		t.Fatalf("sessionSrv.CreateSession() error: %v", err)
	}
	sessionID := sess.ID()
	sess.Close()

	req := httptest.NewRequest("GET", "/chat/"+sessionID, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleChat() status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "What is Go?") {
		t.Errorf("handleChat() body does not contain user message: %s", body)
	}
}

func TestHandleChatNonExistentSession(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/chat/nonexistent-session-id", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("handleChat() with non-existent session status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleAPIConfig(t *testing.T) {
	srv, configDir := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleConfig GET status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Model  string `json:"model"`
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse config response: %v", err)
	}

	if resp.Model != "test-model" {
		t.Errorf("expected model 'test-model', got '%s'", resp.Model)
	}

	body, err := json.Marshal(map[string]string{
		"model":  "llama3.2",
		"url":    "http://localhost:1234",
		"apiKey": "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	req2 := httptest.NewRequest("POST", "/api/config", io.NopCloser(strings.NewReader(string(body))))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("handleConfig POST status = %d, want %d", rec2.Code, http.StatusOK)
		t.Logf("body: %s", rec2.Body.String())
	}

	// Verify config was written to the temp directory.
	configFile := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config.json not found in temp dir: %v", err)
	}

	var saved struct {
		LLM struct {
			Model  string `json:"model"`
			URL    string `json:"url"`
			APIKey string `json:"apiKey"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("failed to parse saved config: %v", err)
	}

	if saved.LLM.Model != "llama3.2" {
		t.Errorf("expected saved model 'llama3.2', got '%s'", saved.LLM.Model)
	}
	if saved.LLM.URL != "http://localhost:1234" {
		t.Errorf("expected saved URL 'http://localhost:1234', got '%s'", saved.LLM.URL)
	}
	if saved.LLM.APIKey != "test-api-key" {
		t.Errorf("expected saved apiKey 'test-api-key', got '%s'", saved.LLM.APIKey)
	}
}

func TestHandleAPISessions(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

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

	sess, err := srv.sessionSrv.CreateSession("hello")
	if err != nil {
		t.Fatalf("sessionSrv.CreateSession() error: %v", err)
	}
	sess.Close()

	req2 := httptest.NewRequest("GET", "/api/sessions", nil)
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("handleSessions GET status = %d, want %d", rec2.Code, http.StatusOK)
	}

	if err := json.Unmarshal(rec2.Body.Bytes(), &summaries); err != nil {
		t.Fatalf("failed to parse sessions response: %v", err)
	}

	if len(summaries) != 1 {
		t.Errorf("expected 1 session, got %d", len(summaries))
	}

	req3 := httptest.NewRequest("POST", "/api/sessions", nil)
	rec3 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec3, req3)

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
	srv, _ := setupTestServer(t)

	sess, err := srv.sessionSrv.CreateSession("test question")
	if err != nil {
		t.Fatalf("sessionSrv.CreateSession() error: %v", err)
	}
	// Record an assistant response
	events, _ := srv.sessionSrv.SessionHistory(sess.ID())
	_ = events
	sessionID := sess.ID()
	sess.Close()

	req := httptest.NewRequest("GET", "/api/sessions/"+sessionID+"/history", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleSessionHistory status = %d, want %d", rec.Code, http.StatusOK)
		t.Logf("body: %s", rec.Body.String())
	}

	var events2 []session.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &events2); err != nil {
		t.Fatalf("failed to parse history response: %v", err)
	}

	if len(events2) < 2 {
		t.Errorf("expected at least 2 events (header + user message), got %d", len(events2))
	}

	req2 := httptest.NewRequest("GET", "/api/sessions/invalid-id/history", nil)
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("handleSessionHistory with invalid ID status = %d, want %d", rec2.Code, http.StatusNotFound)
	}
}

func TestStaticFilesServed(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/styles.css", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("styles.css status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.Len() == 0 {
		t.Error("styles.css response body is empty")
	}

	req2 := httptest.NewRequest("GET", "/app.js", nil)
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("app.js status = %d, want %d", rec2.Code, http.StatusOK)
	}

	if rec2.Body.Len() == 0 {
		t.Error("app.js response body is empty")
	}
}

func TestRootNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/some/other/path", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("handleRoot() with non-root path status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
