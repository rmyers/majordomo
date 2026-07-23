package server

import (
	"encoding/json"
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
	srv := New(cfg, svc, agent, llmManager)

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

func TestHandleSettings(t *testing.T) {
	srv, configDir := setupTestServer(t)

	req := httptest.NewRequest("GET", "/settings", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleSettings GET status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "LLM Configuration") {
		t.Errorf("handleSettings GET missing 'LLM Configuration': %s", body)
	}
	if !strings.Contains(body, `name="provider"`) {
		t.Error("handleSettings GET missing provider field")
	}
	if !strings.Contains(body, `name="model"`) {
		t.Error("handleSettings GET missing model field")
	}
	if !strings.Contains(body, `name="url"`) {
		t.Error("handleSettings GET missing url field")
	}
	if !strings.Contains(body, `name="apiKey"`) {
		t.Error("handleSettings GET missing apiKey field")
	}
	if !strings.Contains(body, `name="host"`) {
		t.Error("handleSettings GET missing host field")
	}
	if !strings.Contains(body, `name="port"`) {
		t.Error("handleSettings GET missing port field")
	}

	formData := "provider=local&model=llama3.2&url=http://localhost:11434&apiKey=&host=localhost&port=3636"
	req2 := httptest.NewRequest("POST", "/settings", strings.NewReader(formData))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("handleSettings POST status = %d, want %d", rec2.Code, http.StatusOK)
		t.Logf("body: %s", rec2.Body.String())
	}

	if !strings.Contains(rec2.Body.String(), "Configuration saved") {
		t.Errorf("handleSettings POST missing success message: %s", rec2.Body.String())
	}

	configFile := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config.json not found in temp dir: %v", err)
	}

	var saved struct {
		LLM struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			URL      string `json:"url"`
			APIKey   string `json:"apiKey,omitempty"`
		} `json:"llm"`
		Server struct {
			Host string `json:"host"`
			Port string `json:"port"`
		} `json:"server"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("failed to parse saved config: %v", err)
	}

	if saved.LLM.Provider != "local" {
		t.Errorf("expected saved provider 'local', got '%s'", saved.LLM.Provider)
	}
	if saved.LLM.Model != "llama3.2" {
		t.Errorf("expected saved model 'llama3.2', got '%s'", saved.LLM.Model)
	}
	if saved.LLM.URL != "http://localhost:11434" {
		t.Errorf("expected saved URL 'http://localhost:11434', got '%s'", saved.LLM.URL)
	}
	if saved.Server.Host != "localhost" {
		t.Errorf("expected saved host 'localhost', got '%s'", saved.Server.Host)
	}
	if saved.Server.Port != "3636" {
		t.Errorf("expected saved port '3636', got '%s'", saved.Server.Port)
	}
}

func TestHandleSettingsInvalidPort(t *testing.T) {
	srv, _ := setupTestServer(t)

	formData := "provider=local&model=llama3.2&url=http://localhost:11434&apiKey=&host=localhost&port=abc"
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(formData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleSettings POST with invalid port status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Port must be a valid number") {
		t.Errorf("handleSettings POST with invalid port missing error message: %s", body)
	}
}

func TestHandleSettingsEmptyURL(t *testing.T) {
	srv, _ := setupTestServer(t)

	formData := "provider=local&model=llama3.2&url=&apiKey=&host=localhost&port=3636"
	req := httptest.NewRequest("POST", "/settings", strings.NewReader(formData))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("handleSettings POST with empty URL status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "URL is required") {
		t.Errorf("handleSettings POST with empty URL missing error message: %s", body)
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

func TestSendEventHTML(t *testing.T) {
	srv, _ := setupTestServer(t)

	cases := []struct {
		name  string
		html  string
		lines []string
	}{
		{
			name: "simple paragraph",
			html: "<p>I'll keep it</p>\n",
			lines: []string{
				"event: message",
				"data: <p>I'll keep it</p>",
				"",
				"",
			},
		},
		{
			name: "shorter paragraph",
			html: "<p>I'll keep it short and sweet one</p>\n",
			lines: []string{
				"event: message",
				"data: <p>I'll keep it short and sweet one</p>",
				"",
				"",
			},
		},
		{
			name: "paragraph with empty code block",
			html: "<p>I'll keep it short and sweet one last time:</p>\n<pre><code></code></pre>\n",
			lines: []string{
				"event: message",
				"data: <p>I'll keep it short and sweet one last time:</p>",
				"data: <pre><code></code></pre>",
				"",
				"",
			},
		},
		{
			name: "paragraph with python code",
			html: "<p>I'll keep it short and sweet one last time:</p>\n<pre><code class=\"language-python\">import pytest\n</code></pre>\n",
			lines: []string{
				"event: message",
				"data: <p>I'll keep it short and sweet one last time:</p>",
				"data: <pre><code class=\"language-python\">import pytest",
				"data: </code></pre>",
				"",
				"",
			},
		},
		{
			name: "python code with blank line",
			html: "<p>I'll keep it short and sweet one last time:</p>\n<pre><code class=\"language-python\">import pytest\n\ndef is_even\n</code></pre>\n",
			lines: []string{
				"event: message",
				"data: <p>I'll keep it short and sweet one last time:</p>",
				"data: <pre><code class=\"language-python\">import pytest",
				"data: ",
				"data: def is_even",
				"data: </code></pre>",
				"",
				"",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.sendEventHTML(rec, "message", tc.html)

			got := strings.Split(rec.Body.String(), "\n")
			if len(got) != len(tc.lines) {
				t.Fatalf("got %d lines, want %d\nbody:\n%s", len(got), len(tc.lines), rec.Body.String())
			}
			for i, want := range tc.lines {
				if got[i] != want {
					t.Errorf("line %d:\n  got: %q\n  want: %q", i, got[i], want)
				}
			}
		})
	}
}
