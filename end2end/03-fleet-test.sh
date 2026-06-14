#!/usr/bin/env bash
# 03-fleet-test.sh — staging-only cross-service SDK + auth proof.
#
# Skips in PR/preview contexts (STAGING_URL unset). In staging contexts
# (arrivals-observer dispatched Job), this script:
#
#   1. Drives the OAuth2 authorization_code+PKCE flow against the
#      staging auth-service to mint a Bearer access_token (same path
#      the leartech-auth-ui frontends use to authenticate).
#   2. Calls THIS template's /api/v1/fleet-test with the Bearer.
#   3. Asserts HTTP 200 + JSON `success: true`.
#
# The /fleet-test handler in internal/handlers/fleettest.go then uses
# each peer template's published Go SDK (from
# github.com/mikelear/leartech-go-packages/<svc>) to hit each peer's
# /api/v1/example with the same forwarded Bearer. A `success: true`
# response proves the full cross-service chain:
#
#   - Hydra OAuth flow mints a token ✓
#   - This template's BearerAuth middleware accepts the token ✓
#   - Peer Go SDKs install + compile + call peers ✓
#   - Peers' auth wiring accepts the same token (multi-aud or shared aud) ✓
#
# Failure of any peer call shows up in the response body's `results[]`,
# and the script exits 1 so run.sh marks the test failed in results.json,
# which the arrivals-observer surfaces as Arrival.phase=Failed, which
# leartech-gate's quill blocks promotion on.

set -eo pipefail

# Skip-in-PR gate. STAGING_URL is set ONLY by the arrivals-observer's
# dispatched Job; the catalog end2end task (PR previews) sets PREVIEW_URL.
# This check mirrors 02-fleet-smoke.sh in the angular template.
if [ -z "${STAGING_URL:-}" ]; then
  echo "[fleet-test] STAGING_URL unset — running in PR/preview context, skipping"
  exit 0
fi

: "${USER_EMAIL:?USER_EMAIL must be supplied via the observer's auth-service-test-user secret}"
: "${USER_PASSWORD:?USER_PASSWORD must be supplied via the observer's auth-service-test-user secret}"

# STAGING_HOST_BASE is the canonical bare cluster domain
# (e.g. jx-staging.az.leartech.com) — supplied by arrivals-observer
# 0.0.27+ alongside STAGING_URL. Peer URLs compose as
# `https://<peer>-${STAGING_HOST_BASE}`.
#
# Fallback for older observers (or local invocation): parse it out of
# STAGING_URL — strip the leading `<service>-` prefix from the host.
if [ -z "${STAGING_HOST_BASE:-}" ]; then
  STAGING_HOST_BASE=$(printf '%s\n' "$STAGING_URL" \
    | sed -E 's|^https?://[^./]+-(jx-staging\.[^/]+)/?.*$|\1|')
  if [ -z "$STAGING_HOST_BASE" ] || [ "$STAGING_HOST_BASE" = "$STAGING_URL" ]; then
    echo "[fleet-test] could not derive STAGING_HOST_BASE from STAGING_URL=$STAGING_URL — aborting"
    exit 1
  fi
  echo "[fleet-test] STAGING_HOST_BASE unset; derived as ${STAGING_HOST_BASE} (observer <0.0.27?)"
fi

HYDRA="https://hydra-${STAGING_HOST_BASE}"
AUTH_API="https://leartech-auth-service-${STAGING_HOST_BASE}"
AUTH_UI="https://leartech-auth-ui-${STAGING_HOST_BASE}"
# Use the auth-service URL as the redirect_uri — it's a registered
# redirect for the frontend-services client. We never actually navigate
# to it; we just need a registered value Hydra accepts.
REDIRECT_URI="${AUTH_UI}/auth/callback"
CLIENT_ID="${CLIENT_ID:-frontend-services}"

COOKIE_JAR=$(mktemp)
trap 'rm -f "$COOKIE_JAR"' EXIT

base64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
VERIFIER=$(openssl rand 32 | base64url)
CHALLENGE=$(printf '%s' "$VERIFIER" | openssl dgst -sha256 -binary | base64url)
STATE=$(openssl rand -hex 16)
NONCE=$(openssl rand -hex 16)

echo "[fleet-test] host_base=${STAGING_HOST_BASE} auth=${AUTH_API}"

# --- 1. Hydra /oauth2/auth → login_challenge ----------------------------
#
# Request multi-aud token (RFC 8707). The forwarded bearer needs `aud`
# to contain BOTH this template AND each peer it calls via SDK.
# Requesting all backend audiences gives a multi-aud token that
# traverses the fleet (matches the forwarded-bearer S2S model).
AUD_PARAM="leartech-rust-service-template+leartech-catalog-mcp+leartech-dotnet-service-template+leartech-auth-service"
R1=$(curl -sSI -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
  "${HYDRA}/oauth2/auth?client_id=${CLIENT_ID}&response_type=code&scope=openid+offline&redirect_uri=${REDIRECT_URI}&state=${STATE}&nonce=${NONCE}&code_challenge=${CHALLENGE}&code_challenge_method=S256&audience=${AUD_PARAM}")
LOGIN_CHALLENGE=$(echo "$R1" | awk '/^[Ll]ocation:/{print $2}' | tr -d '\r' \
  | sed -n 's/.*login_challenge=\([^&]*\).*/\1/p')
[ -n "$LOGIN_CHALLENGE" ] || { echo "FAIL: no login_challenge"; echo "$R1" | head -5; exit 1; }
echo "[fleet-test] login_challenge obtained"

# --- 2. POST credentials → redirect_to (with login_verifier) -----------
LOGIN_REDIR=$(curl -sS -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${USER_EMAIL}\",\"password\":\"${USER_PASSWORD}\"}" \
  "${AUTH_API}/api/auth/login?login_challenge=${LOGIN_CHALLENGE}" \
  | python3 -c "import json,sys; print(json.load(sys.stdin).get('redirect_to',''))")
[ -n "$LOGIN_REDIR" ] || { echo "FAIL: no redirect_to in login response"; exit 1; }
echo "[fleet-test] login accepted"

# --- 3. Follow login_verifier → consent_challenge ----------------------
R3=$(curl -sSI -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$LOGIN_REDIR")
CONSENT_CHALLENGE=$(echo "$R3" | awk '/^[Ll]ocation:/{print $2}' | tr -d '\r' \
  | sed -n 's/.*consent_challenge=\([^&]*\).*/\1/p')
[ -n "$CONSENT_CHALLENGE" ] || { echo "FAIL: no consent_challenge"; exit 1; }
echo "[fleet-test] consent_challenge obtained"

# --- 4. Auto-accept consent ---------------------------------------------
R4=$(curl -sS -i -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
  "${AUTH_API}/api/auth/consent?consent_challenge=${CONSENT_CHALLENGE}")
CONSENT_REDIR=$(echo "$R4" | awk '/^[Ll]ocation:/{print $2}' | tr -d '\r' | head -1)
[ -n "$CONSENT_REDIR" ] || { echo "FAIL: no consent redirect"; exit 1; }
echo "[fleet-test] consent accepted"

# --- 5. Follow consent_verifier → authorization code -------------------
R5=$(curl -sS -i -b "$COOKIE_JAR" -c "$COOKIE_JAR" "$CONSENT_REDIR")
CODE=$(echo "$R5" | awk '/^[Ll]ocation:/{print $2}' | tr -d '\r' \
  | sed -n 's/.*[?&]code=\([^&]*\).*/\1/p')
[ -n "$CODE" ] || { echo "FAIL: no auth code"; exit 1; }
echo "[fleet-test] authorization code obtained"

# --- 6. Token exchange → access_token ----------------------------------
TOKEN_JSON=$(curl -sS -X POST "${HYDRA}/oauth2/token" \
  -d "grant_type=authorization_code" \
  -d "code=${CODE}" \
  -d "redirect_uri=${REDIRECT_URI}" \
  -d "client_id=${CLIENT_ID}" \
  -d "code_verifier=${VERIFIER}")
ACCESS_TOKEN=$(printf '%s' "$TOKEN_JSON" \
  | python3 -c "import json,sys; print(json.load(sys.stdin).get('access_token',''))")
[ -n "$ACCESS_TOKEN" ] || { echo "FAIL: no access_token"; echo "$TOKEN_JSON"; exit 1; }
echo "[fleet-test] access_token obtained ($(printf %s "$ACCESS_TOKEN" | wc -c) bytes)"

# --- 7. Call /api/v1/fleet-test with the Bearer ------------------------
HTTP_CODE=$(mktemp)
BODY=$(mktemp)
trap 'rm -f "$COOKIE_JAR" "$HTTP_CODE" "$BODY"' EXIT

curl -sS -o "$BODY" -w '%{http_code}' \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "Accept: application/json" \
  "${STAGING_URL}/api/v1/fleet-test" > "$HTTP_CODE" || true

CODE_VAL=$(cat "$HTTP_CODE")
echo "[fleet-test] HTTP ${CODE_VAL}"
echo "[fleet-test] body:"
cat "$BODY"
echo

if [ "$CODE_VAL" != "200" ]; then
  echo "[fleet-test] FAIL: expected 200 from /api/v1/fleet-test, got ${CODE_VAL}"
  exit 1
fi

# Parse response: success must be true, every result.ok must be true.
SUCCESS=$(jq -r '.success' < "$BODY" 2>/dev/null || echo "")
if [ "$SUCCESS" != "true" ]; then
  echo "[fleet-test] FAIL: response.success != true"
  echo "[fleet-test] per-peer detail:"
  jq -r '.results[] | "  \(.peer): http=\(.http_code) ok=\(.ok) \(.message)"' < "$BODY" 2>/dev/null || true
  exit 1
fi

PEER_COUNT=$(jq -r '.results | length' < "$BODY")
echo "[fleet-test] PASS — all ${PEER_COUNT} peer SDK calls succeeded"
