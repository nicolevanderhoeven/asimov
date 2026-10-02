package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/httpapi"
)

func runServe(ctx context.Context, g *gm.GM, addr string, ttl time.Duration, scenario string, logger *slog.Logger) error {
	store := httpapi.NewStore(ttl)
	sweepCtx, cancelSweep := context.WithCancel(ctx)
	defer cancelSweep()
	go store.Sweep(sweepCtx, ttl/6)
	api := httpapi.NewServer(g, store, logger, g.Model == nil)
	api.DefaultScenario = scenario
	srv := &http.Server{Addr: addr, Handler: api.Handler()}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Printf("HTTP API listening on %s (session TTL %s, default scenario %s)\n", addr, ttl, scenario)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
