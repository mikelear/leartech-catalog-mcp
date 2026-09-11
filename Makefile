.PHONY: test-coverage vuln pre-push all pre-push swag swag-check lint fetch-mk lint-check fmt vet tidy tidy-check \
        build test test-verbose test-coverage vuln secrets clean help diagnose

VERSION             ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
SWAG_VERSION        := v1.16.4
GOLANGCI_BASE_URL   := https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/.golangci.base.yml
GITLEAKS_VERSION       := v8.18.4
GOVULNCHECK_VERSION    := v1.1.4   # pinned to match CI (catalog tasks/govulncheck/pullrequest.yaml). v1.2.0+ requires go 1.25; bump alongside fleet-wide go.mod upgrade.
GOIMPORTS_VERSION      := latest
GOLANGCI_LINT_VERSION  := v2.11.4   # pinned to match CI (catalog tasks/go-lint/pullrequest.yaml uses image golangci/golangci-lint:v2.11.4)
GO                  := go
MODULE              := github.com/mikelear/leartech-catalog-mcp

all: fmt swag build test lint   ## Format, regenerate spec, build, test, lint

pre-push: fmt vet swag-check tidy-check build test lint vuln secrets   ## Tier-1 gates that MUST pass before pushing

swag:   ## Regenerate OpenAPI spec from annotations
	@command -v swag >/dev/null 2>&1 || $(GO) install github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION)
	@# Portable BSD/GNU sed: `-i.bak` + rm so macOS + Alpine both work.
	@sed -i.bak 's|^//	@version.*|//	@version		$(VERSION)|' cmd/server/main.go
	@rm -f cmd/server/main.go.bak
	swag init -g cmd/server/main.go -o docs

swag-check:   ## Verify docs/swagger.json is in sync with annotations (CI-mode — no writes)
	@cp docs/swagger.json docs/swagger.json.bak 2>/dev/null || true
	@$(MAKE) -s swag >/dev/null 2>&1
	@if ! diff -q docs/swagger.json docs/swagger.json.bak >/dev/null 2>&1; then \
		mv docs/swagger.json.bak docs/swagger.json 2>/dev/null || true; \
		echo "FAIL: docs/swagger.json is not in sync with annotations. Run 'make swag' and commit."; exit 1; \
	fi
	@rm -f docs/swagger.json.bak
	@echo "PASS: docs/swagger.json in sync"

# ── Golden Go lint: delegate to the pipeline catalog ───────────────────────
#
# Runs go/leartech-go.mk from leartech-pipeline-catalog — the SAME file CI curls
# in tasks/go-lint/pullrequest.yaml — so a laptop reproduces CI byte-for-byte
# rather than approximately. This target used to re-implement the fetch+merge
# locally; two implementations of one gate drift, and when they do the local one
# is the weaker.
LEARTECH_GO_MK_REF ?= main
LEARTECH_GO_MK_URL ?= https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/$(LEARTECH_GO_MK_REF)/go/leartech-go.mk
LEARTECH_GO_MK     := .leartech-go.mk

fetch-mk: $(LEARTECH_GO_MK)   ## Fetch the golden go/leartech-go.mk from pipeline-catalog

$(LEARTECH_GO_MK):
	@echo "==> fetching $(LEARTECH_GO_MK_URL)"
	@curl -fsSL -o $@ $(LEARTECH_GO_MK_URL)

# SHELL=/bin/bash: the golden mk uses bash-only syntax. CI images ship bash as
# /bin/sh so the drift is invisible there; a laptop /bin/sh needs the override.
lint: fetch-mk   ## golangci-lint via the merged config (delegates to golden leartech-go.mk::lint)
	$(MAKE) SHELL=/bin/bash -f $(LEARTECH_GO_MK) lint

# Mirror the value .lighthouse/jenkins-x/test.yaml injects, so local
# `make test-coverage` / `make pre-push` reproduce what CI ENFORCES rather than
# what the golden mk defaults to (60.0). Without it local is STRICTER than CI,
# and a gate that cries wolf gets ignored as fast as one that passes anything.
# Keep in sync with that file; drop the override once real coverage clears 60.
COVERAGE_THRESHOLD ?= 30.0

test-coverage: fetch-mk   ## Race + coverage with the floor CI enforces (delegates to golden leartech-go.mk)
	$(MAKE) SHELL=/bin/bash -f $(LEARTECH_GO_MK) test-coverage COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD)

vuln: fetch-mk   ## govulncheck (delegates to golden leartech-go.mk::vuln)
	$(MAKE) SHELL=/bin/bash -f $(LEARTECH_GO_MK) vuln

# pre-push is THE local entry point — what CI runs, in one command.
# `make lint` alone does NOT include govulncheck: that is a separate target,
# and running only lint is how a vulnerability finding reached a PR.
pre-push: fetch-mk   ## Full local gate: vet tidy-check build test-coverage lint vuln
	$(MAKE) SHELL=/bin/bash -f $(LEARTECH_GO_MK) pre-push COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD)

lint-check: lint   ## Alias of lint (idempotent — no auto-fix here)

fmt:   ## Format Go code (gofmt + goimports)
	$(GO) fmt ./...
	@command -v goimports >/dev/null || $(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)
	goimports -w -local $(MODULE) .

vet:   ## Run go vet
	$(GO) vet ./...

tidy:   ## Tidy and verify modules
	$(GO) mod tidy
	$(GO) mod verify

tidy-check:   ## Verify go.mod/go.sum are tidy (CI-mode — no writes)
	@cp go.mod go.mod.bak; cp go.sum go.sum.bak
	@$(GO) mod tidy
	@if ! diff -q go.mod go.mod.bak >/dev/null 2>&1 || ! diff -q go.sum go.sum.bak >/dev/null 2>&1; then \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; \
		echo "FAIL: go.mod/go.sum are not tidy. Run 'make tidy' and commit."; exit 1; \
	fi
	@rm -f go.mod.bak go.sum.bak
	@echo "PASS: go.mod/go.sum are tidy"

build: swag   ## Build the binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/server ./cmd/server

test:   ## Run unit tests
	$(GO) test ./... -v -count=1 -race

test-verbose:   ## Run tests with verbose output (alias of test for now)
	$(GO) test --tags=unit -v -failfast -count=1 ./...



secrets:   ## Scan for committed secrets (gitleaks)
	@command -v gitleaks >/dev/null || { \
		echo "Installing gitleaks $(GITLEAKS_VERSION)..."; \
		GOOS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
		GOARCH=$$(uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/'); \
		curl -sSfL "https://github.com/gitleaks/gitleaks/releases/download/$(GITLEAKS_VERSION)/gitleaks_$${GITLEAKS_VERSION#v}_$${GOOS}_$${GOARCH}.tar.gz" | \
			tar -xz -C /tmp gitleaks && mv /tmp/gitleaks $$($(GO) env GOPATH)/bin/gitleaks; \
	}
	gitleaks detect --source . --no-banner --redact

clean:   ## Clean build artifacts
	rm -rf bin/ cover.out .golangci.base.yml .golangci.merged.yml *.bak

diagnose:   ## Show which Tekton presubmit checks are covered locally vs need cluster
	@echo "Tekton presubmit checks for this repo (PR-time):"
	@echo "─────────────────────────────────────────────────"
	@for f in .lighthouse/jenkins-x/*.yaml .lighthouse/jenkins-x/*/*.yaml; do \
		[ -f "$$f" ] || continue; \
		case "$$f" in *triggers.yaml|*release.yaml|*pullrequest.yaml) continue;; esac; \
		name=$$(echo "$$f" | sed 's|.lighthouse/jenkins-x/||; s|.yaml$$||'); \
		case $$name in \
			lint) covered="✓ \033[32mmake lint\033[0m";; \
			test|test-coverage) covered="✓ \033[32mmake test\033[0m";; \
			govulncheck) covered="✓ \033[32mmake vuln\033[0m";; \
			end2end) covered="✗ \033[33mTier 3 — needs preview cluster\033[0m";; \
			end2end-ui) covered="✗ \033[33mTier 3 — Playwright needs preview cluster\033[0m";; \
			ai-review*) covered="✗ \033[33mTier 3 — LLM-against-deployed-preview\033[0m";; \
			security-scan/dynamic*) covered="✗ \033[33mTier 3 — DAST needs running app\033[0m";; \
			security-scan/image*) covered="✗ \033[33mTier 3 — needs built image\033[0m";; \
			security-scan*) covered="◐ \033[33mpartial — gitleaks via 'make secrets'; SAST needs cluster\033[0m";; \
			*) covered="? \033[31munknown — extend Makefile diagnose mapping\033[0m";; \
		esac; \
		printf "  %-30s %b\n" "$$name" "$$covered"; \
	done
	@echo ""
	@echo "Legend: ✓ covered by 'make pre-push'   ◐ partial   ✗ requires Tekton cluster   ? mapping needs update"
	@echo ""
	@echo "Run 'make pre-push' before pushing to catch all locally-covered failures."

help:   ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
