package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
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
	agent := agent.New(llmManager)
	sessionService := session.NewSessionService(cfg)
	srv := server.New(cfg, sessionService, agent, llmManager)

	// Start agent main loop
	go agent.RunMainLoop()

	// Start server
	go func() {
		if err := srv.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
	}()

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("shutting down...")
	agent.Close()
	// Don't wait for in-flight work - just exit
}
