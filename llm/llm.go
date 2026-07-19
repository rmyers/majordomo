package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"sync/atomic"

	"github.com/rmyers/majordomo/config"
)

// Manager holds the current client and supports runtime swapping
type Manager struct {
	client atomic.Pointer[Client]
}

func NewManager() *Manager {
	return &Manager{}
}

// Get returns the current client. Panics if not initialized.
func (m *Manager) Get() Client {
	c := m.client.Load()
	if c == nil {
		panic("LLM Manager not initialized. Call SetInitial or Refresh first.")
	}
	return *c
}

// SetInitial creates the first client (call from main)
func (m *Manager) SetInitial(cfg *config.Config, modelOverride string) error {
	return m.Refresh(cfg, modelOverride)
}

// Refresh validates, creates, and atomically swaps the client
func (m *Manager) Refresh(cfg *config.Config, modelOverride string) error {
	newClient, err := New(cfg, modelOverride)
	if err != nil {
		return fmt.Errorf("failed to validate/create LLM client: %w", err)
	}

	// Atomically swap. If old client exists, close it to free connections.
	if old := m.client.Swap(&newClient); old != nil {
		if closer, ok := (*old).(io.Closer); ok {
			_ = closer.Close()
		}
	}
	return nil
}

// Message represents a single message in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls is populated when the LLM requests tool use.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID identifies which tool result this message refers to.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCall represents a request from the LLM to invoke a tool.
type ToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function ToolFunctionArg `json:"function"`
}

// ToolFunctionArg holds the parsed arguments for a tool call.
type ToolFunctionArg struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool represents a tool the agent can use.
type Tool struct {
	Name        string
	Description string
	Params      map[string]ParamSchema
}

// ParamSchema describes a single parameter for a tool.
type ParamSchema struct {
	Type        string
	Description string
	Required    bool
}

// Client speaks to an OpenAI-compatible /v1/chat/completions API.
type Client interface {
	// Chat sends messages and returns the full assistant response.
	Chat(ctx context.Context, messages []Message) (*Message, error)
	// StreamChat sends messages and calls handler for each text chunk.
	// When the response contains tool_calls, the handler receives a nil text
	// and a ToolCalls slice. When done, handler is called with text="" and nil ToolCalls.
	StreamChat(ctx context.Context, messages []Message, handler func(text string, toolCalls []ToolCall)) error
	// Name returns a human-readable identifier for this client.
	Name() string
	// Close the connection
	Close() error
}

// Remote talks to any OpenAI-compatible API.
type Remote struct {
	client  *http.Client
	baseURL string
	model   string
	apiKey  string
}

func (r *Remote) Name() string {
	return fmt.Sprintf("%s/%s", r.model, r.baseURL)
}

// Chat sends messages and returns the full assistant response.
func (r *Remote) Chat(ctx context.Context, messages []Message) (*Message, error) {
	slog.Debug("Chat request", "model", r.model, "url", r.baseURL, "messageCount", len(messages))

	body, err := json.Marshal(map[string]any{
		"model":    r.model,
		"messages": messages,
		"stream":   false,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	slog.Debug("sending request", "url", r.baseURL+"/v1/chat/completions", "hasAPIKey", r.apiKey != "")

	resp, err := r.client.Do(req)
	if err != nil {
		slog.Error("HTTP request failed", "url", r.baseURL, "error", err)
		return nil, fmt.Errorf("calling %s: %w", r.baseURL, err)
	}
	defer resp.Body.Close()

	slog.Debug("received response", "statusCode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("non-200 response", "statusCode", resp.StatusCode, "body", string(bodyBytes))
		return nil, fmt.Errorf("%s returned %d: %s", r.baseURL, resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if len(result.Choices) == 0 {
		slog.Warn("empty response from LLM")
		return nil, fmt.Errorf("empty response from %s", r.baseURL)
	}

	slog.Debug("Chat response", "hasToolCalls", len(result.Choices[0].Message.ToolCalls) > 0, "contentLen", len(result.Choices[0].Message.Content))
	return &result.Choices[0].Message, nil
}

// StreamChat sends messages and streams the response via the handler.
func (r *Remote) StreamChat(ctx context.Context, messages []Message, handler func(text string, toolCalls []ToolCall)) error {
	slog.Debug("StreamChat request", "model", r.model, "url", r.baseURL, "messageCount", len(messages))

	body, err := json.Marshal(map[string]any{
		"model":    r.model,
		"messages": messages,
		"stream":   true,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	slog.Debug("sending streaming request", "url", r.baseURL+"/v1/chat/completions", "hasAPIKey", r.apiKey != "")

	resp, err := r.client.Do(req)
	if err != nil {
		slog.Error("HTTP request failed", "url", r.baseURL, "error", err)
		return fmt.Errorf("calling %s: %w", r.baseURL, err)
	}
	defer resp.Body.Close()

	slog.Debug("received streaming response", "statusCode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("non-200 streaming response", "statusCode", resp.StatusCode, "body", string(bodyBytes))
		return fmt.Errorf("%s returned %d: %s", r.baseURL, resp.StatusCode, string(bodyBytes))
	}

	// Parse SSE stream
	var fullText string
	var fullToolCalls []ToolCall

	slog.Debug("parsing SSE stream")
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			slog.Error("error reading SSE stream", "error", err)
			return fmt.Errorf("reading SSE stream: %w", err)
		}
		// SSE data lines start with "data: "
		dataLine, ok := tok.(string)
		if !ok {
			continue
		}
		if dataLine == "[DONE]" {
			slog.Debug("stream complete", "totalTextLen", len(fullText), "totalToolCalls", len(fullToolCalls))
			break
		}

		var chunk struct {
			Choices []struct {
				Delta Message `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(dataLine), &chunk); err != nil {
			// Skip malformed chunks (common with some providers sending empty data)
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		// Accumulate text content
		if delta.Content != "" {
			fullText += delta.Content
		}

		// Accumulate tool calls
		if len(delta.ToolCalls) > 0 {
			slog.Debug("received tool call delta", "toolCallCount", len(delta.ToolCalls))
			for _, tc := range delta.ToolCalls {
				found := false
				for i := range fullToolCalls {
					if fullToolCalls[i].ID == tc.ID {
						// Append to existing tool call
						if tc.Function.Name != "" {
							fullToolCalls[i].Function.Name += tc.Function.Name
							slog.Debug("accumulated tool call", "toolName", fullToolCalls[i].Function.Name, "argLen", len(fullToolCalls[i].Function.Arguments))
						}
						if tc.Function.Arguments != "" {
							fullToolCalls[i].Function.Arguments += tc.Function.Arguments
						}
						found = true
						break
					}
				}
				if !found {
					// New tool call
					fullToolCalls = append(fullToolCalls, tc)
					slog.Debug("new tool call", "toolName", tc.Function.Name, "callID", tc.ID)
				}
			}
		}

		// Emit the delta to the handler
		handler(delta.Content, delta.ToolCalls)
	}

	// Final emit: assembled tool calls as completed
	if len(fullToolCalls) > 0 {
		slog.Debug("stream complete with tool calls", "toolCount", len(fullToolCalls))
		handler("", fullToolCalls)
	} else {
		slog.Debug("stream complete with text", "textLen", len(fullText))
	}

	return nil
}

func (r *Remote) Close() error {
	// Best practice: close idle connections instead of destroying the client
	r.client.CloseIdleConnections()
	return nil
}

// New creates an LLM client based on config.
func New(cfg *config.Config, modelOverride string) (Client, error) {
	model := cfg.LLM.Model
	if modelOverride != "" {
		model = modelOverride
	}

	if cfg.LLM.URL == "" {
		return nil, fmt.Errorf("URL is required (set url in config)")
	}

	return &Remote{
		client:  &http.Client{Timeout: 5 * time.Minute},
		baseURL: cfg.LLM.URL,
		model:   model,
		apiKey:  cfg.LLM.APIKey,
	}, nil
}
