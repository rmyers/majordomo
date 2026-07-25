package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rmyers/majordomo/agent"
	"github.com/rmyers/majordomo/server"
)

type App struct {
	ctx   context.Context
	agent *agent.Agent
}

func NewApp(srv *server.Server, agt *agent.Agent) *App {
	return &App{agent: agt}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.agent.RunMainLoop()
}

func (a *App) exit(ctx context.Context) {
	slog.Info("shutting down...")
	a.agent.Close()
}

func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}
