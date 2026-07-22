package templates

import (
	"strings"
	"testing"

	"github.com/rmyers/majordomo/session"
)

func TestHome(t *testing.T) {
	var buf strings.Builder
	params := HomeParams{
		Sessions:  []session.Summary{},
		SessionID: "",
	}
	err := Home(&buf, params)
	if err != nil {
		t.Fatalf("Home() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Home() output is not valid HTML")
	}
	if !strings.Contains(body, "Majordomo") {
		t.Error("Home() output missing 'Majordomo'")
	}
	if !strings.Contains(body, "Welcome to Majordomo") {
		t.Error("Home() output missing welcome text")
	}
	if !strings.Contains(body, "<textarea") {
		t.Error("Home() output missing textarea input")
	}
	if !strings.Contains(body, "Ask me anything") {
		t.Error("Home() output missing placeholder text")
	}
}

func TestHomeWithSessions(t *testing.T) {
	var buf strings.Builder
	params := HomeParams{
		Sessions: []session.Summary{
			{ID: "abc123", Title: "Test Session", Timestamp: "2025-01-01T00:00:00Z"},
			{ID: "def456", Title: "Another Session", Timestamp: "2025-01-02T00:00:00Z"},
		},
		SessionID: "abc123",
	}
	err := Home(&buf, params)
	if err != nil {
		t.Fatalf("Home() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "Test Session") {
		t.Error("Home() output missing session title")
	}
	if !strings.Contains(body, "Another Session") {
		t.Error("Home() output missing second session title")
	}
	if !strings.Contains(body, "abc123") {
		t.Error("Home() output missing session ID")
	}
}

func TestHomeEmptySessionsList(t *testing.T) {
	var buf strings.Builder
	params := HomeParams{
		Sessions:  []session.Summary{},
		SessionID: "",
	}
	err := Home(&buf, params)
	if err != nil {
		t.Fatalf("Home() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "No sessions yet") {
		t.Error("Home() output missing empty state text")
	}
}

func TestChat(t *testing.T) {
	var buf strings.Builder
	params := ChatParams{
		Sessions:  []session.Summary{},
		SessionID: "test-session-1",
		Messages:  []ChatMessage{},
	}
	err := Chat(&buf, params)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Chat() output is not valid HTML")
	}
	if !strings.Contains(body, "test-session-1") {
		t.Error("Chat() output missing session ID")
	}
	if !strings.Contains(body, "Ask me Almost anything") {
		t.Error("Chat() output missing chat placeholder")
	}
}

func TestChatWithMessages(t *testing.T) {
	var buf strings.Builder
	messages := []ChatMessage{
		{Role: "user", Content: "Hello, world!"},
		{Role: "assistant", Content: "Hi there! How can I help?"},
	}
	params := ChatParams{
		Sessions:  []session.Summary{},
		SessionID: "session-abc",
		Messages:  messages,
	}
	err := Chat(&buf, params)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "Hello, world!") {
		t.Error("Chat() output missing user message")
	}
	if !strings.Contains(body, "Hi there! How can I help?") {
		t.Error("Chat() output missing assistant message")
	}
	if !strings.Contains(body, `class="message user"`) {
		t.Error("Chat() output missing user message class")
	}
	if !strings.Contains(body, `class="message assistant"`) {
		t.Error("Chat() output missing assistant message class")
	}
}

func TestChatEmptyMessages(t *testing.T) {
	var buf strings.Builder
	params := ChatParams{
		Sessions:  []session.Summary{},
		SessionID: "session-xyz",
		Messages:  []ChatMessage{},
	}
	err := Chat(&buf, params)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, `id="messages"`) {
		t.Error("Chat() output missing messages container")
	}
}

func TestSettings(t *testing.T) {
	var buf strings.Builder
	params := SettingsParams{
		Sessions:  []session.Summary{},
		SessionID: "",
		Provider:  "local",
		Model:     "llama3.2",
		URL:       "http://localhost:11434",
		APIKey:    "",
		Host:      "localhost",
		Port:      "3636",
		Success:   "",
		Error:     "",
	}
	err := Settings(&buf, params)
	if err != nil {
		t.Fatalf("Settings() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("Settings() output is not valid HTML")
	}
	if !strings.Contains(body, "LLM Configuration") {
		t.Error("Settings() output missing configuration heading")
	}
	if !strings.Contains(body, `name="provider"`) {
		t.Error("Settings() output missing provider field")
	}
	if !strings.Contains(body, `name="model"`) {
		t.Error("Settings() output missing model field")
	}
	if !strings.Contains(body, `name="url"`) {
		t.Error("Settings() output missing url field")
	}
	if strings.Contains(body, `value="local"`) {
		// Provider value should be pre-populated
	}
	if !strings.Contains(body, "Save") {
		t.Error("Settings() output missing Save button")
	}
}

func TestSettingsWithSuccessMessage(t *testing.T) {
	var buf strings.Builder
	params := SettingsParams{
		Sessions:  []session.Summary{},
		SessionID: "",
		Success:   "Configuration saved",
	}
	err := Settings(&buf, params)
	if err != nil {
		t.Fatalf("Settings() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "Configuration saved") {
		t.Error("Settings() output missing success message")
	}
	if !strings.Contains(body, `class="status-message success"`) {
		t.Error("Settings() output missing success message class")
	}
}

func TestSettingsWithError(t *testing.T) {
	var buf strings.Builder
	params := SettingsParams{
		Sessions: []session.Summary{},
		Error:    "Port must be a valid number",
	}
	err := Settings(&buf, params)
	if err != nil {
		t.Fatalf("Settings() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "Port must be a valid number") {
		t.Error("Settings() output missing error message")
	}
	if !strings.Contains(body, `class="status-message error"`) {
		t.Error("Settings() output missing error message class")
	}
}

func TestSettingsWithNonLocalHost(t *testing.T) {
	var buf strings.Builder
	params := SettingsParams{
		Sessions: []session.Summary{},
		Host:     "0.0.0.0",
		Port:     "3636",
	}
	err := Settings(&buf, params)
	if err != nil {
		t.Fatalf("Settings() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "requires a server restart") {
		t.Error("Settings() output missing restart warning for non-localhost host")
	}
}

func TestSettingsWithLocalHostNoWarning(t *testing.T) {
	var buf strings.Builder
	params := SettingsParams{
		Sessions: []session.Summary{},
		Host:     "localhost",
		Port:     "3636",
	}
	err := Settings(&buf, params)
	if err != nil {
		t.Fatalf("Settings() error: %v", err)
	}

	body := buf.String()
	if strings.Contains(body, "requires a server restart") {
		t.Error("Settings() should not show restart warning for localhost host")
	}
}

func TestSettingsWithEmptySessionList(t *testing.T) {
	var buf strings.Builder
	params := SettingsParams{
		Sessions:  []session.Summary{},
		SessionID: "",
	}
	err := Settings(&buf, params)
	if err != nil {
		t.Fatalf("Settings() error: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "No sessions yet") {
		t.Error("Settings() output missing empty state in sidebar")
	}
}
