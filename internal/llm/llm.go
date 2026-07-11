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

	"github.com/julython/majordomo/internal/config"
)

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
}

// Remote talks to any OpenAI-compatible API (ollama, llama.cpp, lmstudio, etc.).
type Remote struct {
	client   *http.Client
	baseURL  string
	model    string
	provider string
	apiKey   string
}

func (r *Remote) Name() string {
	return fmt.Sprintf("%s/%s", r.provider, r.model)
}

// Chat sends messages and returns the full assistant response.
func (r *Remote) Chat(ctx context.Context, messages []Message) (*Message, error) {
	slog.Debug("Chat request", "provider", r.provider, "model", r.model, "messageCount", len(messages))

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
		slog.Error("HTTP request failed", "provider", r.provider, "url", r.baseURL, "error", err)
		return nil, fmt.Errorf("calling %s at %s: %w", r.provider, r.baseURL, err)
	}
	defer resp.Body.Close()

	slog.Debug("received response", "provider", r.provider, "statusCode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("non-200 response", "provider", r.provider, "statusCode", resp.StatusCode, "body", string(bodyBytes))
		return nil, fmt.Errorf("%s returned %d: %s", r.provider, resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("failed to decode response", "provider", r.provider, "error", err)
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if len(result.Choices) == 0 {
		slog.Warn("empty response from LLM", "provider", r.provider)
		return nil, fmt.Errorf("empty response from %s", r.provider)
	}

	slog.Debug("Chat response", "provider", r.provider, "hasToolCalls", len(result.Choices[0].Message.ToolCalls) > 0, "contentLen", len(result.Choices[0].Message.Content))
	return &result.Choices[0].Message, nil
}

// StreamChat sends messages and streams the response via the handler.
func (r *Remote) StreamChat(ctx context.Context, messages []Message, handler func(text string, toolCalls []ToolCall)) error {
	slog.Debug("StreamChat request", "provider", r.provider, "model", r.model, "messageCount", len(messages))

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
		slog.Error("HTTP request failed", "provider", r.provider, "url", r.baseURL, "error", err)
		return fmt.Errorf("calling %s at %s: %w", r.provider, r.baseURL, err)
	}
	defer resp.Body.Close()

	slog.Debug("received streaming response", "provider", r.provider, "statusCode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("non-200 streaming response", "provider", r.provider, "statusCode", resp.StatusCode, "body", string(bodyBytes))
		return fmt.Errorf("%s returned %d: %s", r.provider, resp.StatusCode, string(bodyBytes))
	}

	// Parse SSE stream
	var fullText string
	var fullToolCalls []ToolCall

	slog.Debug("parsing SSE stream", "provider", r.provider)
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			slog.Error("error reading SSE stream", "provider", r.provider, "error", err)
			return fmt.Errorf("reading SSE stream: %w", err)
		}
		// SSE data lines start with "data: "
		dataLine, ok := tok.(string)
		if !ok {
			continue
		}
		if dataLine == "[DONE]" {
			slog.Debug("stream complete", "provider", r.provider, "totalTextLen", len(fullText), "totalToolCalls", len(fullToolCalls))
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
			slog.Debug("received tool call delta", "provider", r.provider, "toolCallCount", len(delta.ToolCalls))
			for _, tc := range delta.ToolCalls {
				found := false
				for i := range fullToolCalls {
					if fullToolCalls[i].ID == tc.ID {
						// Append to existing tool call
						if tc.Function.Name != "" {
							fullToolCalls[i].Function.Name += tc.Function.Name
							slog.Debug("accumulated tool call", "provider", r.provider, "toolName", fullToolCalls[i].Function.Name, "argLen", len(fullToolCalls[i].Function.Arguments))
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
					slog.Debug("new tool call", "provider", r.provider, "toolName", tc.Function.Name, "callID", tc.ID)
				}
			}
		}

		// Emit the delta to the handler
		handler(delta.Content, delta.ToolCalls)
	}

	// Final emit: assembled tool calls as completed
	if len(fullToolCalls) > 0 {
		slog.Debug("stream complete with tool calls", "provider", r.provider, "toolCount", len(fullToolCalls))
		handler("", fullToolCalls)
	} else {
		slog.Debug("stream complete with text", "provider", r.provider, "textLen", len(fullText))
	}

	return nil
}

// New creates an LLM client based on config, with auto-detection for local servers.
func New(cfg *config.Config, modelOverride string) (Client, error) {
	model := cfg.LLM.Model
	if modelOverride != "" {
		model = modelOverride
	}

	switch cfg.LLM.Provider {
	case "ollama", "llamacpp", "lmstudio", "omlx":
		return &Remote{
			client:   &http.Client{Timeout: 5 * time.Minute},
			baseURL:  cfg.LLM.URL,
			model:    model,
			provider: cfg.LLM.Provider,
			apiKey:   cfg.LLM.APIKey,
		}, nil
	case "auto", "":
		if cfg.LLM.URL != "" {
			// User set a custom URL — use it without probing
			slog.Debug("using custom URL with auto provider", "url", cfg.LLM.URL)
			return &Remote{
				client:   &http.Client{Timeout: 5 * time.Minute},
				baseURL:  cfg.LLM.URL,
				model:    model,
				provider: "auto",
				apiKey:   cfg.LLM.APIKey,
			}, nil
		}
		return autoDetect(model, cfg.LLM.APIKey)
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", cfg.LLM.Provider)
	}
}

func autoDetect(model string, apiKey string) (Client, error) {
	candidates := []struct {
		name string
		url  string
	}{
		{"ollama", "http://localhost:11434"},
		{"omlx", "http://localhost:8000"},
		{"lmstudio", "http://localhost:1234"},
		{"llamacpp", "http://localhost:8080"},
	}

	probe := &http.Client{Timeout: 2 * time.Second}
	for _, c := range candidates {
		resp, err := probe.Get(c.url + "/v1/models")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			m := model
			if m == "" {
				m = "default"
			}
			slog.Debug("detected LLM", "provider", c.name, "url", c.url)
			return &Remote{
				client:   &http.Client{Timeout: 5 * time.Minute},
				baseURL:  c.url,
				model:    m,
				provider: c.name,
				apiKey:   apiKey,
			}, nil
		}
	}

	return nil, fmt.Errorf("no local LLM detected — start ollama, omlx, lmstudio, or llama.cpp")
}
