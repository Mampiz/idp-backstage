package config

import (
	"testing"
	"time"
)

func TestFromEnvAppliesDefaults(t *testing.T) {
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Addr != ":8081" {
		t.Errorf("Addr = %q, want :8081", cfg.Addr)
	}
	if cfg.Resync != 10*time.Minute {
		t.Errorf("Resync = %v, want 10m", cfg.Resync)
	}
}

func TestFromEnvHonoursOverrides(t *testing.T) {
	t.Setenv("STATUS_API_ADDR", ":9000")
	t.Setenv("RESYNC_SECONDS", "30")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Addr != ":9000" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Resync != 30*time.Second {
		t.Errorf("Resync = %v, want 30s", cfg.Resync)
	}
}

func TestFromEnvRejectsAnInvalidResync(t *testing.T) {
	t.Setenv("RESYNC_SECONDS", "-1")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error for a negative resync")
	}
}
