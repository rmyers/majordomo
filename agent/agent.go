package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/rmyers/majordomo/llm"
	"github.com/rmyers/majordomo/repo"
	"github.com/rmyers/majordomo/session"
)

// ToolResult holds the output of a tool execution.
type ToolResult struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	Err    string `json:"error,omitempty"`
}

// Agent runs the agentic loop: send messages to LLM, execute tool calls, repeat.
type Agent struct {
	Manager *llm.Manager
	Tools   []llm.Tool
	session *session.Session
}

// SetSession attaches a session for recording events to disk.
func (a *Agent) SetSession(s *session.Session) {
	a.session = s
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
	}
}

// Run executes the agentic loop: send messages to the LLM, parse tool calls,
// execute them, feed results back, and repeat until the LLM stops calling tools.
// If a session is set, events are recorded to disk as JSONL.
func (a *Agent) Run(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	var allMessages []llm.Message
	for _, m := range messages {
		allMessages = append(allMessages, m)
	}

	iteration := 0
	for {
		iteration++
		slog.Debug("agent loop iteration", "iteration", iteration, "messageCount", len(allMessages))

		client := a.Manager.Get()
		resp, err := client.Chat(ctx, allMessages)
		if err != nil {
			slog.Error("LLM call failed", "iteration", iteration, "error", err)
			return nil, fmt.Errorf("LLM call: %w", err)
		}

		// Record the assistant message in the session (with tool calls if any).
		if a.session != nil {
			a.session.RecordMessage("assistant", resp.Content, resp.ToolCalls, "")
		}

		// If the response has tool calls, execute them and loop
		if len(resp.ToolCalls) > 0 {
			slog.Debug("LLM requested tool calls", "iteration", iteration, "toolCount", len(resp.ToolCalls))
			// Append the assistant message with tool calls
			allMessages = append(allMessages, *resp)

			// Execute each tool call
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

				// Record the tool result in the session.
				if a.session != nil {
					a.session.RecordToolResult(tc.ID, result.Output, result.Err, "")
				}

				// Append the tool result message
				allMessages = append(allMessages, llm.Message{
					Role:       "tool",
					Content:    result.Output,
					ToolCallID: tc.ID,
				})
			}
			continue
		}

		// No tool calls — this is the final response
		slog.Debug("agent loop complete — final response", "iteration", iteration, "contentLen", len(resp.Content))
		return []llm.Message{*resp}, nil
	}
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
