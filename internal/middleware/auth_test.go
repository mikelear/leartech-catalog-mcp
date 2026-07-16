package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mikelear/leartech-go-common/pkg/auth"
)

// jwksMockServer serves a JWKS for a freshly-minted RSA key so tests can
// sign JWTs the middleware will validate. The mock exposes ONLY
// `/.well-known/jwks.json` — the whole point of the inbound-only
// Verifier is that it validates without ever needing an /oauth2/token
// endpoint. The mock's absence of that endpoint is proof the Verifier
// path needs no client-credentials wiring.
func jwksMockServer(t *testing.T) (*httptest.Server, *rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	kid := "test-kid-catalog-mcp"
	pub := &key.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":%q,"n":%q,"e":%q}]}`, kid, n, e)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/jwks.json" {
			_, _ = w.Write([]byte(jwks))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	return srv, key, kid
}

// signTestToken mints an RS256-signed JWT with the supplied claims —
// signing key + kid must match jwksMockServer.
func signTestToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// baseConfig returns a full, valid auth.VerifierConfig pointed at the
// JWKS mock. Tests that want to exercise a specific failure mutate it.
// Note: NO client_id / client_secret / server_url — the whole point of
// the inbound-only Verifier is that a pure resource server (validate
// but never mint) needs neither.
func baseConfig(serverURL string) auth.VerifierConfig {
	return auth.VerifierConfig{
		Issuer:   serverURL,
		Audience: "leartech-catalog-mcp",
	}
}

// TestBearerAuth_FailClosedOnMissingConfig is the C1 anti-fail-open canary
// for the v1.1.0 Verifier path: go-common's NewVerifier refuses to build
// unless Issuer AND Audience are both populated. BearerAuth propagates
// that error rather than log.Fatal so main() can surface a clean crash —
// never a running-but-unauthenticated pod.
//
// Explicitly locks in the "NO client-cred config consulted" contract:
// missing issuer/audience is a rejection, but there is no ServerURL /
// ClientID / ClientSecret to be missing — those fields don't exist on
// VerifierConfig.
func TestBearerAuth_FailClosedOnMissingConfig(t *testing.T) {
	full := baseConfig("https://hydra.example.com")
	cases := []struct {
		name    string
		mutate  func(auth.VerifierConfig) auth.VerifierConfig
		wantEnv string
	}{
		{"empty Issuer", func(c auth.VerifierConfig) auth.VerifierConfig { c.Issuer = ""; return c }, "LEARTECH_AUTH_ISSUER"},
		{"empty Audience (C1: no noop-with-empty-aud path)", func(c auth.VerifierConfig) auth.VerifierConfig { c.Audience = ""; return c }, "LEARTECH_AUTH_AUDIENCE"},
		{"both empty", func(_ auth.VerifierConfig) auth.VerifierConfig { return auth.VerifierConfig{} }, "LEARTECH_AUTH_ISSUER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BearerAuth(tc.mutate(full))
			if err == nil {
				t.Fatal("BearerAuth must error when required config is missing — no noop / pass-through path")
			}
			if !strings.Contains(err.Error(), tc.wantEnv) {
				t.Fatalf("error must name the missing field %q, got: %v", tc.wantEnv, err)
			}
			if !strings.Contains(err.Error(), "inbound token validation is mandatory") {
				t.Fatalf("error must cite the fail-closed contract, got: %v", err)
			}
		})
	}
}

// TestBearerAuth_NoClientCredsRequired is the core initiative canary:
// catalog-mcp is a validate-only resource server, so the Verifier must
// construct with ONLY issuer + audience. If BearerAuth ever starts
// demanding client_id / client_secret / server_url again, this test
// breaks and the regression is caught immediately.
func TestBearerAuth_NoClientCredsRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _, _ := jwksMockServer(t)
	defer srv.Close()

	// Deliberately zero client-cred config — VerifierConfig has no such
	// fields to zero. Just issuer + audience.
	mw, err := BearerAuth(auth.VerifierConfig{
		Issuer:   srv.URL,
		Audience: "leartech-catalog-mcp",
	})
	if err != nil {
		t.Fatalf("BearerAuth must construct with issuer+audience only — no client creds: %v", err)
	}
	if mw == nil {
		t.Fatal("BearerAuth returned nil middleware without error")
	}
}

// TestBearerAuth_AudienceEnforced is the C1 core: a validly-signed
// token whose `aud` claim is NOT "leartech-catalog-mcp" must be
// rejected. This is the failure mode the initiative closes — the old
// v0.4.1 `Middleware(nil)` with empty audience accepted ANY signed
// token from the same issuer.
func TestBearerAuth_AudienceEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, key, kid := jwksMockServer(t)
	defer srv.Close()

	mw, err := BearerAuth(baseConfig(srv.URL))
	if err != nil {
		t.Fatalf("BearerAuth: %v", err)
	}

	cases := []struct {
		name       string
		aud        any
		wantStatus int
	}{
		{"aud=leartech-catalog-mcp (string) → 200 pass-through", "leartech-catalog-mcp", http.StatusOK},
		{"aud=[leartech-catalog-mcp] (array) → 200 pass-through", []string{"leartech-catalog-mcp"}, http.StatusOK},
		{"aud=[other,leartech-catalog-mcp] (array containing) → 200", []string{"other", "leartech-catalog-mcp"}, http.StatusOK},
		{"aud=leartech-orchestrator → 401 (wrong audience)", "leartech-orchestrator", http.StatusUnauthorized},
		{"aud=[some-other-svc] (array not containing) → 401", []string{"some-other-svc"}, http.StatusUnauthorized},
		{"aud missing entirely → 401", nil, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := jwt.MapClaims{
				"iss": srv.URL,
				"sub": "test-user",
				"exp": time.Now().Add(1 * time.Hour).Unix(),
				"iat": time.Now().Unix(),
			}
			if tc.aud != nil {
				claims["aud"] = tc.aud
			}
			token := signTestToken(t, key, kid, claims)

			w := httptest.NewRecorder()
			gc, _ := gin.CreateTestContext(w)
			gc.Request = httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
			gc.Request.Header.Set("Authorization", "Bearer "+token)
			mw(gc)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (aud=%v)", w.Code, tc.wantStatus, tc.aud)
			}
		})
	}
}

// TestBearerAuth_IssuerEnforced confirms RFC 7519 issuer binding is
// enforced even for tokens signed by a key the JWKS advertises. A token
// whose `iss` claim points somewhere else is rejected regardless of
// signature validity.
func TestBearerAuth_IssuerEnforced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, key, kid := jwksMockServer(t)
	defer srv.Close()

	mw, err := BearerAuth(baseConfig(srv.URL))
	if err != nil {
		t.Fatalf("BearerAuth: %v", err)
	}

	// Sign a token whose iss points elsewhere — must be rejected.
	token := signTestToken(t, key, kid, jwt.MapClaims{
		"iss": "https://evil.example.com",
		"sub": "test-user",
		"aud": []string{"leartech-catalog-mcp"},
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	w := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(w)
	gc.Request = httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
	gc.Request.Header.Set("Authorization", "Bearer "+token)
	mw(gc)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (wrong iss)", w.Code)
	}
}

// TestBearerAuth_RejectsMissingHeader confirms a request with no
// Authorization header returns 401 — the base fail-closed behaviour
// unchanged from v0.4.1.
func TestBearerAuth_RejectsMissingHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _, _ := jwksMockServer(t)
	defer srv.Close()

	mw, err := BearerAuth(baseConfig(srv.URL))
	if err != nil {
		t.Fatalf("BearerAuth: %v", err)
	}

	w := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(w)
	gc.Request = httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
	// no Authorization header
	mw(gc)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (missing bearer)", w.Code)
	}
}

// TestBearerAuth_RejectsMalformedBearer confirms garbage in the
// Authorization header is 401 rather than pass-through.
func TestBearerAuth_RejectsMalformedBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv, _, _ := jwksMockServer(t)
	defer srv.Close()

	mw, err := BearerAuth(baseConfig(srv.URL))
	if err != nil {
		t.Fatalf("BearerAuth: %v", err)
	}

	w := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(w)
	gc.Request = httptest.NewRequest(http.MethodGet, "/api/v1/example", nil)
	gc.Request.Header.Set("Authorization", "Bearer not-a-jwt")
	mw(gc)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (malformed jwt)", w.Code)
	}
}
