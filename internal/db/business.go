package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/sqldb"
)

// Coordination id: db_dialect_selection_v1
//
// BusinessDialect maps DB_DRIVER to the business SQL dialect: "" / "mysql" ->
// MySQL (default, unchanged), "postgres" -> PostgreSQL, anything else is an
// error. There is no fallback between engines.
func BusinessDialect(driver string) (sqldb.Dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "mysql":
		return sqldb.MySQL, nil
	case "postgres":
		return sqldb.Postgres, nil
	}
	return "", fmt.Errorf("unknown DB_DRIVER %q", driver)
}

// pgToolsQAEnv must be "1" for a tool to start on PostgreSQL. It exists so a
// PG start is an explicit, reviewed decision; setting it does not prove the
// port: runtime QA does.
const pgToolsQAEnv = "DB_PG_TOOLS_QA_APPROVED"

// OpenBusinessForTool opens the business DB for an ops tool. MySQL: the
// existing OpenMySQL (unchanged). PostgreSQL: fails closed unless
// DB_PG_TOOLS_QA_APPROVED=1 and DATABASE_URL is set (no MySQL fallback); the
// pool pins search_path=public, TimeZone=UTC and timestamptz ScanLocation=UTC
// (sqldb.OpenPostgres); the import marker dbmigrate.marker('import_verified')
// written by the PG import must exist. Nothing is created or migrated: on PG
// the schema comes only from the import plan.
func OpenBusinessForTool(ctx context.Context, cfg config.Config) (*sql.DB, sqldb.Dialect, error) {
	return openBusiness(ctx, cfg, nil)
}

// Local QA waivers (pg_local_http_qa_v1, repair-v20). Only the server's
// VC_PG_LOCAL_HTTP_QA mode may pass them, only for a disposable scratch, and
// each one is logged loudly by the caller. They never change data: the 731
// historical FK orphans stay as they are (never deleted or invented).
const (
	WaiveImportMarker = "import-marker" // marker report not ready_for_cutover
	WaiveShadowFK     = "shadow-fk"     // pgnative-B: accepted no-op (no shadow FKs in the native schema; orphan FKs are NOT VALID)
)

// OpenBusinessForLocalQA is OpenBusinessForTool with an explicit waiver set.
// Unknown waiver names are refused. All other gates still apply.
func OpenBusinessForLocalQA(ctx context.Context, cfg config.Config, waivers []string) (*sql.DB, sqldb.Dialect, error) {
	w := map[string]bool{}
	for _, v := range waivers {
		switch v = strings.TrimSpace(v); v {
		case "":
		case WaiveImportMarker, WaiveShadowFK:
			w[v] = true
		default:
			return nil, "", fmt.Errorf("unknown local QA waiver %q", v)
		}
	}
	return openBusiness(ctx, cfg, w)
}

func openBusiness(ctx context.Context, cfg config.Config, waive map[string]bool) (*sql.DB, sqldb.Dialect, error) {
	return openBusinessGated(ctx, cfg, businessGate{approvalEnv: pgToolsQAEnv, waive: waive})
}

// Coordination id: pg_production_start_v1 (pgnative-K).
// PGProductionApprovedEnv must be "1" for the server's PRODUCTION start on
// PostgreSQL (cmd/server). It is a separate explicit decision from the tools
// approval and from the local QA mode.
const PGProductionApprovedEnv = "DB_PG_PRODUCTION_APPROVED"

// PGAcceptImportStagingEnv accepts a NOT-ready import marker only for exactly
// the known staging conditions, e.g. "orphans=731,pending=329,fks_not_valid=4".
// Every given count must equal the marker; orphans and pending are required.
// Unset (default): the marker must be ok + ready_for_cutover + 0 pending + 0
// NOT VALID FKs, exactly as before.
const PGAcceptImportStagingEnv = "DB_PG_ACCEPT_IMPORT_STAGING"

// StagingAcceptance is the parsed DB_PG_ACCEPT_IMPORT_STAGING value.
type StagingAcceptance struct {
	Orphans, Pending, FKsNotValid int64
	HasFKs                        bool
	Raw                           string
}

// ParseStagingAcceptance parses "orphans=N,pending=M[,fks_not_valid=K]".
func ParseStagingAcceptance(raw string) (*StagingAcceptance, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	a := &StagingAcceptance{Raw: raw, Orphans: -1, Pending: -1}
	for _, part := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if !ok || err != nil || n < 0 {
			return nil, fmt.Errorf("%s: bad item %q (want orphans=N,pending=M[,fks_not_valid=K])", PGAcceptImportStagingEnv, part)
		}
		switch strings.TrimSpace(k) {
		case "orphans":
			a.Orphans = n
		case "pending":
			a.Pending = n
		case "fks_not_valid":
			a.FKsNotValid, a.HasFKs = n, true
		default:
			return nil, fmt.Errorf("%s: unknown key %q", PGAcceptImportStagingEnv, k)
		}
	}
	if a.Orphans < 0 || a.Pending < 0 {
		return nil, fmt.Errorf("%s: orphans and pending are both required", PGAcceptImportStagingEnv)
	}
	return a, nil
}

// OpenBusinessForProduction opens the PG business DB for the production
// server: DB_PG_PRODUCTION_APPROVED=1 and DATABASE_URL are required, no local
// QA waiver is honoured, and every readiness gate runs. A not-ready marker is
// accepted only through an exact DB_PG_ACCEPT_IMPORT_STAGING match.
func OpenBusinessForProduction(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	accept, err := ParseStagingAcceptance(os.Getenv(PGAcceptImportStagingEnv))
	if err != nil {
		return nil, err
	}
	db, _, err := openBusinessGated(ctx, cfg, businessGate{approvalEnv: PGProductionApprovedEnv, accept: accept})
	return db, err
}

type businessGate struct {
	approvalEnv string
	waive       map[string]bool    // local QA only
	accept      *StagingAcceptance // production only
}

func openBusinessGated(ctx context.Context, cfg config.Config, g businessGate) (*sql.DB, sqldb.Dialect, error) {
	waive := g.waive
	d, err := BusinessDialect(cfg.DBDriver)
	if err != nil {
		return nil, "", err
	}
	if d == sqldb.MySQL {
		db, err := OpenMySQL(cfg.MySQL)
		return db, d, err
	}
	if os.Getenv(g.approvalEnv) != "1" {
		return nil, d, fmt.Errorf("DB_DRIVER=postgres refused: %s=1 not set (explicit approval required)", g.approvalEnv)
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return nil, d, fmt.Errorf("DB_DRIVER=postgres requires DATABASE_URL (no MySQL fallback)")
	}
	db, err := sqldb.OpenPostgres(cfg.DatabaseURL, sqldb.SchemaBusiness)
	if err != nil {
		return nil, d, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, d, fmt.Errorf("ping postgres: %w", err)
	}
	// The import writes dbmigrate.marker('import_verified') with its report
	// even when the import is NOT ready (internal/dbmigrate/pgimport.go): the
	// row's existence proves nothing. Require the recorded report to say
	// ok, ready_for_cutover and zero pending blocking decisions.
	var marker []byte
	if err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT value::text FROM dbmigrate.marker WHERE key = 'import_verified'`)).Scan(&marker); err != nil {
		_ = db.Close()
		return nil, d, fmt.Errorf("postgres business DB has no import marker (dbmigrate.marker import_verified): %w", err)
	}
	var rep struct {
		OK      bool     `json:"ok"`
		Ready   bool     `json:"ready_for_cutover"`
		Pending int      `json:"pending_blocking_decisions"`
		NotVal  []string `json:"fks_not_valid_staging"`
		Orphans int64    `json:"allowlisted_orphan_rows"`
	}
	err = json.Unmarshal(marker, &rep)
	ready := err == nil && rep.OK && rep.Ready && rep.Pending == 0 && len(rep.NotVal) == 0
	accepted := false
	if !ready && g.accept != nil && err == nil && rep.OK {
		a := g.accept
		accepted = int64(rep.Pending) == a.Pending && rep.Orphans == a.Orphans && (!a.HasFKs || int64(len(rep.NotVal)) == a.FKsNotValid)
		if !accepted {
			_ = db.Close()
			return nil, d, fmt.Errorf("%s=%q does not match the import marker exactly (marker: pending=%d orphans=%d fks_not_valid=%d)", PGAcceptImportStagingEnv, a.Raw, rep.Pending, rep.Orphans, len(rep.NotVal))
		}
		log.Printf("[pg_production_start_v1] import marker NOT ready_for_cutover, accepted via %s (pending=%d orphans=%d fks_not_valid=%d)", PGAcceptImportStagingEnv, rep.Pending, rep.Orphans, len(rep.NotVal))
	}
	if !ready && !accepted && !waive[WaiveImportMarker] {
		_ = db.Close()
		return nil, d, fmt.Errorf("postgres business DB import not ready for cutover (ok=%v ready=%v pending=%d fks_not_valid=%d orphans=%d err=%v)", rep.OK, rep.Ready, rep.Pending, len(rep.NotVal), rep.Orphans, err)
	}
	if err := RequireNativeKeys(ctx, db); err != nil { // pgnative-B: app.norm ux__ indexes
		_ = db.Close()
		return nil, d, err
	}
	if err := RequireOnUpdateTriggers(ctx, db); err != nil {
		_ = db.Close()
		return nil, d, err
	}
	return db, d, nil
}

// pgOnlySQL marks PostgreSQL-only text (identity; inventory marker).
func pgOnlySQL(q string) string { return q }
