// Command status-api serves the WebApp custom resources of the cluster over a
// small REST API, backed by informers rather than by polling the API server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mampiz/idp-backstage/services/status-api/internal/config"
	"github.com/Mampiz/idp-backstage/services/status-api/internal/httpapi"
	"github.com/Mampiz/idp-backstage/services/status-api/internal/k8s"
	"github.com/Mampiz/idp-backstage/services/status-api/internal/webapps"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	clients, err := k8s.New()
	if err != nil {
		return err
	}
	logger.Info("connected to the cluster", "host", clients.Host)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := webapps.NewStore(clients.Dynamic, clients.Typed, cfg.Resync)
	server := httpapi.New(store, logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)

	// The HTTP server comes up before the caches are warm on purpose: /readyz
	// reports the difference, which is exactly what a readiness probe is for.
	go func() {
		logger.Info("status-api listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		if err := store.Start(ctx); err != nil {
			errCh <- err
			return
		}
		logger.Info("informer caches synced")
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
