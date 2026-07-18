package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/server"
	"github.com/rmyers/majordomo/session"
)

func main() {
	w := os.Stderr

	// Set global logger with custom options
	slog.SetDefault(slog.New(
		tint.NewTextHandler(w, &tint.Options{
			Level:      slog.LevelDebug,
			TimeFormat: time.Kitchen,
			AddSource:  true,
		}),
	))

	port := flag.String("port", ":3636", "HTTP listen address (host:port)")
	configDir := flag.String("config", "", "directory for config.json and sessions (default: ~/.config/majordomo)")
	flag.Parse()

	cfg := config.New(*configDir)
	if err := cfg.Load(); err != nil {
		slog.Warn("using default config", "error", err)
	}
	slog.Info("Using config", "model", cfg.GetModel(), "url", cfg.GetURL())

	sessionService := session.NewSessionService(cfg)
	srv := server.New(*port, sessionService)
	if err := srv.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
