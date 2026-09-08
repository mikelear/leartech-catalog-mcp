// Package config loads 12-factor configuration via envconfig.
// Per golden-standard: no config files baked into the image.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/kelseyhightower/envconfig"
	"github.com/mikelear/leartech-go-common/pkg/auth"
)

// Config is the envconfig-populated runtime configuration.
type Config struct {
	Port      string `envconfig:"PORT" default:"8080"`
	ClusterID string `envconfig:"CLUSTER_ID" default:""`

	// DatabaseURL is the postgres DSN. Format:
	// postgres://user:pass@host:port/dbname?sslmode=require
	//
	// Optional: if empty, the service runs in "shell mode" — no DB pool,
	// `/health/ready` returns ok without a DB ping. Used for the template's
	// own self-deploy to prove the chain end-to-end. Real services always
	// provide this via ExternalSecret.
	DatabaseURL string `envconfig:"DATABASE_URL"`

	// Auth is the go-common VERIFIER config — inbound token validation only.
	// catalog-mcp is a pure resource server: it validates JWTs and never mints
	// them, so VerifierConfig (issuer + audience) is the right role, not the
	// dual-role Config with its client_credentials fields.
	//
	// NOT populated by envconfig, and the `-` tag is load-bearing. It used to be
	// `envconfig:"AUTH"`, which maps each Go field name onto AUTH_<FIELDNAME> —
	// AUTH_ISSUER, AUTH_AUDIENCE. Those names match nothing any other service in
	// the estate renders and nothing go-common documents (its own tags are
	// LEARTECH_AUTH_*), so the same value had a different name here than
	// everywhere else. Populated explicitly in Load() from the standard names.
	Auth auth.VerifierConfig `envconfig:"-"`

	// FleetTestEnabled is a template-only flag — when true, register
	// the /api/v1/fleet-test endpoint that calls peer golden-template
	// Go SDKs to prove cross-service SDK + auth wiring. Cloned real
	// services should DELETE this field along with the handler and
	// chart stanza. Env var: FLEET_TEST_ENABLED (no AUTH prefix because
	// it's not auth-related — it's the template's own feature flag).
	FleetTestEnabled bool `envconfig:"FLEET_TEST_ENABLED" default:"false"`
}

// Load reads env and returns a populated Config.
func Load() (*Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return nil, fmt.Errorf("envconfig: %w", err)
	}
	c.Auth = loadAuthFromEnv()
	return &c, nil
}

// loadAuthFromEnv builds the inbound VerifierConfig from the estate-standard
// LEARTECH_AUTH_* names. Empty values pass through unchanged: NewVerifier
// decides whether the result is serviceable, and it refuses rather than
// degrading — there is no runtime disable path to fall back to.
func loadAuthFromEnv() auth.VerifierConfig {
	return auth.VerifierConfig{
		Issuer:   os.Getenv("LEARTECH_AUTH_ISSUER"),
		Audience: os.Getenv("LEARTECH_AUTH_AUDIENCE"),
		JWKSURL:  os.Getenv("LEARTECH_AUTH_JWKS_URL"),
	}
}

// inertAuthEnv lists LEARTECH_AUTH_* variables this service reads NO meaning
// from, so a set-but-ignored value is reported at boot rather than discarded.
// A resource server verifies with issuer + audience alone; SERVER_URL is the
// trap worth naming, because it reads like the issuer knob and is not one.
var inertAuthEnv = []string{
	"LEARTECH_AUTH_CLIENT_ID",
	"LEARTECH_AUTH_CLIENT_SECRET",
	"LEARTECH_AUTH_TARGET_AUDIENCE",
	"LEARTECH_AUTH_SERVER_URL",
	"LEARTECH_AUTH_REQUIRED",
	"LEARTECH_AUTH_REQUIRED_SCOPES",
}

// InertAuthEnvSet returns the inert LEARTECH_AUTH_* variables actually present
// and non-empty. Never fatal — an inert value is untidy, not unsafe.
func InertAuthEnvSet() []string {
	var set []string
	for _, k := range inertAuthEnv {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			set = append(set, k)
		}
	}
	return set
}
