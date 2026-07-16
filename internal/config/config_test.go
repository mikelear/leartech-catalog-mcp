package config

import (
	"os"
	"testing"
)

// unsetEnv is a t.Helper()-flavoured wrapper that clears an env var
// and IGNORES the error from os.Unsetenv — Unsetenv returns an error
// only on Windows when the var name is invalid, which isn't a case
// we can hit in a Linux CI env. Wrapping it silences errcheck without
// littering the test with `_ = os.Unsetenv(...)` prefixes.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
}

// TestLoadDefaults confirms envconfig supplies defaults when env is empty.
// DATABASE_URL is intentionally not required (shell mode).
// AUTH_REQUIRED defaults to `true` per C1 (auth hardening) — the chart
// must never accidentally boot with AUTH_REQUIRED=false because envconfig
// omitted the env var.
func TestLoadDefaults(t *testing.T) {
	// t.Setenv registers a cleanup that RESTORES the prior value, so we
	// still need to explicitly Unsetenv to get the "env-absent" state
	// this test exercises. Registering Setenv("") first also causes
	// t.Cleanup to remove the var after the test, which is what we want.
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CLUSTER_ID", "")
	t.Setenv("AUTH_REQUIRED", "")
	unsetEnv(t, "PORT", "DATABASE_URL", "CLUSTER_ID", "AUTH_REQUIRED")

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
// go-common v1.1.0's auth.VerifierConfig fields via uppercased field
// names — the wire-format the chart must emit for the fail-closed
// NewVerifier to receive a fully-populated VerifierConfig.
//
// Critically: only AUTH_ISSUER + AUTH_AUDIENCE are consulted. NO
// AUTH_SERVERURL / CLIENTID / CLIENTSECRET — catalog-mcp is a pure
// resource server and doesn't need client_credentials for inbound
// validation. That's the initiative closing the fail-closed crashloop.
func TestLoadAuthEnvMapping(t *testing.T) {
	t.Setenv("AUTH_ISSUER", "https://hydra.example.com")
	t.Setenv("AUTH_AUDIENCE", "leartech-catalog-mcp")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.Issuer != "https://hydra.example.com" {
		t.Errorf("Auth.Issuer = %q, want https://hydra.example.com", cfg.Auth.Issuer)
	}
	if cfg.Auth.Audience != "leartech-catalog-mcp" {
		t.Errorf("Auth.Audience = %q, want leartech-catalog-mcp", cfg.Auth.Audience)
	}
}

// TestLoadAuthOptionalJWKSURL locks in that the JWKSURL override is
// pluggable via envconfig for tests / non-standard issuers. Empty by
// default → Verifier derives from Issuer.
func TestLoadAuthOptionalJWKSURL(t *testing.T) {
	t.Setenv("AUTH_ISSUER", "https://hydra.example.com")
	t.Setenv("AUTH_AUDIENCE", "leartech-catalog-mcp")
	t.Setenv("AUTH_JWKSURL", "https://custom-jwks.example.com/keys")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.JWKSURL != "https://custom-jwks.example.com/keys" {
		t.Errorf("Auth.JWKSURL = %q, want the custom override", cfg.Auth.JWKSURL)
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
