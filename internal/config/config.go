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
	// path — with AuthRequired=true, missing issuer / audience / client
	// creds all crash the pod at boot via go-common's fail-closed
	// NewServiceClient. Never noop, never fail-open.
	AuthRequired bool `envconfig:"AUTH_REQUIRED" default:"true"`

	// Auth is the go-common ServiceAuthClient config — used both for the
	// bearer middleware guarding our API and for outbound auth to other
	// leartech services.
	//
	// Envconfig populates each field as AUTH_<FIELDNAME> (uppercased), so
	// go-common's Config.ServerURL is set via AUTH_SERVERURL, ClientID via
	// AUTH_CLIENTID, ClientSecret via AUTH_CLIENTSECRET, Audience via
	// AUTH_AUDIENCE. The chart's deployment.yaml supplies each; when
	// AuthRequired is true and any of these is empty the pod fails to
	// boot (the intended fail-closed signal).
	Auth auth.Config `envconfig:"AUTH"`

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
