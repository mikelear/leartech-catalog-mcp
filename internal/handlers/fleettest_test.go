// Tests for the template-only /api/v1/fleet-test handler. These prove
// the SDK + auth-forwarding chain works the way `fleettest.go`
// promises, AND they keep the package's coverage above the gate's
// floor — without tests, fleettest.go's seven uncovered functions
// drag the line-rate down ~12% on go-test.
//
// Cloned services should DELETE this file along with `fleettest.go`,
// the peer-SDK go.mod deps, and the chart's fleetTest stanza.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestClusterHostFromRequest covers the host-parsing edge cases. The
// rule is "everything after the first `-jx-staging.` literal"; the
// fallback is `jx.leartech.com` when that literal isn't present.
func TestClusterHostFromRequest(t *testing.T) {
	cases := []struct {
		name string
		host string
		want string
	}{
		{
			name: "gcp staging",
			host: "leartech-catalog-mcp-jx-staging.jx.leartech.com",
			want: "jx.leartech.com",
		},
		{
			name: "azure staging",
			host: "leartech-catalog-mcp-jx-staging.az.leartech.com",
			want: "az.leartech.com",
		},
		{
			name: "preview (no -jx-staging suffix) falls back",
			host: "leartech-catalog-mcp-pr16.jx.leartech.com",
			want: "jx.leartech.com",
		},
		{
			name: "empty host falls back",
			host: "",
			want: "jx.leartech.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			c.Request.Host = tc.host
			got := clusterHostFromRequest(c)
			if got != tc.want {
				t.Errorf("cluster host = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestItoa covers the inline integer-formatting helper. Tiny pure
// function; table test keeps the regression net cheap.
func TestItoa(t *testing.T) {
	cases := map[int]string{
		0:    "0",
		1:    "1",
		9:    "9",
		10:   "10",
		200:  "200",
		1234: "1234",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestSummary covers the per-peer summary line shape — the
// arrivals-observer surfaces this in the Arrival status message so
// the contract matters.
func TestSummary(t *testing.T) {
	if got := summary(0, 0); got != "0/0 peer calls passed" {
		t.Errorf("summary(0,0) = %q", got)
	}
	if got := summary(2, 2); got != "2/2 peer calls passed" {
		t.Errorf("summary(2,2) = %q", got)
	}
	if got := summary(1, 3); got != "1/3 peer calls passed" {
		t.Errorf("summary(1,3) = %q", got)
	}
}

// TestFleetTestHandler_NoBearerReturns401 confirms the handler refuses
// to call any peer SDKs when no Bearer is present. In production the
// BearerAuth middleware short-circuits first; this is a defence-in-depth
// check for misconfigured route groups.
func TestFleetTestHandler_NoBearerReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewFleetTestHandler()
	r := gin.New()
	rg := r.Group("/api/v1")
	h.RegisterRoutes(rg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet-test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without bearer, got %d", w.Code)
	}
	var resp fleetTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false when no bearer is forwarded")
	}
	if !strings.Contains(resp.Summary, "no bearer token") {
		t.Errorf("summary = %q, want it to mention missing bearer", resp.Summary)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected empty results, got %d entries", len(resp.Results))
	}
}

// TestFleetTestHandler_PeerReachFails_FlagsNotOk drives the handler
// end-to-end with a Bearer that survives extraction, then asserts the
// per-peer result records the network failure (peer URL is unreachable
// because it's synthesised from the request Host, which httptest won't
// match). This proves the failure path of `callRustPeer` — the
// success path is exercised by the staging end2end pack against real
// peer deployments.
func TestFleetTestHandler_PeerReachFails_FlagsNotOk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewFleetTestHandler()
	r := gin.New()
	rg := r.Group("/api/v1")
	h.RegisterRoutes(rg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet-test", nil)
	req.Header.Set("Authorization", "Bearer fake-token-for-test")
	// Force the cluster host to a non-routable suffix so peer calls
	// fail fast rather than hitting public DNS / leaking traffic.
	req.Host = "leartech-catalog-mcp-jx-staging.invalid.example"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Handler always returns 200 — per-peer failures live in results[]
	// so the arrivals-observer can render granular diagnostics.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (per-peer fail rolls into body), got %d", w.Code)
	}
	var resp fleetTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false when peer is unreachable")
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one peer result entry")
	}
	for _, pr := range resp.Results {
		if pr.OK {
			t.Errorf("peer %q reported ok=true with unreachable host", pr.Peer)
		}
		if pr.HTTPCode != 0 {
			t.Errorf("peer %q: http_code=%d, want 0 for network failure", pr.Peer, pr.HTTPCode)
		}
		if !strings.Contains(strings.ToLower(pr.Message), "sdk call failed") {
			t.Errorf("peer %q: message=%q, want 'SDK call failed: ...'", pr.Peer, pr.Message)
		}
		if pr.DurationMs < 0 {
			t.Errorf("peer %q: duration_ms=%d, want >=0", pr.Peer, pr.DurationMs)
		}
	}
}
