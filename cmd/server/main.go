// Package main is the leartech-catalog-mcp entrypoint.
//
// Catalog MCP — read-only inventory of leartech services for BA/Architect
// agents. Foundation for V6-P10 + the new-product layer.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/swaggest/swgui/v3cdn"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mikelear/leartech-catalog-mcp/internal/config"
	"github.com/mikelear/leartech-catalog-mcp/internal/db"
	"github.com/mikelear/leartech-catalog-mcp/internal/handlers"
	"github.com/mikelear/leartech-catalog-mcp/internal/middleware"
	"github.com/mikelear/leartech-catalog-mcp/internal/tracing"

	// Import generated Swagger docs so swgui can serve them.
	// Regenerate with `make swag`.
	_ "github.com/mikelear/leartech-catalog-mcp/docs"
)

// version is injected at build time via -ldflags "-X main.version=<version>".
var version = "dev"

// @title Catalog MCP API
// @version 0.0.1
// @description Read-only inventory of leartech services for BA/Architect agents. Foundation for V6-P10 + the new-product layer.
// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Bearer token (JWT) issued by the leartech auth service.
func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if err := run(); err != nil {
		log.Fatal().Err(err).Msg("server failed")
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info().
		Str("version", version).
		Str("clusterID", cfg.ClusterID).
		Str("port", cfg.Port).
		Msg("starting leartech-catalog-mcp")

	shutdownTracer, err := tracing.Init(ctx, "leartech-catalog-mcp", version, cfg.ClusterID)
	if err != nil {
		return fmt.Errorf("init tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			log.Warn().Err(err).Msg("tracer shutdown failed")
		}
	}()

	// Shell mode: if DATABASE_URL is empty the service runs without a DB —
	// `/health/ready`, `/docs`, `/openapi.json`, `/metrics` all still respond so
	// the template's own staging deploy can prove the chain end-to-end without
	// provisioning a DB. Real consumer services always set DATABASE_URL and the
	// DB path runs as normal.
	var pool *pgxpool.Pool
	if cfg.DatabaseURL != "" {
		p, err := db.NewPool(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connect db: %w", err)
		}
		pool = p
		defer pool.Close()
	} else {
		log.Warn().Msg("DATABASE_URL not set — running in shell mode (no database)")
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(otelgin.Middleware("leartech-catalog-mcp"))
	router.Use(middleware.RequestLogger())

	// Health endpoints — unauthenticated per golden-standard contract.
	healthHandler := handlers.NewHealthHandler(pool, version)
	healthHandler.RegisterRoutes(router)

	// Prometheus /metrics — scoped to internal scrape client via NetworkPolicy.
	handlers.RegisterMetrics(router)

	// OpenAPI spec + interactive docs. HEAD registered alongside GET so
	// `curl -sI /docs` and uptime checks return 200 — gin.New() doesn't
	// auto-dispatch HEAD. ABSOLUTE path to /docs/swagger.json: distroless
	// nonroot sets WORKDIR=/home/nonroot, so a relative "docs/swagger.json"
	// resolves to /home/nonroot/docs/swagger.json (missing) — Go stdlib
	// returns 403 for that path rather than 404. Absolute avoids the trap.
	openapi := func(c *gin.Context) { c.File("/docs/swagger.json") }
	router.GET("/openapi.json", openapi)
	router.HEAD("/openapi.json", openapi)
	docs := gin.WrapH(v3cdn.NewHandler(
		"Catalog MCP API",
		"/openapi.json",
		"/",
	))
	router.GET("/docs", docs)
	router.HEAD("/docs", docs)

	// All non-health, non-metrics, non-docs routes require bearer auth.
	//
	// C1 (auth hardening) fail-closed contract:
	//   - AuthRequired defaults to true; the pod refuses to boot unless
	//     issuer + audience + client creds are all wired (go-common v1.0.0
	//     validates in NewServiceClient — no noop / pass-through path).
	//   - Audience validation is enforced on every request against the
	//     configured cfg.Auth.Audience ("leartech-catalog-mcp" in prod).
	//   - AuthRequired=false is a deliberate local-dev / smoke opt-out
	//     — production charts always run with AuthRequired=true.
	authed := router.Group("/api/v1")
	if cfg.AuthRequired {
		bearer, err := middleware.BearerAuth(cfg.Auth)
		if err != nil {
			return fmt.Errorf("auth middleware (AUTH_REQUIRED=true): %w", err)
		}
		authed.Use(bearer)
		log.Info().
			Str("audience", cfg.Auth.Audience).
			Str("issuer", cfg.Auth.ServerURL).
			Msg("auth: bearer middleware enabled on /api/v1")
	} else {
		log.Warn().Msg("AUTH_REQUIRED=false — /api/v1 is UNAUTHENTICATED (local-dev only)")
	}

	exampleHandler := handlers.NewExampleHandler(pool)
	exampleHandler.RegisterRoutes(authed)

	// Template-only /api/v1/fleet-test — opt-in via FLEET_TEST_ENABLED
	// (chart value fleetTest.enabled, default false). Cloned services
	// remove this block along with internal/handlers/fleet_test.go and
	// the peer-SDK go.mod deps.
	if cfg.FleetTestEnabled {
		handlers.NewFleetTestHandler().RegisterRoutes(authed)
		log.Info().Msg("FLEET_TEST_ENABLED=true — /api/v1/fleet-test enabled")
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("server listen failed")
		}
	}()

	log.Info().Str("port", cfg.Port).Msg("server started")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server")
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server forced shutdown: %w", err)
	}

	log.Info().Msg("server stopped")
	return nil
}
