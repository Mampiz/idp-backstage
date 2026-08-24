// Package config reads the service configuration from the environment.
//
// Secrets are read from the environment and nowhere else: there is no config
// file, no flag and no on-disk fallback for the GitHub token.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds everything the scaffolder service needs to run.
type Config struct {
	// Addr is the listen address of the HTTP server.
	Addr string
	// GitHubToken authenticates every call to the GitHub API.
	GitHubToken string
	// Owner is the GitHub user or organization the service works against.
	Owner string
	// CatalogPath is the file a repository must contain to be considered part
	// of the catalog, matching Backstage's catalog.import.entityFilename.
	CatalogPath string
	// DiscoveryTTL is how long a discovery result is served from cache before
	// GitHub is queried again.
	DiscoveryTTL time.Duration
}

// ErrMissingToken is returned when GITHUB_TOKEN is not set in the environment.
var ErrMissingToken = errors.New("GITHUB_TOKEN is not set: export it in your shell, it is never read from disk")

// FromEnv builds a Config from the process environment, applying defaults.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:         envOr("SCAFFOLDER_ADDR", ":8082"),
		GitHubToken:  os.Getenv("GITHUB_TOKEN"),
		Owner:        envOr("GITHUB_OWNER", ""),
		CatalogPath:  envOr("CATALOG_PATH", "catalog-info.yaml"),
		DiscoveryTTL: 5 * time.Minute,
	}

	if cfg.GitHubToken == "" {
		return Config{}, ErrMissingToken
	}
	if cfg.Owner == "" {
		return Config{}, errors.New("GITHUB_OWNER is not set")
	}
	if raw := os.Getenv("DISCOVERY_TTL_SECONDS"); raw != "" {
		secs, err := strconv.Atoi(raw)
		if err != nil || secs <= 0 {
			return Config{}, fmt.Errorf("DISCOVERY_TTL_SECONDS must be a positive integer, got %q", raw)
		}
		cfg.DiscoveryTTL = time.Duration(secs) * time.Second
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
