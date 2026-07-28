package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
	mu         sync.RWMutex
	cfg        *config.Config
	sessionSrv *session.SessionService
	llmManager *llm.Manager
	Router     *http.ServeMux
	agent      *agent.Agent
}

func New(config *config.Config, sessionSrv *session.SessionService, agent *agent.Agent, llmManager *llm.Manager) *Server {
	mux := http.NewServeMux()
	server := &Server{
		cfg:        config,
		sessionSrv: sessionSrv,
		agent:      agent,
		llmManager: llmManager,
		Router:     mux,
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
	return http.ListenAndServe(s.addr(), s.Router)
}

func (s *Server) loadRouter() {
	// Serve static assets
	s.Router.Handle("/styles.css", http.FileServer(http.FS(webFS)))
	s.Router.Handle("/app.js", http.FileServer(http.FS(webFS)))

	// Settings page.
	s.Router.HandleFunc("GET /settings", s.handleGetSettings)
	s.Router.HandleFunc("POST /settings", s.handlePostSettings)

	// Chat page: serves the web UI with a specific session.
	// Route: /chat/{id} → serve web UI with the specified session.
	s.Router.HandleFunc("GET /chat/new", s.handleNewChat)
	s.Router.HandleFunc("/chat/{id}", s.handleChat)
	s.Router.HandleFunc("/", s.handleRoot)
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
		APIKey:    apiKey,
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

// handleNewChat creates a fresh, empty session and redirects to its chat
// page. This is what "/chat/new" links to — a plain <a href="/chat/new">
// is enough on the frontend; the redirect does the rest, same as any
// other server-rendered navigation, no JS or form required.
//
// Uses GET rather than POST: this only makes sense as something a link
// (not a form submit) can trigger, and since this app runs locally via
// Wails rather than as a public multi-tenant web service, GET-triggers-a-
// write isn't the CSRF/caching concern it would be on the open web.
func (s *Server) handleNewChat(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionSrv.CreateSession("")
	if err != nil {
		slog.Error("failed to create session", "error", err)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/chat/"+sess.ID(), http.StatusSeeOther)
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
		slog.Error("failed to load session history", "sessionID", sessionID, "error", err)
		http.Error(w, "failed to load session history", http.StatusInternalServerError)
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

	// Convert agent tools to template tool info.
	toolList := make([]session.ToolInfo, 0, len(s.agent.Tools))
	for _, t := range s.agent.Tools {
		toolList = append(toolList, session.ToolInfo{
			Name:    t.Name,
			Summary: t.Summary,
		})
	}

	renderedMessages := session.RenderChatEvents(events, toolList)

	data := templates.ChatParams{
		Sessions:  summaries,
		SessionID: sessionID,
		Messages:  renderedMessages,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.Chat(w, data); err != nil {
		http.Error(w, "Error rendering home", http.StatusBadRequest)
		return
	}
}

// StreamQuery runs one turn of the agent loop for query against sessionID,
// relaying every event to sink. It blocks until the agent finishes, errors,
// or ctx is cancelled — so it's meant to be called from a goroutine, not
// directly on a request-handling or UI-bound call path. In practice
// StreamManager.run is the only caller, and it already backgrounds this
// itself; call StreamQuery directly only if you're bypassing
// StreamManager's per-session queueing/cancellation entirely.
func (s *Server) StreamQuery(ctx context.Context, sessionID, turnID, query string, sink StreamSink) {
	target := responseTargetID(turnID)

	sess, err := s.sessionSrv.OpenSession(sessionID)
	if err != nil {
		slog.Error("failed to open session", "id", sessionID, "error", err)
		sink.Emit("error", target, assistantHTML(turnID, false, errorHTML(fmt.Sprintf("session not found: %v", err))))
		return
	}
	slog.Info("session resumed", "id", sess.ID())

	sess.RecordMessage("user", query, nil, "")

	messages := []llm.Message{
		{Role: "user", Content: query},
	}

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

	// Create work item and submit to agent
	resultsCh := make(chan agent.ResultEvent, 10)
	doneCh := make(chan error, 1)

	workItem := agent.WorkItem{
		SessionID: sessionID,
		Session:   sess,
		Messages:  messages,
		Results:   resultsCh,
		Done:      doneCh,
	}

	if !s.agent.SubmitWork(workItem) {
		sink.Emit("error", target, assistantHTML(turnID, false, errorHTML("agent queue full")))
		return
	}

	// Accumulated text for this turn (text → HTML). Scoped to this single
	// StreamQuery call, so no map/session-keying needed.
	var accumulated string

	for {
		select {
		case <-ctx.Done():
			// Cancelled mid-turn. This is the one place that still has
			// `accumulated` in scope, so it's also the right place to
			// render the "(cancelled)" marker onto whatever partial
			// response existed — StreamManager, which triggers the
			// cancel, doesn't have that context.
			sink.Emit("cancelled", target, assistantHTML(turnID, false,
				session.RenderMarkdown(accumulated)+`<br><span class="cancelled">(cancelled)</span>`))
			return
		case event, ok := <-resultsCh:
			if !ok {
				// Channel closed without an explicit "done" event — treat
				// as finished so the UI doesn't hang on a typing indicator.
				sink.Emit("done", target, assistantHTML(turnID, false, session.RenderMarkdown(accumulated)))
				return
			}
			switch event.Type {
			case "status":
				// Agent-level "thinking" ping. The typing placeholder is
				// already showing (StreamManager set it when this turn
				// started), so there's nothing new to render here.
			case "chunk":
				accumulated += event.Content
				sink.Emit("message", target, assistantHTML(turnID, false, session.RenderMarkdown(accumulated)))
			case "message":
				accumulated = event.Content
				sink.Emit("message", target, assistantHTML(turnID, false, session.RenderMarkdown(accumulated)))
			case "error":
				sink.Emit("error", target, assistantHTML(turnID, false, errorHTML(session.RenderMarkdown(event.Error))))
			case "tool":
				sink.Emit("tool", target, assistantHTML(turnID, true, toolBadgeHTML(event.Tool)+session.RenderMarkdown(accumulated)))
			case "done":
				sink.Emit("done", target, assistantHTML(turnID, false, session.RenderMarkdown(accumulated)))
				return
			}
		}
	}
}
