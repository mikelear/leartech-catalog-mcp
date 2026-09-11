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
// AUTH_REQUIRED is gone; there is no default to get wrong because there is no
// flag. This kept its other assertions. (It once existed because envconfig
// omitted the env var.
func TestLoadDefaults(t *testing.T) {
	// t.Setenv registers a cleanup that RESTORES the prior value, so we
	// still need to explicitly Unsetenv to get the "env-absent" state
	// this test exercises. Registering Setenv("") first also causes
	// t.Cleanup to remove the var after the test, which is what we want.
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CLUSTER_ID", "")
	unsetEnv(t, "PORT", "DATABASE_URL", "CLUSTER_ID")

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
// Critically: only LEARTECH_AUTH_ISSUER + LEARTECH_AUTH_AUDIENCE are consulted. NO
// AUTH_SERVERURL / CLIENTID / CLIENTSECRET — catalog-mcp is a pure
// resource server and doesn't need client_credentials for inbound
// validation. That's the initiative closing the fail-closed crashloop.
func TestLoadAuthEnvMapping(t *testing.T) {
	t.Setenv("LEARTECH_AUTH_ISSUER", "https://hydra.example.com")
	t.Setenv("LEARTECH_AUTH_AUDIENCE", "leartech-catalog-mcp")

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
	t.Setenv("LEARTECH_AUTH_ISSUER", "https://hydra.example.com")
	t.Setenv("LEARTECH_AUTH_AUDIENCE", "leartech-catalog-mcp")
	t.Setenv("LEARTECH_AUTH_JWKS_URL", "https://custom-jwks.example.com/keys")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.JWKSURL != "https://custom-jwks.example.com/keys" {
		t.Errorf("Auth.JWKSURL = %q, want the custom override", cfg.Auth.JWKSURL)
	}
}

// TestLoadAuthRequiredIsIgnored replaces TestLoadAuthRequiredExplicitFalse,
// which confirmed the local-dev opt-out: AUTH_REQUIRED=false parsed to
// AuthRequired=false, and main.go then mounted /api/v1 with no middleware at
// all. One env var served the whole API unauthenticated.
//
// The field is gone. This asserts the variable is inert rather than deleting
// the coverage, so a reintroduction fails a test instead of passing silently.
func TestLoadAuthRequiredIsIgnored(t *testing.T) {
	t.Setenv("AUTH_REQUIRED", "false")
	t.Setenv("LEARTECH_AUTH_ISSUER", "https://hydra.example.com")
	t.Setenv("LEARTECH_AUTH_AUDIENCE", "leartech-catalog-mcp")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.Issuer != "https://hydra.example.com" || cfg.Auth.Audience != "leartech-catalog-mcp" {
		t.Fatalf("auth config was affected by AUTH_REQUIRED=false: %+v — the flag must have no effect", cfg.Auth)
	}
}

// InertAuthEnvSet reports LEARTECH_AUTH_* variables this service reads no
// meaning from. It exists because an operator who sets one deserves to be told
// it does nothing, rather than infer protection that was never configured —
// LEARTECH_AUTH_SERVER_URL especially, which reads like the issuer knob and is
// the go-common TOKEN ENDPOINT a resource server never posts to.
//
// A reporter that reports nothing is indistinguishable from a clean
// environment, so both directions are asserted.
func TestInertAuthEnvSet(t *testing.T) {
	t.Run("silent when nothing inert is set", func(t *testing.T) {
		for _, k := range []string{
			"LEARTECH_AUTH_CLIENT_ID", "LEARTECH_AUTH_CLIENT_SECRET",
			"LEARTECH_AUTH_TARGET_AUDIENCE", "LEARTECH_AUTH_SERVER_URL",
			"LEARTECH_AUTH_REQUIRED", "LEARTECH_AUTH_REQUIRED_SCOPES",
		} {
			t.Setenv(k, "x")
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("unset %s: %v", k, err)
			}
		}
		if got := InertAuthEnvSet(); len(got) != 0 {
			t.Fatalf("InertAuthEnvSet() = %v on a clean environment", got)
		}
	})

	t.Run("names a set-but-ignored credential", func(t *testing.T) {
		t.Setenv("LEARTECH_AUTH_CLIENT_ID", "catalog-mcp")
		got := InertAuthEnvSet()
		if len(got) != 1 || got[0] != "LEARTECH_AUTH_CLIENT_ID" {
			t.Fatalf("InertAuthEnvSet() = %v, want exactly [LEARTECH_AUTH_CLIENT_ID]", got)
		}
	})

	// The decoy. SERVER_URL must be reported, because an operator setting it
	// almost certainly believes they are configuring the issuer.
	t.Run("names LEARTECH_AUTH_SERVER_URL, which does not set the issuer", func(t *testing.T) {
		t.Setenv("LEARTECH_AUTH_SERVER_URL", "https://hydra.example")
		t.Setenv("LEARTECH_AUTH_ISSUER", "https://real-issuer.example")
		t.Setenv("LEARTECH_AUTH_AUDIENCE", "leartech-catalog-mcp")

		found := false
		for _, k := range InertAuthEnvSet() {
			if k == "LEARTECH_AUTH_SERVER_URL" {
				found = true
			}
		}
		if !found {
			t.Error("LEARTECH_AUTH_SERVER_URL was not reported as inert")
		}

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Auth.Issuer != "https://real-issuer.example" {
			t.Errorf("Auth.Issuer = %q — LEARTECH_AUTH_SERVER_URL must not influence it", cfg.Auth.Issuer)
		}
	})

	// Whitespace-only is not "set": reporting it would be noise, and an
	// operator clearing a value by blanking it should see it disappear.
	t.Run("whitespace-only is not reported", func(t *testing.T) {
		t.Setenv("LEARTECH_AUTH_CLIENT_SECRET", "   ")
		for _, k := range InertAuthEnvSet() {
			if k == "LEARTECH_AUTH_CLIENT_SECRET" {
				t.Error("a whitespace-only value was reported as set")
			}
		}
	})
}
