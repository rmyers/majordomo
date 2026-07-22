package server

import (
	"bytes"
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
