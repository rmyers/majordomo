package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/julython/majordomo/internal/agent"
	"github.com/julython/majordomo/internal/config"
	"github.com/julython/majordomo/internal/llm"
)

// Server serves the web interface and SSE agent stream.
type Server struct {
	addr string
	mu   sync.RWMutex
	cfg  *config.Config
}

// New creates a Server listening on the given address.
func New(addr string) *Server {
	return &Server{addr: addr}
}

// Run starts the HTTP server.
func (s *Server) Run(cfg *config.Config) error {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	mux := http.NewServeMux()

	// Serve the web UI as static files from the web/ directory
	webDir := findWebDir()
	if webDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(webDir)))
	} else {
		// Fallback: serve a minimal inline page
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<html><body style="background:#1a1a2e;color:#e0e0e0;font-family:sans-serif;padding:40px;">
			<h1>Majordomo</h1><p>Open <code>web/index.html</code> in your browser.</p></body></html>`)
		}))
	}

	// Config API endpoints
	mux.HandleFunc("/api/config", s.handleConfig)

	// SSE agent stream endpoint
	mux.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		s.handleStream(w, r)
	})

	slog.Info("server starting", "addr", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

// getConfig returns the current config (read lock).
func (s *Server) getConfig() *config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
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

// handleStream handles SSE connections for the agent loop.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	// Get query from URL or POST body
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

	slog.Info("stream request", "query", query, "remoteAddr", r.RemoteAddr)

	// Create LLM client from current config
	cfg := s.getConfig()
	client, err := llm.New(cfg, "")
	if err != nil {
		slog.Error("no LLM available", "error", err)
		http.Error(w, fmt.Sprintf("no LLM available: %v", err), http.StatusServiceUnavailable)
		return
	}
	slog.Info("using LLM", "client", client.Name())

	// Create agent
	ag := agent.New(client)

	// Initialize conversation with the user's message
	messages := []llm.Message{
		{Role: "user", Content: query},
	}

	// Create a context that cancels when the client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Run the agent loop and stream results
	go func() {
		defer cancel()

		slog.Debug("starting agent loop", "query", query)
		// Run the full agentic loop
		results, err := ag.Run(ctx, messages)
		if err != nil {
			slog.Error("agent loop failed", "query", query, "error", err)
			s.sendEvent(w, "error", map[string]string{"message": err.Error()})
			s.sendDone(w)
			return
		}

		// Stream the final assistant response
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
	// Keep the connection open until the client disconnects or context is cancelled
	<-ctx.Done()
}

// findWebDir looks for the web/ directory relative to the binary or source.
func findWebDir() string {
	// Try relative to current working directory first
	if _, err := os.Stat("web"); err == nil {
		return "web"
	}
	// Try relative to the server package
	candidate := filepath.Join("..", "..", "web")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
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
