package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"preactvillacarmen/internal/analytics"
	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
	"preactvillacarmen/internal/sqldb"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	// Coordination id: sqldb_native_pg_v1 / pg_production_start_v1 - MySQL
	// (default) unchanged. DB_DRIVER=postgres starts only with an explicit
	// approval: production (DB_PG_PRODUCTION_APPROVED=1, pg_production.go,
	// every readiness gate incl. RequireNativeKeys) or local QA
	// (VC_PG_LOCAL_HTTP_QA=1, pg_local_qa.go); otherwise it is refused.
	dialect, err := appdb.BusinessDialect(cfg.DBDriver)
	if err != nil {
		log.Fatalf("Invalid DB_DRIVER: %v", err)
	}
	if dialect != sqldb.MySQL {
		if os.Getenv(pgLocalQAEnv) == "1" {
			runPGLocalQA(cfg, dialect) // local QA mode, unchanged
			return
		}
		if os.Getenv(appdb.PGProductionApprovedEnv) == "1" {
			runPGProduction(cfg, dialect) // pg_production_start_v1
			return
		}
		log.Fatalf("DB_DRIVER=%q refused: set %s=1 (production, all gates) or %s=1 (local QA only)", cfg.DBDriver, appdb.PGProductionApprovedEnv, pgLocalQAEnv)
	}
	// db_dialect_selection_v1: one dialect for every SQL package.
	api.SetSQLDialect(dialect)
	analytics.SetSQLDialect(dialect)

	db, err := appdb.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// SKIP_MIGRATIONS=1 starts against an already-migrated database (used by
	// dev/test tooling such as the Forky e2e stub backend).
	if os.Getenv("SKIP_MIGRATIONS") != "1" {
		if err := migrations.Apply(ctx, db); err != nil {
			log.Fatalf("Failed to apply migrations: %v", err)
		}
	}

	server := api.NewServer(db, cfg)
	log.Printf("Hero Restaurant API starting on %s", cfg.Addr)
	log.Fatal(newHTTPServer(cfg.Addr, server.Routes()).ListenAndServe())
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// runPGLocalQA serves the API on PG for local manual QA only (pg_local_http_qa_v1).
// The business pool is opened through appdb.OpenBusinessForTool, so every
// readiness gate (import marker ready_for_cutover, native app.norm keys, ON
// UPDATE triggers) still applies unless a scratch waiver is logged.
func runPGLocalQA(cfg config.Config, dialect sqldb.Dialect) {
	// config.Addr is ":"+PORT (all interfaces): QA mode always binds loopback.
	if _, port, err := net.SplitHostPort(cfg.Addr); err == nil {
		cfg.Addr = net.JoinHostPort("127.0.0.1", port)
	}
	if err := localQAGuards(cfg); err != nil {
		log.Fatalf("%s refused: %v", pgLocalQAEnv, err)
	}
	disableOutboundHTTP()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	waivers, err := localQAWaivers(cfg)
	if err != nil {
		log.Fatalf("%s refused: %v", pgLocalQAEnv, err)
	}
	db, _, err := appdb.OpenBusinessForLocalQA(ctx, cfg, waivers)
	if err != nil {
		log.Fatalf("%s: business PG refused: %v", pgLocalQAEnv, err)
	}
	defer db.Close()
	api.SetSQLDialect(dialect)
	analytics.SetSQLDialect(dialect)
	server := api.NewServer(db, cfg)
	log.Printf("[%s] LOCAL QA ONLY: API on %s (PG, workers off, outbound HTTP refused)", pgLocalQAEnv, cfg.Addr)
	log.Fatal(newHTTPServer(cfg.Addr, server.Routes()).ListenAndServe())
}
