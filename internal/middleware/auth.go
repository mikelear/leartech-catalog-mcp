// Package middleware provides HTTP middleware for the service.
package middleware

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mikelear/leartech-go-common/pkg/auth"
)

// BearerAuth builds the leartech-go-common v1.1.0 inbound-only Verifier
// middleware from the supplied auth.VerifierConfig. It returns an error
// rather than log.Fatal so the caller (main) can wrap the failure with
// context — the pod must crash on any auth-config error (auth-hardening
// C1: never noop, never fail-open).
//
// go-common v1.1.0 semantics (fail-closed, load-bearing here):
//
//   - NewVerifier returns an error unless BOTH Issuer AND Audience are
//     populated. There is no runtime disable / noop path — a
//     mis-configured resource server refuses to boot rather than
//     silently accepting unvalidated tokens.
//   - The returned Middleware validates JWKS signature + RFC 7519 issuer
//     binding + RFC 8707 audience binding on every request; a token
//     whose `aud` claim is not our configured Audience
//     (`leartech-catalog-mcp` in production) is rejected with 401.
//   - VerifierConfig carries ONLY the inbound-validate role: no
//     ServerURL / ClientID / ClientSecret. That's the whole point of
//     separating the roles — a pure resource server (catalog-mcp) never
//     mints tokens, so it doesn't need client credentials. Requiring
//     them before was a mis-wiring that fail-closed'd the pod on boot
//     with "required config missing: LEARTECH_AUTH_SERVER_URL, CLIENT_ID,
//     CLIENT_SECRET" — this initiative closes that.
//
// Callers must not silently ignore the returned error. main() log.Fatals
// on it so a miswiring produces a crashed pod that ops can see — never
// an unauthenticated running pod.
func BearerAuth(cfg auth.VerifierConfig) (gin.HandlerFunc, error) {
	verifier, err := auth.NewVerifier(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("auth: build verifier (audience=%q, issuer=%q): %w",
			cfg.Audience, cfg.Issuer, err)
	}
	// Middleware(nil) = "any audience-validated signed token passes" —
	// audience + issuer + signature + exp are enforced inside
	// DecodeToken regardless of requiredPerms, so this is NOT a
	// fail-open path. Passing nil is the exact analogue of the old
	// ServiceClient.Middleware(nil) semantic; audience is now always
	// enforced because NewVerifier refuses empty Audience.
	return auth.Middleware(verifier, nil), nil
}
