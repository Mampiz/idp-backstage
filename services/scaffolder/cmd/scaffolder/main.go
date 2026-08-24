// Command scaffolder is the Go service that owns everything this platform does
// against GitHub: discovering which repositories belong in the Backstage
// catalog and, from F3 on, creating them and applying the matching WebApp
// custom resource.
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

	"github.com/google/go-github/v77/github"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/config"
	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/discovery"
	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/httpapi"
	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/k8s"
	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/provision"
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

	client := github.NewClient(nil).WithAuthToken(cfg.GitHubToken)
	discoverer := discovery.New(client, cfg.Owner, cfg.CatalogPath, cfg.DiscoveryTTL)

	clients, err := k8s.New()
	if err != nil {
		return err
	}
	logger.Info("connected to the cluster", "host", clients.Host)

	repos := provision.NewGitHub(client)
	// Surface a token that cannot finish the job now, not half way through
	// provisioning somebody's repository.
	if err := repos.CheckTokenScopes(context.Background()); err != nil {
		logger.Warn("the GitHub token may not be able to complete a scaffold", "error", err)
	}

	scaffolder := provision.NewService(
		repos,
		provision.NewCluster(clients.Dynamic, clients.Typed),
		logger,
		provision.Options{
			DefaultOwner:     cfg.Owner,
			DefaultNamespace: cfg.Namespace,
			Private:          cfg.PrivateRepos,
		},
	)

	server := httpapi.New(discoverer, scaffolder, cfg.Owner, logger)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("scaffolder listening", "addr", cfg.Addr, "owner", cfg.Owner)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
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
