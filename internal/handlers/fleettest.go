// Template-only /api/v1/fleet-test handler. Symmetric to the rust
// template's src/fleet_test.rs + the dotnet template's
// FleetTestEndpoints.cs:
//
//   - Inbound bearer is forwarded to each peer template's published Go
//     SDK (from github.com/mikelear/leartech-go-packages/<svc>)
//   - Each peer's /api/v1/example is invoked with that bearer
//   - Aggregated `success: true` proves the full SDK + auth chain
//
// Gated by config field FleetTestEnabled (chart value
// `fleetTest.enabled`, default false). Cloned services should DELETE
// this file along with the peer-SDK go.mod deps + the chart's
// fleetTest stanza, since none of it is part of the generic service
// skeleton.

package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	rustsvc "github.com/mikelear/leartech-go-packages/rustservicetemplate"
)

// peerResult is one entry in the aggregate response's results[].
type peerResult struct {
	Peer       string `json:"peer"`
	HTTPCode   int    `json:"http_code"`
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	DurationMs int64  `json:"duration_ms"`
}

// fleetTestResponse is the JSON body the handler returns.
type fleetTestResponse struct {
	Success bool         `json:"success"`
	Summary string       `json:"summary"`
	Results []peerResult `json:"results"`
}

// FleetTestHandler exposes the /fleet-test cross-service SDK probe.
// Removed entirely when cloning the template into a real service.
type FleetTestHandler struct{}

// NewFleetTestHandler constructs the handler. No deps — peer SDKs
// are instantiated per-request because each one needs the inbound
// bearer wired into a request editor.
func NewFleetTestHandler() *FleetTestHandler {
	return &FleetTestHandler{}
}

// RegisterRoutes wires /fleet-test onto the supplied (auth-guarded)
// router group. The group must already be `.Use(middleware.BearerAuth(...))`
// — otherwise the forwarded token is unvalidated.
func (h *FleetTestHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/fleet-test", h.get)
}

// get handles GET /api/v1/fleet-test. It extracts the validated bearer,
// derives the cluster host from the inbound Host header, and fans out
// to each peer template's published Go SDK with the same bearer.
//
// @Summary Fleet test endpoint — calls peer template SDKs to prove cross-service auth + SDK wiring.
// @Tags fleet-test
// @Security BearerAuth
// @Produce json
// @Success 200 {object} fleetTestResponse
// @Failure 401 {object} map[string]string
// @Router /api/v1/fleet-test [get]
func (h *FleetTestHandler) get(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		c.JSON(http.StatusUnauthorized, fleetTestResponse{
			Success: false,
			Summary: "no bearer token on request — BearerAuth should have rejected this",
			Results: []peerResult{},
		})
		return
	}
	bearer := strings.TrimPrefix(auth, "Bearer ")
	clusterHost := clusterHostFromRequest(c)

	// Peers: each entry is (display name, peer URL). Today only the
	// rust template's Go SDK is published into leartech-go-packages.
	// The dotnet template's Go SDK will land on its next release —
	// add it as a peer + go.mod require entry then.
	peers := []struct {
		name string
		url  string
	}{
		{
			"leartech-rust-service-template",
			"https://leartech-rust-service-template-jx-staging." + clusterHost,
		},
	}

	results := make([]peerResult, 0, len(peers))
	for _, p := range peers {
		results = append(results, h.callRustPeer(c.Request.Context(), p.name, p.url, bearer))
	}

	passed := 0
	for _, r := range results {
		if r.OK {
			passed++
		}
	}

	c.JSON(http.StatusOK, fleetTestResponse{
		Success: passed == len(results),
		Summary: summary(passed, len(results)),
		Results: results,
	})
}

func summary(passed, total int) string {
	// Cheap conversion that avoids fmt for hot-path; %d via fmt is fine too.
	return itoa(passed) + "/" + itoa(total) + " peer calls passed"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// callRustPeer invokes the rust template's Go SDK with the supplied
// bearer and records the verdict. Each peer with a published Go SDK
// gets its own callXxxPeer method — the SDK types differ enough that
// generics aren't worth the indirection.
func (h *FleetTestHandler) callRustPeer(ctx context.Context, name, baseURL, bearer string) peerResult {
	start := time.Now()
	// Forward the bearer via a RequestEditor — the cleanest hook the
	// generated SDK exposes. Anything we set on the *http.Request gets
	// applied to every operation on the Client.
	bearerEditor := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+bearer)
		return nil
	}

	client, err := rustsvc.NewClient(baseURL, rustsvc.WithRequestEditorFn(bearerEditor))
	if err != nil {
		return peerResult{
			Peer:       name,
			HTTPCode:   0,
			OK:         false,
			Message:    "SDK client init failed: " + err.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	resp, err := client.Example(ctx)
	if err != nil {
		return peerResult{
			Peer:       name,
			HTTPCode:   0,
			OK:         false,
			Message:    "SDK call failed: " + err.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	ok := resp.StatusCode == http.StatusOK
	msg := "SDK call returned 200 — peer auth wiring validated our token"
	if !ok {
		msg = "peer returned HTTP " + itoa(resp.StatusCode)
	}
	return peerResult{
		Peer:       name,
		HTTPCode:   resp.StatusCode,
		OK:         ok,
		Message:    msg,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

// clusterHostFromRequest extracts the cluster suffix
// (jx.leartech.com / az.leartech.com) from the incoming Host header so
// the same handler works on both clusters without redeploy. Falls
// back to jx.leartech.com defensively (every real ingress sets Host).
func clusterHostFromRequest(c *gin.Context) string {
	host := c.Request.Host
	// Host looks like `leartech-catalog-mcp-jx-staging.jx.leartech.com`.
	// Strip everything up to and including `-jx-staging.`.
	if idx := strings.Index(host, "-jx-staging."); idx >= 0 {
		return host[idx+len("-jx-staging."):]
	}
	return "jx.leartech.com"
}
