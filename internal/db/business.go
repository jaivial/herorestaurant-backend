package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
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
	d, err := BusinessDialect(cfg.DBDriver)
	if err != nil {
		return nil, "", err
	}
	if d == sqldb.MySQL {
		db, err := OpenMySQL(cfg.MySQL)
		return db, d, err
	}
	if os.Getenv(pgToolsQAEnv) != "1" {
		return nil, d, fmt.Errorf("DB_DRIVER=postgres refused for this tool: %s=1 not set (PG runtime not QA-approved)", pgToolsQAEnv)
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
	}
	if err := json.Unmarshal(marker, &rep); (err != nil || !rep.OK || !rep.Ready || rep.Pending != 0 || len(rep.NotVal) != 0) && !waive[WaiveImportMarker] {
		_ = db.Close()
		return nil, d, fmt.Errorf("postgres business DB import not ready for cutover (ok=%v ready=%v pending=%d fks_not_valid=%d err=%v)", rep.OK, rep.Ready, rep.Pending, len(rep.NotVal), err)
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
