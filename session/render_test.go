package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderChatEventsEmpty(t *testing.T) {
	events := []Event{}
	result := RenderChatEvents(events, nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 events, got %d", len(result))
	}
}

func TestRenderChatEventsNonMessageEventsSkipped(t *testing.T) {
	sessionHeader := json.RawMessage(`{"type":"session","id":"abc123","timestamp":"2025-01-01T00:00:00Z"}`)

	events := []Event{
		{Type: "session", Message: &sessionHeader},
		{Type: "model_change", Model: "llama3.2"},
	}

	result := RenderChatEvents(events, nil)
	if len(result) != 0 {
		t.Fatalf("expected 0 events (non-message skipped), got %d", len(result))
	}
}

func TestRenderChatEventsUserOnly(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "user", Content: "Hello world"})
	rawMsg := json.RawMessage(msg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
	}

	result := RenderChatEvents(events, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Fatalf("expected role 'user', got '%s'", result[0].Role)
	}
	if !strings.Contains(string(result[0].Content), "<p>") {
		t.Error("expected markdown-rendered HTML for user content")
	}
}

func TestRenderChatEventsAssistantOnly(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "assistant", Content: "I can help with that."})
	rawMsg := json.RawMessage(msg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
	}

	result := RenderChatEvents(events, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Role != "assistant" {
		t.Fatalf("expected role 'assistant', got '%s'", result[0].Role)
	}
	if !strings.Contains(string(result[0].Content), "<p>") {
		t.Error("expected markdown-rendered HTML for assistant content")
	}
}

func TestRenderChatEventsAssistantWithToolCalls(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "assistant", Content: "Let me check the file.", ToolCalls: []ToolCall{{ID: "call1", Name: "read", Args: `{"path":"foo.go"}`}}})
	rawMsg := json.RawMessage(msg)

	resultJSON, _ := json.Marshal(ToolCallResult{Output: "package main\n\nfunc main() {}\n"})
	toolResultMsg, _ := json.Marshal(Message{Role: "tool", Content: "package main\n\nfunc main() {}", ToolCallID: "call1"})
	toolResultRaw := json.RawMessage(toolResultMsg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
		{Type: "message", Message: &toolResultRaw, ToolResult: (*json.RawMessage)(&resultJSON)},
	}

	toolList := []ToolInfo{
		{Name: "read", Summary: func(args string) string { return "read " + args }},
	}

	result := RenderChatEvents(events, toolList)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Role != "assistant" {
		t.Fatalf("expected role 'assistant', got '%s'", result[0].Role)
	}
	if len(result[0].Tools) != 1 {
		t.Fatalf("expected 1 tool section, got %d", len(result[0].Tools))
	}
	if result[0].Tools[0].Name != "read" {
		t.Fatalf("expected tool name 'read', got '%s'", result[0].Tools[0].Name)
	}
	if result[0].Tools[0].IsErr {
		t.Error("expected IsErr=false for successful tool result")
	}
}

func TestRenderChatEventsToolErrorDetection(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call1", Name: "bash", Args: `{"cmd":"false"}`}}})
	rawMsg := json.RawMessage(msg)

	resultJSON, _ := json.Marshal(ToolCallResult{Output: "", Error: "command failed: exit status 1"})
	toolResultMsg, _ := json.Marshal(Message{Role: "tool", Content: "", ToolCallID: "call1"})
	toolResultRaw := json.RawMessage(toolResultMsg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
		{Type: "message", Message: &toolResultRaw, ToolResult: (*json.RawMessage)(&resultJSON)},
	}

	toolList := []ToolInfo{
		{Name: "bash", Summary: func(args string) string { return "bash" }},
	}

	result := RenderChatEvents(events, toolList)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Tools[0].IsErr != true {
		t.Error("expected IsErr=true for tool with error")
	}
}

func TestRenderChatEventsMixedEventsInOrder(t *testing.T) {
	userMsg, _ := json.Marshal(Message{Role: "user", Content: "Read foo.go"})
	userRaw := json.RawMessage(userMsg)

	assistantMsg, _ := json.Marshal(Message{Role: "assistant", Content: "Let me check.", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Args: `{"path":"foo.go"}`}}})
	assistantRaw := json.RawMessage(assistantMsg)

	resultJSON, _ := json.Marshal(ToolCallResult{Output: "package main\n{}\n"})
	toolMsg, _ := json.Marshal(Message{Role: "tool", Content: "package main\n{}\n", ToolCallID: "c1"})
	toolRaw := json.RawMessage(toolMsg)

	finalMsg, _ := json.Marshal(Message{Role: "assistant", Content: "The file defines a main package."})
	finalRaw := json.RawMessage(finalMsg)

	events := []Event{
		{Type: "message", Message: &userRaw},
		{Type: "message", Message: &assistantRaw},
		{Type: "message", Message: &toolRaw, ToolResult: (*json.RawMessage)(&resultJSON)},
		{Type: "message", Message: &finalRaw},
	}

	toolList := []ToolInfo{
		{Name: "read", Summary: func(args string) string { return "read foo.go" }},
	}

	result := RenderChatEvents(events, toolList)
	if len(result) != 3 {
		t.Fatalf("expected 3 events, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Fatal("first event should be user")
	}
	if result[1].Role != "assistant" || len(result[1].Tools) != 1 {
		t.Fatal("second event should be assistant with tool")
	}
	if result[2].Role != "assistant" || result[2].Content == "" {
		t.Fatal("third event should be assistant with content")
	}
}

func TestRenderChatEventsMalformedJSONSkipped(t *testing.T) {
	badJSON := json.RawMessage([]byte(`not valid json`))
	validMsg, _ := json.Marshal(Message{Role: "user", Content: "Hello"})
	validRaw := json.RawMessage(validMsg)

	events := []Event{
		{Type: "message", Message: &badJSON},
		{Type: "message", Message: &validRaw},
	}

	result := RenderChatEvents(events, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 event (malformed skipped), got %d", len(result))
	}
}

func TestRenderChatEventsAssistantToolOnlyNoContent(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Args: `{"cmd":"ls"}`}}})
	rawMsg := json.RawMessage(msg)

	resultJSON, _ := json.Marshal(ToolCallResult{Output: "file1 file2\n"})
	toolMsg, _ := json.Marshal(Message{Role: "tool", Content: "file1 file2\n", ToolCallID: "c1"})
	toolRaw := json.RawMessage(toolMsg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
		{Type: "message", Message: &toolRaw, ToolResult: (*json.RawMessage)(&resultJSON)},
	}

	toolList := []ToolInfo{
		{Name: "bash", Summary: func(args string) string { return "bash" }},
	}

	result := RenderChatEvents(events, toolList)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Content != "" {
		t.Error("expected empty content for assistant with no text")
	}
	if len(result[0].Tools) != 1 {
		t.Fatal("expected 1 tool section")
	}
}

func TestRenderChatEventsMarkdownRendering(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "user", Content: "Hello **bold** and *italic*"})
	rawMsg := json.RawMessage(msg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
	}

	result := RenderChatEvents(events, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	html := string(result[0].Content)
	if !strings.Contains(html, "<strong>") {
		t.Error("expected <strong> tag for bold markdown")
	}
	if !strings.Contains(html, "<em>") {
		t.Error("expected <em> tag for italic markdown")
	}
}

func TestRenderChatEventsUnknownToolSummaryFallback(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "unknown_tool", Args: `{}`}}})
	rawMsg := json.RawMessage(msg)

	resultJSON, _ := json.Marshal(ToolCallResult{Output: "done"})
	toolMsg, _ := json.Marshal(Message{Role: "tool", Content: "done", ToolCallID: "c1"})
	toolRaw := json.RawMessage(toolMsg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
		{Type: "message", Message: &toolRaw, ToolResult: (*json.RawMessage)(&resultJSON)},
	}

	// Empty tool list — should fall back to tool name
	result := RenderChatEvents(events, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if result[0].Tools[0].Args != "unknown_tool" {
		t.Fatalf("expected fallback args to be tool name, got '%s'", result[0].Tools[0].Args)
	}
}

func TestRenderChatEventsToolResultNotFound(t *testing.T) {
	msg, _ := json.Marshal(Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Args: `{}`}}})
	rawMsg := json.RawMessage(msg)

	events := []Event{
		{Type: "message", Message: &rawMsg},
	}

	toolList := []ToolInfo{
		{Name: "read", Summary: func(args string) string { return "read" }},
	}

	result := RenderChatEvents(events, toolList)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if len(result[0].Tools) != 1 {
		t.Fatal("expected 1 tool section even without matching result")
	}
	if result[0].Tools[0].Result != "" {
		t.Error("expected empty result when no tool result event exists")
	}
}

func TestRenderChatEventsPreservesOrder(t *testing.T) {
	messages := []string{"First user", "Second user", "Third user"}
	var rawMessages []json.RawMessage
	for _, m := range messages {
		msg, _ := json.Marshal(Message{Role: "user", Content: m})
		rawMessages = append(rawMessages, json.RawMessage(msg))
	}

	events := make([]Event, len(rawMessages))
	for i, rm := range rawMessages {
		events[i] = Event{Type: "message", Message: &rm}
	}

	result := RenderChatEvents(events, nil)
	for i, expected := range messages {
		if !strings.Contains(string(result[i].Content), expected) {
			t.Errorf("event %d: expected to contain '%s'", i, expected)
		}
	}
}

func TestRenderChatEventsMultipleToolCalls(t *testing.T) {
	msg, _ := json.Marshal(Message{
		Role: "assistant", Content: "",
		ToolCalls: []ToolCall{
			{ID: "c1", Name: "read", Args: `{"path":"a.go"}`},
			{ID: "c2", Name: "read", Args: `{"path":"b.go"}`},
		},
	})
	rawMsg := json.RawMessage(msg)

	result1JSON, _ := json.Marshal(ToolCallResult{Output: "package a\n"})
	result2JSON, _ := json.Marshal(ToolCallResult{Output: "package b\n"})
	toolMsg1, _ := json.Marshal(Message{Role: "tool", Content: "package a\n", ToolCallID: "c1"})
	toolMsg2, _ := json.Marshal(Message{Role: "tool", Content: "package b\n", ToolCallID: "c2"})
	toolRaw1 := json.RawMessage(toolMsg1)
	toolRaw2 := json.RawMessage(toolMsg2)

	events := []Event{
		{Type: "message", Message: &rawMsg},
		{Type: "message", Message: &toolRaw1, ToolResult: (*json.RawMessage)(&result1JSON)},
		{Type: "message", Message: &toolRaw2, ToolResult: (*json.RawMessage)(&result2JSON)},
	}

	toolList := []ToolInfo{
		{Name: "read", Summary: func(args string) string { return "read " + args }},
	}

	result := RenderChatEvents(events, toolList)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}
	if len(result[0].Tools) != 2 {
		t.Fatalf("expected 2 tool sections, got %d", len(result[0].Tools))
	}
}
