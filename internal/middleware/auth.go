// Package middleware provides HTTP middleware for the service.
package middleware

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mikelear/leartech-go-common/pkg/auth"
)

// BearerAuth builds the leartech-go-common bearer middleware from the
// supplied auth.Config. It returns an error rather than log.Fatal so the
// caller (main) can wrap the failure with context — the pod must crash
// on any auth-config error (auth-hardening C1: never noop, never
// fail-open).
//
// go-common v1.0.0 semantics (fail-closed, load-bearing here):
//
//   - NewServiceClient returns an error unless ServerURL, ClientID,
//     ClientSecret AND Audience are ALL populated. There is no
//     "Middleware(nil) with empty audience → accept any token" path any
//     more — that was the scan finding this initiative closes.
//   - The returned Middleware validates JWKS signature + RFC 8707
//     audience binding on every request; a token whose `aud` is not
//     our configured Audience (`leartech-catalog-mcp` in production)
//     is rejected with 401.
//
// Callers must not silently ignore the returned error. main() log.Fatals
// on it so a miswiring produces a crashed pod that ops can see — never
// an unauthenticated running pod.
func BearerAuth(cfg auth.Config) (gin.HandlerFunc, error) {
	client, err := auth.NewServiceClient(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("auth: build service client (audience=%q, serverURL=%q): %w",
			cfg.Audience, cfg.ServerURL, err)
	}
	// Middleware(nil) = "any authenticated + audience-bound token" — the
	// audience check is applied inside decodeToken() regardless of
	// requiredPerms, so this is NOT the fail-open path the old
	// v0.4.1 behaviour was; go-common v1.0.0 enforces audience on
	// every request.
	return client.Middleware(nil), nil
}
