package config

import (
	"os"
	"testing"
)

// TestLoadDefaults confirms envconfig supplies defaults when env is empty.
// DATABASE_URL is intentionally not required (shell mode).
// AUTH_REQUIRED defaults to `true` per C1 (auth hardening) — the chart
// must never accidentally boot with AUTH_REQUIRED=false because envconfig
// omitted the env var.
func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CLUSTER_ID", "")
	t.Setenv("AUTH_REQUIRED", "")
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("CLUSTER_ID")
	os.Unsetenv("AUTH_REQUIRED")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (DATABASE_URL is optional)", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty (shell mode)", cfg.DatabaseURL)
	}
	if !cfg.AuthRequired {
		t.Errorf("AuthRequired = false, want true (C1 default — never boot fail-open by omission)")
	}
}

// TestLoadOverrides confirms env values override defaults.
func TestLoadOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("CLUSTER_ID", "gcp")
	t.Setenv("DATABASE_URL", "postgres://x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want 9090", cfg.Port)
	}
	if cfg.ClusterID != "gcp" {
		t.Errorf("ClusterID = %q, want gcp", cfg.ClusterID)
	}
	if cfg.DatabaseURL != "postgres://x" {
		t.Errorf("DatabaseURL = %q, want postgres://x", cfg.DatabaseURL)
	}
}

// TestLoadAuthEnvMapping confirms envconfig maps the AUTH_ prefix onto
// go-common's auth.Config fields via uppercased field names — the
// wire-format the chart must emit for go-common v1.0.0's fail-closed
// NewServiceClient to receive a fully-populated Config.
func TestLoadAuthEnvMapping(t *testing.T) {
	t.Setenv("AUTH_SERVERURL", "https://hydra.example.com")
	t.Setenv("AUTH_CLIENTID", "catalog-mcp")
	t.Setenv("AUTH_CLIENTSECRET", "s3cret")
	t.Setenv("AUTH_AUDIENCE", "leartech-catalog-mcp")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.ServerURL != "https://hydra.example.com" {
		t.Errorf("Auth.ServerURL = %q, want https://hydra.example.com", cfg.Auth.ServerURL)
	}
	if cfg.Auth.ClientID != "catalog-mcp" {
		t.Errorf("Auth.ClientID = %q, want catalog-mcp", cfg.Auth.ClientID)
	}
	if cfg.Auth.ClientSecret != "s3cret" {
		t.Errorf("Auth.ClientSecret = %q, want s3cret", cfg.Auth.ClientSecret)
	}
	if cfg.Auth.Audience != "leartech-catalog-mcp" {
		t.Errorf("Auth.Audience = %q, want leartech-catalog-mcp", cfg.Auth.Audience)
	}
}

// TestLoadAuthRequiredExplicitFalse confirms the local-dev opt-out
// works. Production charts default to true — this test locks the
// override in so we know how to disable auth during local iteration.
func TestLoadAuthRequiredExplicitFalse(t *testing.T) {
	t.Setenv("AUTH_REQUIRED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AuthRequired {
		t.Errorf("AuthRequired = true, want false")
	}
}
