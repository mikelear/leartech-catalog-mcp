# leartech-catalog-mcp

Catalog MCP — read-only inventory of leartech services for BA/Architect agents.
Foundation for V6-P10 + the new-product layer
(`project_new_product_layer_design`).

This service is bootstrapped from `leartech-go-service-template` and currently
ships the template's sample `/api/v1/example` endpoint so the platform's
smoke-test pipelines have something to exercise. Real catalog tools
(`list_services`, `get_service_shape`, `get_auth_pattern`, …) ship in the
v7-P1.4 follow-up initiative.

## What's in the box (today)

| Area | What | Where |
|---|---|---|
| HTTP | gin + zerolog + graceful shutdown | `cmd/server/main.go` |
| Health | `/health/live`, `/health/ready` (pings Postgres), unauthenticated | `internal/handlers/health.go` |
| Metrics | `/metrics` (Prometheus), scoped via NetworkPolicy | `internal/handlers/metrics.go` |
| OpenAPI | swaggo annotations → `docs/swagger.json` → swgui at `/docs`, raw spec at `/openapi.json` | `cmd/server/main.go` + handlers |
| Auth | `leartech-go-common/pkg/auth` bearer middleware on all `/api/v1/*` routes | `internal/middleware/auth.go` |
| Database | pgx pool (Postgres-only per golden std); goose migrations as Helm post-install job | `internal/db/pgx.go`, `charts/**/migrations-job.yaml`, `migrations/*.sql` |
| Config | envconfig (12-factor) | `internal/config/config.go` |
| Chart | uses `leartech-helm-library` for labels/securityContext/probes — consistent spine | `charts/leartech-catalog-mcp/` |
| Preview | Per-PR helmfile with env-templated URLs (no hardcoded cluster registries) | `preview/` |
| end2end | Smoke script wired into the shared end2end Tekton task | `end2end/` |
| Pipelines | PR: build+lint+test+coverage+vulnscan+security-scan+image-scan+dynamic-scan+ai-review+preview. Release: cosign-signed + cluster-suffixed tag + `jx promote` | `.lighthouse/jenkins-x/` |

## What's coming

| Phase | Adds |
|---|---|
| v7-P1.4 | Real MCP tools: `list_services`, `get_service_shape(name)`, `get_auth_pattern(name)` |
| v7-P1.5 | Auth integration / OIDC validation against `leartech-auth-service` |
| v7-P1.6 | MCP registration with `platform-mcps` discovery |

See `project_track_b_sequence` + `project_new_product_layer_design` for the
target shape.

## Local development

Six `make` targets — nothing else hidden. swag + golangci-lint are
auto-installed on first run of the corresponding target.

```bash
# Regenerate OpenAPI spec after editing handler annotations
make swag

# Lint (fetches leartech-pipeline-catalog base + merges with local overrides)
make lint

# Build
make build
./bin/server   # run — needs DATABASE_URL + AUTH_* env for full mode

# Tests
make test
make test-coverage

# Local DB + migrations
export DATABASE_URL='postgres://postgres:postgres@localhost:5432/leartech_catalog_mcp?sslmode=disable'
go install github.com/pressly/goose/v3/cmd/goose@latest
goose -dir migrations postgres "${DATABASE_URL}" up
```

## Release mechanics

Per `~/leartech/hub/shared-rules/conventions.md` § Golden release pattern:

- `jx-release-version --previous-version from-tag` (NOT `--tag` — would race on both clusters)
- Git tag is cluster-suffixed: `v0.1.0-gcp` / `v0.1.0-az`
- Image tag is plain `$VERSION` (per-cluster registries don't race)
- `jx promote` opens an auto-PR against each cluster's gitops repo
- Cosign signs the image against the verifier-trusted key

`CLUSTER_ID` comes from the cluster-wide `jx-cluster-config` ConfigMap.

## References

- `~/leartech/hub/shared-rules/golden-service-standard.md` — the architecture decisions this service satisfies
- `~/leartech/hub/shared-rules/conventions.md` — CI/pipeline rules
- `~/leartech/leartech-go-common` — auth middleware, logger, httptools
- `~/leartech/leartech-helm-library` — chart spine (labels, securityContext, probes)
- `project_new_product_layer_design` — eventual tool shape
- `project_track_b_sequence` — bootstrap → tools → registration sequence
