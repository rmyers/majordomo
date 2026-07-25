package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
	"github.com/rmyers/majordomo/agent"
	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/llm"
	"github.com/rmyers/majordomo/server"
	"github.com/rmyers/majordomo/session"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

func main() {
	w := os.Stderr
	slog.SetDefault(slog.New(
		tint.NewTextHandler(w, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
			AddSource:  true,
		}),
	))
	configDir := flag.String("config", "", "directory for config.json and sessions (default: ~/.config/majordomo)")
	flag.Parse()
	cfg := config.New(*configDir)
	slog.Info("Using config", "model", cfg.GetModel(), "url", cfg.GetURL())
	llmManager := llm.NewManager()
	if err := llmManager.SetInitial(cfg, ""); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
	agt := agent.New(llmManager)
	sessionService := session.NewSessionService(cfg)
	srv := server.New(cfg, sessionService, agt, llmManager)

	app := NewApp(srv, agt)

	err := wails.Run(&options.App{
		Title:            "majordomo-ui",
		Width:            1024,
		Height:           768,
		AssetServer:      &assetserver.Options{Handler: srv.Router},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.exit,
		Bind:             []interface{}{app},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
