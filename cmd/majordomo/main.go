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

	configDir := flag.String("config", "", "directory for config.json and sessions (default: ~/.config/majordomo)")
	flag.Parse()

	cfg := config.New(*configDir)
	slog.Info("Using config", "model", cfg.GetModel(), "url", cfg.GetURL())

	// Configure the llm Manager
	llmManager := llm.NewManager()
	if err := llmManager.SetInitial(cfg, ""); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}

	// Configure the agent
	agent := agent.New(llmManager)

	sessionService := session.NewSessionService(cfg)
	srv := server.New(cfg, sessionService, agent)
	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
