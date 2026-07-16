// Package config loads 12-factor configuration via envconfig.
// Per golden-standard: no config files baked into the image.
package config

import (
	"fmt"

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

	// AuthRequired gates the bearer middleware on /api/v1/*.
	//
	// C1 (auth hardening): default `true` — production charts MUST run
	// with auth on. Setting `false` is a deliberate local-dev / smoke-
	// test opt-out; there is NO "auth on but no audience configured"
	// path — with AuthRequired=true, missing issuer / audience crash
	// the pod at boot via go-common v1.1.0's fail-closed NewVerifier.
	// Never noop, never fail-open.
	AuthRequired bool `envconfig:"AUTH_REQUIRED" default:"true"`

	// Auth is go-common v1.1.0's INBOUND-ONLY VerifierConfig — used to
	// validate bearer tokens on /api/v1/*. catalog-mcp is a pure resource
	// server: it validates JWTs but never mints them, so we deliberately
	// use VerifierConfig (issuer + audience) rather than the full Config
	// (which also carries client_credentials fields for OUTBOUND minting).
	//
	// Envconfig populates each field as AUTH_<FIELDNAME> (uppercased):
	//   - VerifierConfig.Issuer   → AUTH_ISSUER
	//   - VerifierConfig.Audience → AUTH_AUDIENCE
	//   - VerifierConfig.JWKSURL  → AUTH_JWKSURL (optional override)
	//
	// The chart's deployment.yaml supplies AUTH_ISSUER and AUTH_AUDIENCE;
	// when AuthRequired is true and either is empty the pod fails to boot
	// (the intended fail-closed signal). NO LEARTECH_AUTH_SERVER_URL /
	// CLIENT_ID / CLIENT_SECRET are required or consulted — that was the
	// crash-loop this initiative closes: catalog was mis-using the outbound
	// ServiceAuthClient path for pure inbound validation.
	Auth auth.VerifierConfig `envconfig:"AUTH"`

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
	return &c, nil
}
