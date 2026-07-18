package templates

import (
	"embed"
	"html/template"
	"io"

	"github.com/rmyers/majordomo/session"
)

//go:embed *
var files embed.FS

var (
	home = parse("home.html")
	chat = parse("chat.html")
)

type HomeParams struct {
	Sessions  []session.Summary
	SessionID string
}

func Home(w io.Writer, p HomeParams) error {
	return home.ExecuteTemplate(w, "layout.html", p)
}

type ChatParams struct {
	Sessions  []session.Summary
	SessionID string
	Messages  []session.Message
}

func Chat(w io.Writer, p ChatParams) error {
	return chat.ExecuteTemplate(w, "layout.html", p)
}

func parse(file string) *template.Template {
	return template.Must(
		template.New("layout.html").ParseFS(files, "layout.html", file))
}
