package config

import (
	"errors"
	"testing"
	"time"
)

func TestFromEnvRequiresTheGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_OWNER", "mampiz")

	_, err := FromEnv()
	if !errors.Is(err, ErrMissingToken) {
		t.Fatalf("err = %v, want ErrMissingToken", err)
	}
}

func TestFromEnvAppliesDefaults(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_OWNER", "mampiz")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Addr != ":8082" {
		t.Errorf("Addr = %q, want :8082", cfg.Addr)
	}
	if cfg.CatalogPath != "catalog-info.yaml" {
		t.Errorf("CatalogPath = %q", cfg.CatalogPath)
	}
	if cfg.DiscoveryTTL != 5*time.Minute {
		t.Errorf("DiscoveryTTL = %v, want 5m", cfg.DiscoveryTTL)
	}
}

func TestFromEnvRejectsAnInvalidTTL(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_OWNER", "mampiz")
	t.Setenv("DISCOVERY_TTL_SECONDS", "not-a-number")

	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error for a non-numeric TTL")
	}
}
