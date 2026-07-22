package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"github.com/rmyers/majordomo/llm"
	"github.com/rmyers/majordomo/repo"
	"github.com/rmyers/majordomo/session"
)

const maxQueueSize = 100
const maxConcurrentSessions = 2

// ToolResult holds the output of a tool execution.
type ToolResult struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	Err    string `json:"error,omitempty"`
}

// WorkItem represents a unit of work for the agent to process.
type WorkItem struct {
	SessionID string
	Session   *session.Session
	Messages  []llm.Message
	Results   chan ResultEvent
	Done      chan error
}

// ResultEvent is sent back through a WorkItem's Results channel.
type ResultEvent struct {
	Type    string // "status", "message", "chunk", "tool", "error", "done"
	Content string
	Tool    string
	Error   string
	Turn    int
}

// activeSession tracks the context for an active agent session.
type activeSession struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// Agent runs the agentic loop: send messages to LLM, execute tool calls, repeat.
type Agent struct {
	Manager        *llm.Manager
	Tools          []llm.Tool
	workQueue      chan WorkItem
	activeSessions map[string]*activeSession
	sem            chan struct{}
	wg             sync.WaitGroup
	stopCh         chan struct{}
	mu             sync.RWMutex
}

// New creates an Agent with the standard tools (read, edit, write, bash).
func New(manager *llm.Manager) *Agent {
	return &Agent{
		Manager: manager,
		Tools: []llm.Tool{
			{
				Name:        "read",
				Description: "Read the contents of a file. Use this to inspect files without modifying them.",
				Params: map[string]llm.ParamSchema{
					"path": {Type: "string", Description: "The file path to read", Required: true},
				},
			},
			{
				Name:        "edit",
				Description: "Replace a block of text in a file with new text. The old text must match exactly (including whitespace). Use this to make targeted changes.",
				Params: map[string]llm.ParamSchema{
					"path":    {Type: "string", Description: "The file path to edit", Required: true},
					"oldText": {Type: "string", Description: "The exact text to find and replace", Required: true},
					"newText": {Type: "string", Description: "The text to replace with", Required: true},
				},
			},
			{
				Name:        "write",
				Description: "Write content to a file, creating or overwriting it. Use this for new files or complete rewrites.",
				Params: map[string]llm.ParamSchema{
					"path":    {Type: "string", Description: "The file path to write", Required: true},
					"content": {Type: "string", Description: "The content to write", Required: true},
				},
			},
			{
				Name:        "bash",
				Description: "Execute a shell command and return its output. Use this for running scripts, installing packages, or any command-line task.",
				Params: map[string]llm.ParamSchema{
					"cmd": {Type: "string", Description: "The shell command to execute", Required: true},
				},
			},
		},
		workQueue:      make(chan WorkItem, maxQueueSize),
		activeSessions: make(map[string]*activeSession),
		sem:            make(chan struct{}, maxConcurrentSessions),
		stopCh:         make(chan struct{}),
	}
}

// WorkQueue returns the work queue channel for the server to send work.
func (a *Agent) WorkQueue() chan<- WorkItem {
	return a.workQueue
}

// RunMainLoop runs the agent's main processing loop in a goroutine.
// It processes work items from the queue, limiting concurrency to maxConcurrentSessions.
func (a *Agent) RunMainLoop() {
	slog.Info("agent main loop started")
	for {
		select {
		case <-a.stopCh:
			slog.Info("agent main loop stopping")
			for len(a.workQueue) > 0 {
				<-a.workQueue
			}
			close(a.workQueue)
			return
		case item, ok := <-a.workQueue:
			if !ok {
				slog.Info("agent work queue closed")
				return
			}
			a.wg.Add(1)
			select {
			case a.sem <- struct{}{}:
				go a.processWorkItem(item)
			default:
				slog.Warn("agent at max concurrency, dropping work item", "sessionID", item.SessionID)
				a.wg.Done()
			}
		}
	}
}

func (a *Agent) processWorkItem(item WorkItem) {
	defer a.wg.Done()
	defer func() { <-a.sem }()

	slog.Info("processing work item", "sessionID", item.SessionID)

	ctx, cancel := context.WithCancel(context.Background())
	if item.SessionID != "" {
		a.mu.Lock()
		a.activeSessions[item.SessionID] = &activeSession{ctx: ctx, cancel: cancel}
		a.mu.Unlock()
	}
	defer cancel()

	// Send status event
	select {
	case item.Results <- ResultEvent{Type: "status", Content: "thinking", Turn: 0}:
	case <-ctx.Done():
		return
	}

	// Run the agentic loop with the item's session, streaming chunks via Results
	if item.Session == nil {
		slog.Error("session is required but was nil", "sessionID", item.SessionID)
		return
	}
	results, err := a.runWithSession(ctx, item.Session, item.Messages, item.Results)
	if err != nil {
		slog.Error("agent loop failed", "sessionID", item.SessionID, "error", err)
		select {
		case item.Results <- ResultEvent{Type: "error", Error: err.Error()}:
		case <-ctx.Done():
		}
		select {
		case item.Done <- err:
		default:
		}
		return
	}

	// Send each result message
	for i, msg := range results {
		if msg.Content != "" {
			select {
			case item.Results <- ResultEvent{Type: "message", Content: msg.Content, Turn: i + 1}:
			case <-ctx.Done():
				return
			}
		}
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				select {
				case item.Results <- ResultEvent{Type: "tool", Tool: tc.Function.Name}:
				case <-ctx.Done():
					return
				}
			}
		}
	}

	// Results already recorded during runWithSession, just signal done
	select {
	case item.Results <- ResultEvent{Type: "done"}:
	case <-ctx.Done():
	}
	select {
	case item.Done <- nil:
	default:
	}

	slog.Info("work item complete", "sessionID", item.SessionID)
}

// SubmitWork sends a work item to the agent's queue (non-blocking).
func (a *Agent) SubmitWork(item WorkItem) bool {
	select {
	case a.workQueue <- item:
		return true
	default:
		slog.Warn("agent work queue full, dropping work item", "sessionID", item.SessionID)
		return false
	}
}

// HandleStop cancels all active sessions.
func (a *Agent) HandleStop() {
	slog.Info("agent stop requested, cancelling all active sessions")
	a.mu.Lock()
	for id, sess := range a.activeSessions {
		slog.Info("cancelling active session", "sessionID", id)
		sess.cancel()
	}
	a.mu.Unlock()
}

// RemoveSession removes a session from the active map (called on client disconnect).
func (a *Agent) RemoveSession(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sess, ok := a.activeSessions[sessionID]; ok {
		slog.Info("removing session from active map", "sessionID", sessionID)
		sess.cancel()
		delete(a.activeSessions, sessionID)
	}
}

// ActiveSessionCount returns the number of currently active sessions.
func (a *Agent) ActiveSessionCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.activeSessions)
}

// Close cancels all work and closes channels immediately.
func (a *Agent) Close() {
	slog.Info("closing agent")
	close(a.stopCh)
}

// runWithSession executes the agentic loop with a specific session.
func (a *Agent) runWithSession(ctx context.Context, sess *session.Session, messages []llm.Message, results chan ResultEvent) ([]llm.Message, error) {
	var allMessages []llm.Message
	for _, m := range messages {
		allMessages = append(allMessages, m)
	}

	iteration := 0
	for {
		iteration++

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		slog.Debug("agent loop iteration", "iteration", iteration, "messageCount", len(allMessages))

		client := a.Manager.Get()

		// For the final response (no tool calls from previous iterations), always use streaming.
		// For iterations with tool calls, use blocking Chat.
		if iteration == 1 {
			// First iteration: check if LLM wants tool calls using blocking Chat
			resp, err := client.Chat(ctx, allMessages)
			if err != nil {
				slog.Error("LLM call failed", "iteration", iteration, "error", err)
				return nil, fmt.Errorf("LLM call: %w", err)
			}

			// If the response has tool calls, record the assistant message and execute them
			if len(resp.ToolCalls) > 0 {
				sess.RecordMessage("assistant", resp.Content, resp.ToolCalls, "")
				slog.Debug("LLM requested tool calls", "iteration", iteration, "toolCount", len(resp.ToolCalls))
				allMessages = append(allMessages, *resp)

				for _, tc := range resp.ToolCalls {
					slog.Debug("executing tool", "iteration", iteration, "toolName", tc.Function.Name, "callID", tc.ID)
					args, err := parseToolArgs(tc.Function.Arguments)
					if err != nil {
						slog.Error("failed to parse tool arguments", "iteration", iteration, "toolName", tc.Function.Name, "error", err)
						allMessages = append(allMessages, llm.Message{
							Role:       "tool",
							Content:    fmt.Sprintf("Error parsing arguments: %v", err),
							ToolCallID: tc.ID,
						})
						continue
					}

					result := a.executeTool(tc.Function.Name, args)
					if result.Err != "" {
						slog.Warn("tool execution returned error", "iteration", iteration, "toolName", tc.Function.Name, "error", result.Err)
					} else {
						slog.Debug("tool executed successfully", "iteration", iteration, "toolName", tc.Function.Name, "outputLen", len(result.Output))
					}

					sess.RecordToolResult(tc.ID, result.Output, result.Err, "")

					allMessages = append(allMessages, llm.Message{
						Role:       "tool",
						Content:    result.Output,
						ToolCallID: tc.ID,
					})
				}
				// After tool calls, stream the final response
				slog.Debug("tool calls complete — streaming final response", "iteration", iteration+1)
				return a.streamFinalResponse(ctx, sess, allMessages, results)
			}

			// No tool calls — this is the final response, stream it directly
			slog.Debug("agent loop complete — final response (no tools, streaming)", "iteration", iteration, "contentLen", len(resp.Content))
			return a.streamFinalResponse(ctx, sess, allMessages, results)
		}

		// Subsequent iterations: stream the final response
		slog.Debug("agent loop complete — streaming final response", "iteration", iteration)
		return a.streamFinalResponse(ctx, sess, allMessages, results)
	}
}

// streamFinalResponse streams the final text response using StreamChat,
// sending accumulated text chunks through the results channel for SSE relay.
func (a *Agent) streamFinalResponse(ctx context.Context, sess *session.Session, messages []llm.Message, results chan ResultEvent) ([]llm.Message, error) {
	client := a.Manager.Get()
	var accumulatedText string

	err := client.StreamChat(ctx, messages, func(text string, toolCalls []llm.ToolCall) {
		if text != "" {
			accumulatedText += text
			select {
			case <-ctx.Done():
				return
			default:
			}
			if results != nil {
				select {
				case results <- ResultEvent{Type: "chunk", Content: text, Turn: 0}:
				case <-ctx.Done():
					return
				default:
				}
			}
		}
		if len(toolCalls) > 0 {
			// Tool calls from stream — shouldn't normally happen with final response
			// but handle it by switching to blocking mode
			slog.Debug("stream returned tool calls, falling back to blocking")
		}
	})

	if err != nil {
		slog.Error("stream final response failed", "error", err)
		return nil, err
	}

	slog.Debug("streamed final response complete", "textLen", len(accumulatedText))

	// Record the accumulated result in the session
	if accumulatedText != "" {
		sess.RecordMessage("assistant", accumulatedText, nil, "")
	}

	return []llm.Message{{Role: "assistant", Content: accumulatedText}}, nil
}

// executeTool runs a single tool call and returns the result.
func (a *Agent) executeTool(name string, args map[string]any) ToolResult {
	switch name {
	case "read":
		return a.toolRead(args)
	case "edit":
		return a.toolEdit(args)
	case "write":
		return a.toolWrite(args)
	case "bash":
		return a.toolBash(args)
	default:
		return ToolResult{Output: fmt.Sprintf("unknown tool: %s", name)}
	}
}

// toolRead reads a file and returns its contents.
func (a *Agent) toolRead(args map[string]any) ToolResult {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolResult{Output: "error: 'path' argument is required"}
	}

	content, err := repo.ReadFile(".", path)
	if err != nil {
		return ToolResult{Output: "", Err: fmt.Sprintf("read %s: %v", path, err)}
	}
	return ToolResult{Output: content}
}

// toolEdit replaces text in a file.
func (a *Agent) toolEdit(args map[string]any) ToolResult {
	path, ok1 := args["path"].(string)
	oldText, ok2 := args["oldText"].(string)
	newText, ok3 := args["newText"].(string)
	if !ok1 || path == "" || !ok2 || !ok3 {
		return ToolResult{Output: "error: 'path', 'oldText', and 'newText' arguments are required"}
	}

	content, err := repo.ReadFile(".", path)
	if err != nil {
		return ToolResult{Output: "", Err: fmt.Sprintf("read %s: %v", path, err)}
	}

	updated := strings.Replace(content, oldText, newText, 1)
	if updated == content {
		return ToolResult{Output: fmt.Sprintf("no change: text not found in %s", path)}
	}

	if err := repo.WriteFile(".", path, []byte(updated)); err != nil {
		return ToolResult{Output: "", Err: fmt.Sprintf("write %s: %v", path, err)}
	}
	return ToolResult{Output: fmt.Sprintf("edited %s successfully", path)}
}

// toolWrite writes content to a file.
func (a *Agent) toolWrite(args map[string]any) ToolResult {
	path, ok1 := args["path"].(string)
	content, ok2 := args["content"].(string)
	if !ok1 || path == "" || !ok2 {
		return ToolResult{Output: "error: 'path' and 'content' arguments are required"}
	}

	if err := repo.WriteFile(".", path, []byte(content)); err != nil {
		return ToolResult{Output: "", Err: fmt.Sprintf("write %s: %v", path, err)}
	}
	return ToolResult{Output: fmt.Sprintf("wrote %s (%d bytes)", path, len(content))}
}

// toolBash executes a shell command.
func (a *Agent) toolBash(args map[string]any) ToolResult {
	cmd, ok := args["cmd"].(string)
	if !ok || cmd == "" {
		return ToolResult{Output: "error: 'cmd' argument is required"}
	}

	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return ToolResult{Output: string(out), Err: fmt.Sprintf("command failed: %v", err)}
	}
	return ToolResult{Output: string(out)}
}

// parseToolArgs parses the JSON arguments string from a tool call.
func parseToolArgs(argsJSON string) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing tool arguments: %w", err)
	}
	return args, nil
}
