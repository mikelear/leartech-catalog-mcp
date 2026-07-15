#!/usr/bin/env bash
# 02-auth-fail-closed.sh — C1 (auth hardening) end-to-end proof.
#
# Verifies the deployed pod is running fail-closed:
#
#   - /api/v1/example (auth-required) MUST return 401 when hit with no
#     bearer, and 401 when hit with a random unsigned bearer. This is
#     the guarantee C1 added — the old v0.4.1 behaviour with an empty
#     audience `Middleware(nil)` accepted any signed token, or (worse)
#     any token at all if ServerURL was unset (noop path).
#
# Runs against BOTH preview and staging (dual-mode env like 01-smoke).
# Preview pods carry a placeholder issuer (hydra.preview-default.invalid)
# so JWKS lookup always fails; every token is 401 by design.
#
# Explicitly does NOT try to obtain a valid token — that would require a
# live Hydra plus s2s client creds we don't have in-pipeline. The
# fail-closed proof is the important side; the pass-through path is
# covered by unit tests + upstream integration.

set -eo pipefail

BASE_URL="${STAGING_URL:-${PREVIEW_URL:-}}"
if [ -z "$BASE_URL" ]; then
  echo "[auth-fail-closed] neither STAGING_URL nor PREVIEW_URL set — nothing to test against; aborting" >&2
  exit 1
fi
MODE="preview"
[ -n "${STAGING_URL:-}" ] && MODE="staging"
echo "[auth-fail-closed] mode=${MODE} base=${BASE_URL}"

expect_status() {
  local method="$1" path="$2" want="$3" hdr="${4:-}"
  local code
  if [ -n "$hdr" ]; then
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X "$method" -m 10 -H "$hdr" "${BASE_URL}${path}" 2>/dev/null || true)
  else
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X "$method" -m 10 "${BASE_URL}${path}" 2>/dev/null || true)
  fi
  [ -z "$code" ] && code="000"
  if [ "$code" = "$want" ]; then
    printf '[auth-fail-closed] %-4s %-25s %s\n' "$method" "$path" "HTTP $code ✓"
  else
    printf '[auth-fail-closed] %-4s %-25s %s\n' "$method" "$path" "HTTP $code (want $want) ✗"
    return 1
  fi
}

# /api/v1/example with no Authorization header → 401
expect_status GET /api/v1/example 401

# /api/v1/example with a garbage bearer → 401 (not 500, not 200)
expect_status GET /api/v1/example 401 "Authorization: Bearer not-a-valid-jwt"

# /api/v1/example with an unsigned-token-shaped garbage → 401
# (three base64-ish segments separated by `.` — same shape as a JWT
# so we exercise the JWKS-verify path, not just the header parser).
expect_status GET /api/v1/example 401 "Authorization: Bearer eyJ.eyJ.sig"

echo "[auth-fail-closed] all 3 checks passed — /api/v1/example is fail-closed"
