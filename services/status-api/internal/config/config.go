// Package config reads the service configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds everything the status API needs to run.
type Config struct {
	// Addr is the listen address of the HTTP server.
	Addr string
	// Resync is the informer resync period. Watches keep the cache current on
	// their own; the resync is only a safety net against a missed event.
	Resync time.Duration
}

// FromEnv builds a Config from the process environment, applying defaults.
func FromEnv() (Config, error) {
	cfg := Config{
		Addr:   envOr("STATUS_API_ADDR", ":8081"),
		Resync: 10 * time.Minute,
	}
	if raw := os.Getenv("RESYNC_SECONDS"); raw != "" {
		secs, err := strconv.Atoi(raw)
		if err != nil || secs <= 0 {
			return Config{}, fmt.Errorf("RESYNC_SECONDS must be a positive integer, got %q", raw)
		}
		cfg.Resync = time.Duration(secs) * time.Second
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
