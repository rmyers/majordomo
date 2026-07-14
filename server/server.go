package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rmyers/majordomo/agent"
	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/llm"
	"github.com/rmyers/majordomo/session"
)

//go:embed templates/*.html styles.css app.js
var webFS embed.FS

//go:embed templates/layout.html templates/index.html
var homeTemplates embed.FS

//go:embed templates/layout.html templates/chat.html
var chatTemplates embed.FS

var indexTemplate *template.Template
var chatTemplate *template.Template

// Server serves the web interface and SSE agent stream.
type Server struct {
	addr        string
	mu          sync.RWMutex
	cfg         *config.Config
	sessionsDir string
}

// New creates a Server listening on the given address with the specified sessions directory.
func New(addr string, sessionsDir string) *Server {
	return &Server{
		addr:        addr,
		sessionsDir: sessionsDir,
	}
}

// Run starts the HTTP server.
func (s *Server) Run(cfg *config.Config) error {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	// Parse templates
	var err error
	indexTemplate, err = template.ParseFS(homeTemplates, "templates/*.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}
	chatTemplate, err = template.ParseFS(chatTemplates, "templates/*.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	mux := http.NewServeMux()

	// Serve static assets
	mux.Handle("/styles.css", http.FileServer(http.FS(webFS)))
	mux.Handle("/app.js", http.FileServer(http.FS(webFS)))

	// Config API endpoints.
	mux.HandleFunc("/api/config", s.handleConfig)

	// Session list endpoint (GET returns summaries, POST creates a new session).
	mux.HandleFunc("/api/sessions", s.handleSessions)

	// Session history endpoint: GET /api/sessions/{id}/history.
	mux.HandleFunc("/api/sessions/{id}/history", s.handleSessionHistory)

	// Chat page: serves the web UI with a specific session.
	// Route: /chat/{id} → serve web UI with the specified session.
	mux.HandleFunc("/chat/{id}", s.handleChat)

	// SSE agent stream endpoint.
	mux.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		s.handleStream(w, r)
	})

	// Serve the web UI from the embedded filesystem.
	mux.HandleFunc("/", s.handleRoot)

	slog.Info("server starting", "addr", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

// getConfig returns the current config (read lock).
func (s *Server) getConfig() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// handleRoot handles GET / - shows the index page with session list in sidebar.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	summaries, err := session.List(s.sessionsDir)
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Sessions  []session.Summary
		SessionID string
	}{
		Sessions:  summaries,
		SessionID: "",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		slog.Error("failed to render template", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleConfig handles GET/POST for /api/config.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetConfig(w)
	case http.MethodPost:
		s.handlePostConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetConfig returns the current config as JSON.
func (s *Server) handleGetConfig(w http.ResponseWriter) {
	cfg := s.getConfig()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// handlePostConfig saves a new config from the request body.
func (s *Server) handlePostConfig(w http.ResponseWriter, r *http.Request) {
	var newCfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate provider
	if newCfg.LLM.Provider != "auto" && newCfg.LLM.Provider != "ollama" &&
		newCfg.LLM.Provider != "lmstudio" && newCfg.LLM.Provider != "llamacpp" &&
		newCfg.LLM.Provider != "omlx" && newCfg.LLM.Provider != "" {
		http.Error(w, "invalid provider", http.StatusBadRequest)
		return
	}

	// Save to disk
	if err := config.Save(&newCfg); err != nil {
		slog.Error("failed to save config", "error", err)
		http.Error(w, "failed to save config", http.StatusInternalServerError)
		return
	}

	// Update in-memory config
	s.mu.Lock()
	s.cfg = &newCfg
	s.mu.Unlock()

	slog.Info("config updated", "provider", newCfg.LLM.Provider, "model", newCfg.LLM.Model, "url", newCfg.LLM.URL)

	// Return saved config
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(newCfg)
}

// handleSessions handles GET (list) and POST (create) for /api/sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListSessions(w)
	case http.MethodPost:
		s.handleCreateSession(w)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListSessions returns the list of session summaries (id + title).
func (s *Server) handleListSessions(w http.ResponseWriter) {
	summaries, err := session.List(s.sessionsDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("list sessions: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

// handleCreateSession creates a new session and returns its ID.
func (s *Server) handleCreateSession(w http.ResponseWriter) {
	slog.Info("creating new session")
	sess, err := session.New(s.sessionsDir)
	if err != nil {
		slog.Error("failed to create session", "error", err)
		http.Error(w, fmt.Sprintf("create session: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": sess.ID()})
}

// Message represents a chat message for template rendering
type Message struct {
	Role    string
	Content string
}

// handleChat serves the chat page for a specific session with server-side rendered messages.
// Route: /chat/{id} → serve web UI with the specified session.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path parameter.
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}

	// Load session to verify it exists
	sess, err := session.Open(sessionID, s.sessionsDir)
	if err != nil {
		slog.Error("failed to open session for chat view", "sessionID", sessionID, "error", err)
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	defer sess.Close()

	// Load session history to render messages
	var messages []Message
	events, err := session.History(sess.Dir())
	if err != nil {
		slog.Warn("failed to load session history for rendering", "sessionID", sessionID, "error", err)
	} else {
		for _, ev := range events {
			if ev.Type == "message" && ev.Message != nil {
				var msg session.Message
				if unmarshalErr := json.Unmarshal(*ev.Message, &msg); unmarshalErr == nil {
					// Only render user and assistant messages with content
					if (msg.Role == "user" || msg.Role == "assistant") && msg.Content != "" {
						messages = append(messages, Message{
							Role:    msg.Role,
							Content: msg.Content,
						})
					}
				}
			}
		}
	}
	slog.Error("found messages", "messages", messages)

	// Load all sessions for sidebar
	summaries, err := session.List(s.sessionsDir)
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Sessions  []session.Summary
		SessionID string
		Messages  []Message
	}{
		Sessions:  summaries,
		SessionID: sessionID,
		Messages:  messages,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := chatTemplate.ExecuteTemplate(w, "layout", data); err != nil {
		slog.Error("failed to render chat template", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// handleSessionHistory returns the full message history for a session.
// Route: GET /api/sessions/{id}/history
func (s *Server) handleSessionHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session ID", http.StatusBadRequest)
		return
	}

	slog.Info("loading session history", "sessionID", sessionID)

	// Get the session file path
	sessionFile := filepath.Join(s.sessionsDir, sessionID+".jsonl")
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	events, err := session.History(sessionFile)
	if err != nil {
		slog.Error("failed to load session history", "sessionID", sessionID, "error", err)
		http.Error(w, fmt.Sprintf("load history: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// handleStream handles SSE connections for the agent loop.
// The session ID is passed via ?session=<id> query parameter.
// If no session ID is provided, a new session is created.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	// Get session ID from URL (optional — creates new if omitted).
	sessionID := r.URL.Query().Get("session")

	// Get query from URL or POST body.
	query := r.URL.Query().Get("query")
	if query == "" && r.Method == "POST" {
		buf := make([]byte, 1024*64)
		n, _ := r.Body.Read(buf)
		query = strings.TrimSpace(string(buf[:n]))
	}
	if query == "" {
		http.Error(w, "missing query", http.StatusBadRequest)
		return
	}

	slog.Info("stream request", "query", query, "sessionID", sessionID, "remoteAddr", r.RemoteAddr)

	// Create LLM client from current config.
	cfg := s.getConfig()
	client, err := llm.New(cfg, "")
	if err != nil {
		slog.Error("no LLM available", "error", err)
		http.Error(w, fmt.Sprintf("no LLM available: %v", err), http.StatusServiceUnavailable)
		return
	}
	slog.Info("using LLM", "client", client.Name())

	// Create or resume a session.
	var sess *session.Session
	if sessionID != "" {
		var err error
		sess, err = session.Open(sessionID, s.sessionsDir)
		if err != nil {
			slog.Error("failed to open session", "id", sessionID, "error", err)
			http.Error(w, fmt.Sprintf("session not found: %v", err), http.StatusNotFound)
			return
		}
		slog.Info("session resumed", "id", sess.ID())
	} else {
		var err error
		sess, err = session.New(s.sessionsDir)
		if err != nil {
			slog.Error("failed to create session", "error", err)
			http.Error(w, fmt.Sprintf("create session: %v", err), http.StatusInternalServerError)
			return
		}
	}

	if sess != nil {
		slog.Info("session", "id", sess.ID(), "dir", sess.Dir(), "title", sess.Title())
		sess.RecordModel(client.Name(), cfg.LLM.Model)
		defer sess.Close()
	}

	// Create agent and attach the session.
	ag := agent.New(client)
	if sess != nil {
		ag.SetSession(sess)
	}

	// Initialize conversation with the user's message.
	messages := []llm.Message{
		{Role: "user", Content: query},
	}

	// If resuming a session, prepend existing message history so the LLM
	// has full context from the conversation.
	if sess != nil {
		events, histErr := session.History(sess.Dir())
		if histErr != nil {
			slog.Warn("failed to load session history for LLM context", "sessionID", sess.ID(), "error", histErr)
		} else {
			// Extract messages from events and prepend to conversation.
			var history []llm.Message
			for _, ev := range events {
				if ev.Type == "message" && ev.Message != nil {
					var msg session.Message
					if unmarshalErr := json.Unmarshal(*ev.Message, &msg); unmarshalErr == nil {
						// Convert session.ToolCall to llm.ToolCall.
						var toolCalls []llm.ToolCall
						for _, stc := range msg.ToolCalls {
							toolCalls = append(toolCalls, llm.ToolCall{
								ID:   stc.ID,
								Type: "function",
								Function: llm.ToolFunctionArg{
									Name:      stc.Name,
									Arguments: stc.Args,
								},
							})
						}
						history = append(history, llm.Message{
							Role:       msg.Role,
							Content:    msg.Content,
							ToolCalls:  toolCalls,
							ToolCallID: msg.ToolCallID,
						})
					}
				}
			}
			if len(history) > 0 {
				slog.Info("prepending session history to LLM context", "sessionID", sess.ID(), "historyCount", len(history))
				messages = append(history, messages...)
			}
		}
	}

	// Create a context that cancels when the client disconnects.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send the session ID as the first event so the UI can track it.
	if sess != nil {
		s.sendEvent(w, "session", map[string]string{"id": sess.ID()})
	}

	// Run the agent loop and stream results.
	go func() {
		defer cancel()

		slog.Debug("starting agent loop", "query", query, "sessionID", sess.ID())
		// Run the full agentic loop.
		results, err := ag.Run(ctx, messages)
		if err != nil {
			slog.Error("agent loop failed", "query", query, "error", err)
			s.sendEvent(w, "error", map[string]string{"message": err.Error()})
			s.sendDone(w)
			return
		}

		// Stream the final assistant response.
		for _, msg := range results {
			if msg.Content != "" {
				slog.Debug("streaming final response", "contentLen", len(msg.Content))
				s.sendEvent(w, "message", map[string]string{"content": msg.Content})
			}
		}

		slog.Debug("stream complete", "query", query)
		s.sendDone(w)
	}()

	flusher.Flush()
	// Keep the connection open until the client disconnects or context is cancelled.
	<-ctx.Done()
}

// sendEvent sends a single SSE event.
func (s *Server) sendEvent(w http.ResponseWriter, event string, data map[string]string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, mustJSON(data))
}

// sendDone sends the [DONE] marker.
func (s *Server) sendDone(w http.ResponseWriter) {
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// mustJSON marshals v to JSON.
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
