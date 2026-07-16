package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/rmyers/majordomo/config"
	"github.com/rmyers/majordomo/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	port := flag.String("port", ":3636", "HTTP listen address (host:port)")
	configPath := flag.String("config", "", "path to config.json (default: ~/.majordomo/config.json)")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Warn("using default config", "error", err)
		cfg = config.Default()
	}
	slog.Info("Using config", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model, "url", cfg.LLM.URL)

	// Get sessions directory
	sessionsDir, err := config.SessionsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get sessions directory: %v\n", err)
		os.Exit(1)
	}

	// Start the server
	srv := server.New(*port, sessionsDir)
	if err := srv.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
