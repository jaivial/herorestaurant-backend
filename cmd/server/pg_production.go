package main

// Coordination id: pg_production_start_v1 (pgnative-K). PRODUCTION start of
// the server on PostgreSQL. Separate from the local QA mode (pg_local_qa.go),
// which is unchanged: QA keeps loopback, no workers, no providers and its
// scratch waivers; this path keeps the normal address, background workers and
// providers exactly as the MySQL mode, and honours NO QA waiver.
//
// Required (fatal otherwise):
//   DB_DRIVER=postgres
//   DB_PG_PRODUCTION_APPROVED=1
//   DATABASE_URL                       (TCP or unix socket; sslmode as given)
//   BOT_CONTEXT_DRIVER=postgres, BOT_CONTEXT_POSTGRES_DSN, BOT_CONTEXT_POSTGRES_SCHEMA
//   VC_PG_LOCAL_HTTP_QA and VC_PG_LOCAL_HTTP_QA_SCRATCH_WAIVE unset
// Optional: DB_PG_ACCEPT_IMPORT_STAGING="orphans=N,pending=M[,fks_not_valid=K]"
// (exact match against the import marker; default: marker must be ready).
// MySQL migrations are NOT run on PG: the schema comes from the import plan.

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"preactvillacarmen/internal/analytics"
	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/config"
	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/sqldb"
)

func pgProductionGuards(cfg config.Config) []string {
	var bad []string
	if os.Getenv(appdb.PGProductionApprovedEnv) != "1" {
		bad = append(bad, appdb.PGProductionApprovedEnv+"=1 not set")
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		bad = append(bad, "DATABASE_URL empty")
	}
	if cfg.BotContextDriver != "postgres" {
		bad = append(bad, "BOT_CONTEXT_DRIVER must be postgres (no SQLite bot store with a PG business DB)")
	}
	if strings.TrimSpace(cfg.BotContextPostgresDSN) == "" {
		bad = append(bad, "BOT_CONTEXT_POSTGRES_DSN empty")
	}
	if strings.TrimSpace(cfg.BotContextPostgresSchema) == "" {
		bad = append(bad, "BOT_CONTEXT_POSTGRES_SCHEMA empty (expected bot_context)")
	}
	for _, k := range []string{pgLocalQAEnv, pgLocalQAWaiveEnv} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			bad = append(bad, k+" must be unset in production mode (QA waivers are never honoured here)")
		}
	}
	return bad
}

func runPGProduction(cfg config.Config, dialect sqldb.Dialect) {
	if bad := pgProductionGuards(cfg); len(bad) > 0 {
		log.Fatalf("[pg_production_start_v1] DB_DRIVER=postgres production start refused: %s", strings.Join(bad, "; "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := appdb.OpenBusinessForProduction(ctx, cfg)
	if err != nil {
		log.Fatalf("[pg_production_start_v1] business PG refused: %v", err)
	}
	defer db.Close()
	api.SetSQLDialect(dialect)
	analytics.SetSQLDialect(dialect)
	server := api.NewServer(db, cfg)
	log.Printf("[pg_production_start_v1] PRODUCTION API on %s (PostgreSQL, readiness gates passed; MySQL migrations not applied on PG)", cfg.Addr)
	log.Fatal(newHTTPServer(cfg.Addr, server.Routes()).ListenAndServe())
}
