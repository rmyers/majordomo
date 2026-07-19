package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/rmyers/majordomo/agent"
	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/llm"
	"github.com/rmyers/majordomo/session"
	"github.com/rmyers/majordomo/templates"
)

//go:embed *
var webFS embed.FS

// Server serves the web interface and SSE agent stream.
type Server struct {
	mu          sync.RWMutex
	cfg         *config.Config
	sessionSrv  *session.SessionService
	agent       *agent.Agent
	llmManager  *llm.Manager
	mux         *http.ServeMux
}

// New creates a Server listening on the given address with the specified session service.
func New(config *config.Config, sessionSrv *session.SessionService, agent *agent.Agent, llmManager *llm.Manager) *Server {
	mux := http.NewServeMux()
	server := &Server{
		cfg:        config,
		sessionSrv: sessionSrv,
		agent:      agent,
		llmManager: llmManager,
		mux:        mux,
	}
	server.loadRouter()

	return server
}

func (s *Server) addr() string {
	return fmt.Sprintf("%s:%s", s.cfg.Server.Host, s.cfg.Server.Port)
}

// Run starts the HTTP server.
func (s *Server) Run() error {
	slog.Info("server starting", "addr", s.addr())
	return http.ListenAndServe(s.addr(), s.mux)
}

func (s *Server) loadRouter() {
	// Serve static assets
	s.mux.Handle("/styles.css", http.FileServer(http.FS(webFS)))
	s.mux.Handle("/app.js", http.FileServer(http.FS(webFS)))

	// Settings page.
	s.mux.HandleFunc("GET /settings", s.handleGetSettings)
	s.mux.HandleFunc("POST /settings", s.handlePostSettings)

	// Session endpoints
	s.mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.mux.HandleFunc("POST /api/sessions", s.handleCreateSession)

	// Session history endpoint: GET /api/sessions/{id}/history.
	s.mux.HandleFunc("GET /api/sessions/{id}/history", s.handleSessionHistory)

	// Chat page: serves the web UI with a specific session.
	// Route: /chat/{id} → serve web UI with the specified session.
	s.mux.HandleFunc("/chat/{id}", s.handleChat)

	// SSE agent stream endpoint.
	s.mux.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		s.handleStream(w, r)
	})

	// Serve the web UI from the embedded filesystem.
	s.mux.HandleFunc("/", s.handleRoot)
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

	summaries, err := s.sessionSrv.ListSessions()
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := templates.HomeParams{
		Sessions:  summaries,
		SessionID: "",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := templates.Home(w, data); err != nil {
		http.Error(w, "Error rendering home", http.StatusBadRequest)
		return
	}
}

// handleGetSettings renders the settings page with current config values.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.getConfig()

	summaries, err := s.sessionSrv.ListSessions()
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := templates.SettingsParams{
		Sessions:  summaries,
		SessionID: "",
		Provider:  cfg.GetProvider(),
		Model:     cfg.GetModel(),
		URL:       cfg.GetURL(),
		APIKey:    "",
		Host:      cfg.GetHost(),
		Port:      cfg.GetPort(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Settings(w, data); err != nil {
		http.Error(w, "Error rendering settings", http.StatusInternalServerError)
		return
	}
}

// handlePostSettings saves config from form data and refreshes the LLM client.
func (s *Server) handlePostSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		slog.Error("failed to parse form", "error", err)
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	provider := strings.TrimSpace(r.Form.Get("provider"))
	model := strings.TrimSpace(r.Form.Get("model"))
	url := strings.TrimSpace(r.Form.Get("url"))
	apiKey := r.Form.Get("apiKey")
	host := strings.TrimSpace(r.Form.Get("host"))
	port := strings.TrimSpace(r.Form.Get("port"))

	if url == "" {
		s.renderSettingsPage(w, r, "", provider, model, url, apiKey, host, port, "URL is required")
		return
	}

	if port != "" {
		if _, err := fmt.Sscanf(port, "%d", new(int)); err != nil {
			s.renderSettingsPage(w, r, "", provider, model, url, apiKey, host, port, "Port must be a valid number")
			return
		}
	}

	cfg := s.getConfig()
	if cfg == nil {
		s.renderSettingsPage(w, r, "", provider, model, url, apiKey, host, port, "config not initialized")
		return
	}

	// Preserve existing API key if empty.
	if apiKey == "" {
		apiKey = cfg.GetAPIKey()
	}

	cfg.SetProvider(provider)
	cfg.SetModel(model)
	cfg.SetURL(url)
	cfg.SetAPIKey(apiKey)
	cfg.SetHost(host)
	cfg.SetPort(port)

	if err := cfg.Save(); err != nil {
		slog.Error("failed to save config", "error", err)
		s.renderSettingsPage(w, r, "", provider, model, url, apiKey, host, port, fmt.Sprintf("failed to save config: %v", err))
		return
	}

	// Refresh the LLM client with new configuration.
	if err := s.llmManager.Refresh(cfg, ""); err != nil {
		slog.Error("failed to refresh LLM client", "error", err)
		s.renderSettingsPage(w, r, "", provider, model, url, apiKey, host, port, fmt.Sprintf("config saved but LLM refresh failed: %v", err))
		return
	}

	slog.Info("config updated and LLM refreshed", "provider", cfg.GetProvider(), "model", cfg.GetModel(), "url", cfg.GetURL())

	s.renderSettingsPage(w, r, "Configuration saved and LLM refreshed!", provider, model, url, apiKey, host, port, "")
}

func (s *Server) renderSettingsPage(w http.ResponseWriter, r *http.Request, success, provider, model, url, apiKey, host, port, errMsg string) {
	summaries, err := s.sessionSrv.ListSessions()
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := templates.SettingsParams{
		Sessions:  summaries,
		SessionID: "",
		Provider:  provider,
		Model:     model,
		URL:       url,
		APIKey:    "",
		Host:      host,
		Port:      port,
		Success:   success,
		Error:     errMsg,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Settings(w, data); err != nil {
		http.Error(w, "Error rendering settings", http.StatusInternalServerError)
		return
	}
}

// handleListSessions returns the list of session summaries (id + title).
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	summaries, err := s.sessionSrv.ListSessions()
	if err != nil {
		http.Error(w, fmt.Sprintf("list sessions: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	slog.Info("creating new session")

	// Read the query from the request body (first user message content).
	query := ""
	if r.ContentLength > 0 {
		buf := make([]byte, 1024*64)
		n, _ := r.Body.Read(buf)
		if n > 0 {
			query = strings.TrimSpace(string(buf[:n]))
		}
	}

	if query == "" {
		sess, err := s.sessionSrv.CreateSession("")
		if err != nil {
			slog.Error("failed to create session", "error", err)
			http.Error(w, fmt.Sprintf("create session: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": sess.ID()})
		return
	}

	sess, err := s.sessionSrv.CreateSession(query)
	if err != nil {
		slog.Error("failed to create session", "error", err)
		http.Error(w, fmt.Sprintf("create session: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": sess.ID()})
}

// handleChat serves the chat page for a specific session with server-side rendered messages.
// Route: /chat/{id} → serve web UI with the specified session.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "session ID required", http.StatusBadRequest)
		return
	}

	sess, err := s.sessionSrv.OpenSession(sessionID)
	if err != nil {
		slog.Error("failed to open session for chat view", "sessionID", sessionID, "error", err)
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	defer sess.Close()

	var messages []session.Message
	events, err := s.sessionSrv.SessionHistory(sessionID)
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "Sessions events missing", http.StatusInternalServerError)
		return
	}

	for _, ev := range events {
		if ev.Type == "message" && ev.Message != nil {
			var msg session.Message
			if unmarshalErr := json.Unmarshal(*ev.Message, &msg); unmarshalErr == nil {
				if (msg.Role == "user" || msg.Role == "assistant") && msg.Content != "" {
					messages = append(messages, msg)
				}
			}
		}
	}

	summaries, err := s.sessionSrv.ListSessions()
	if err != nil {
		slog.Error("failed to list sessions", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := templates.ChatParams{
		Sessions:  summaries,
		SessionID: sessionID,
		Messages:  messages,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Chat(w, data); err != nil {
		http.Error(w, "Error rendering home", http.StatusBadRequest)
		return
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

	events, err := s.sessionSrv.SessionHistory(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
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
	sessionID := r.URL.Query().Get("session")

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

	sess, err := s.sessionSrv.OpenSession(sessionID)
	if err != nil {
		slog.Error("failed to open session", "id", sessionID, "error", err)
		http.Error(w, fmt.Sprintf("session not found: %v", err), http.StatusNotFound)
		return
	}
	slog.Info("session resumed", "id", sess.ID())

	s.agent.SetSession(sess)

	messages := []llm.Message{
		{Role: "user", Content: query},
	}

	if sess != nil {
		events, histErr := s.sessionSrv.SessionHistory(sess.ID())
		if histErr != nil {
			slog.Warn("failed to load session history for LLM context", "sessionID", sess.ID(), "error", histErr)
		} else {
			var history []llm.Message
			for _, ev := range events {
				if ev.Type == "message" && ev.Message != nil {
					var msg session.Message
					if unmarshalErr := json.Unmarshal(*ev.Message, &msg); unmarshalErr == nil {
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

	// Track how many messages are already in the session history.
	// We only record the new messages (query + agent response), not the history.
	historyCount := len(messages) - 1

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	if sess != nil {
		s.sendEvent(w, "session", map[string]string{"id": sess.ID()})
	}

	go func() {
		defer cancel()

		slog.Debug("starting agent loop", "query", query, "sessionID", sess.ID())
		results, err := s.agent.Run(ctx, messages)
		if err != nil {
			slog.Error("agent loop failed", "query", query, "error", err)
			s.sendEvent(w, "error", map[string]string{"message": err.Error()})
			s.sendDone(w)
			return
		}

		for _, msg := range results {
			if msg.Content != "" {
				slog.Debug("streaming final response", "contentLen", len(msg.Content))
				s.sendEvent(w, "message", map[string]string{"content": msg.Content})
			}
		}

		// Record only the new messages (not the prepended history).
		if sess != nil && historyCount >= 0 {
			newMessages := messages[historyCount:]
			for _, msg := range newMessages {
				if msg.Content != "" {
					sess.RecordMessage(msg.Role, msg.Content, nil, "")
				}
			}
		}

		slog.Debug("stream complete", "query", query)
		s.sendDone(w)
	}()

	flusher.Flush()
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
