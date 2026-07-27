package session

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log/slog"
	"strings"

	"github.com/yuin/goldmark"
	goldmarkHTML "github.com/yuin/goldmark/extension"
)

var md = goldmark.New(
	goldmark.WithExtensions(
		goldmarkHTML.Table,
		goldmarkHTML.Strikethrough,
	),
)

// RenderMarkdown converts markdown text to HTML.
func RenderMarkdown(text string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(text), &buf); err != nil {
		return "<pre>" + escapeHTML(text) + "</pre>"
	}
	return buf.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// ToolInfo holds minimal tool metadata for rendering.
type ToolInfo struct {
	Name    string
	Summary func(string) string
}

type ToolSection struct {
	ID     string
	Name   string
	Args   string
	Result string
	IsErr  bool
}

type ChatEvent struct {
	Role    string
	Content template.HTML
	Tools   []ToolSection
}

// RenderChatEvents processes session events in order and returns a slice of ChatEvent.
func RenderChatEvents(events []Event, toolList []ToolInfo) []ChatEvent {
	var rendered []ChatEvent

	// Pre-collect tool results keyed by tool call ID.
	resultsByCallID := make(map[string]toolResultInfo)
	for _, ev := range events {
		if ev.Type == "message" && ev.Message != nil && ev.ToolResult != nil {
			var innerMsg Message
			if json.Unmarshal(*ev.Message, &innerMsg) == nil {
				var tr ToolCallResult
				if json.Unmarshal(*ev.ToolResult, &tr) == nil {
					resultsByCallID[innerMsg.ToolCallID] = toolResultInfo{
						Output: tr.Output,
						Error:  tr.Error,
					}
				}
			}
		}
	}

	for _, ev := range events {
		if ev.Type != "message" || ev.Message == nil {
			continue
		}

		var msg Message
		if json.Unmarshal(*ev.Message, &msg) != nil {
			continue
		}

		if msg.Role == "user" && msg.Content != "" {
			rendered = append(rendered, ChatEvent{
				Role:    "user",
				Content: template.HTML(RenderMarkdown(msg.Content)),
			})
		} else if msg.Role == "assistant" && (msg.Content != "" || len(msg.ToolCalls) > 0) {
			var sections []ToolSection
			for _, tc := range msg.ToolCalls {
				tr, _ := resultsByCallID[tc.ID]
				result := tr.Output
				if tr.Error != "" {
					result = tr.Error
				}
				isErr := result != "" && (strings.Contains(result, "error:") || strings.HasPrefix(result, "read ") || strings.HasPrefix(result, "write ") || strings.Contains(result, "command failed") || strings.Contains(result, "no change:"))

				var summary string
				for _, tool := range toolList {
					if tool.Name == tc.Name && tool.Summary != nil {
						summary = tool.Summary(tc.Args)
						break
					}
				}
				slog.Info("Tool summary", "summary", summary)
				if summary == "" {
					summary = tc.Name
				}
				sections = append(sections, ToolSection{
					ID:     tc.ID,
					Name:   tc.Name,
					Args:   summary,
					Result: result,
					IsErr:  isErr,
				})
			}

			if msg.Content != "" {
				rendered = append(rendered, ChatEvent{
					Role:    "assistant",
					Content: template.HTML(RenderMarkdown(msg.Content)),
					Tools:   sections,
				})
			} else if len(sections) > 0 {
				rendered = append(rendered, ChatEvent{
					Role:  "assistant",
					Tools: sections,
				})
			}
		}
	}

	return rendered
}

type toolResultInfo struct {
	Output string
	Error  string
}
