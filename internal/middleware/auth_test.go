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
// sign JWTs the middleware will validate. The mock also hosts
// `/health/ready` (the ServiceClient's Ping endpoint) and `/oauth2/token`
// for completeness — neither is exercised by the middleware path but
// keeping them means the mock is drop-in for future s2s tests.
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
		switch r.URL.Path {
		case "/.well-known/jwks.json":
			_, _ = w.Write([]byte(jwks))
		case "/health/ready":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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

// baseConfig returns a full, valid auth.Config pointed at the JWKS mock.
// Tests that want to exercise a specific failure mutate it.
func baseConfig(serverURL string) auth.Config {
	return auth.Config{
		ServerURL:    serverURL,
		ClientID:     "catalog-mcp-test",
		ClientSecret: "test-secret",
		Audience:     "leartech-catalog-mcp",
	}
}

// TestBearerAuth_FailClosedOnMissingConfig is the C1 anti-fail-open canary:
// go-common v1.0.0's NewServiceClient refuses to build unless ServerURL,
// ClientID, ClientSecret AND Audience are all populated. BearerAuth
// propagates that error rather than log.Fatal so main() can surface a
// clean crash — never a running-but-unauthenticated pod.
func TestBearerAuth_FailClosedOnMissingConfig(t *testing.T) {
	full := baseConfig("https://hydra.example.com")
	cases := []struct {
		name    string
		mutate  func(auth.Config) auth.Config
		wantEnv string
	}{
		{"empty ServerURL (issuer unwired)", func(c auth.Config) auth.Config { c.ServerURL = ""; return c }, "SERVER_URL"},
		{"empty ClientID", func(c auth.Config) auth.Config { c.ClientID = ""; return c }, "CLIENT_ID"},
		{"empty ClientSecret", func(c auth.Config) auth.Config { c.ClientSecret = ""; return c }, "CLIENT_SECRET"},
		{"empty Audience (C1: no noop-with-empty-aud path)", func(c auth.Config) auth.Config { c.Audience = ""; return c }, "AUDIENCE"},
		{"all empty", func(_ auth.Config) auth.Config { return auth.Config{} }, "SERVER_URL"},
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
		})
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
