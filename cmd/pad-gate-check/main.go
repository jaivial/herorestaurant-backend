// Command pad-gate-check is a MANUAL read-only diagnostic (repair-padgate1):
// it runs the exact PG readiness gates (business native app.norm key indexes,
// MySQL-origin bot table ownership) against an existing database. It is not a test,
// fixture or suite. It never starts the app, imports, migrates, writes markers
// or calls providers: sessions are opened with default_transaction_read_only=on
// and the command refuses to continue unless PG confirms transaction_read_only.
// DSNs come only from the environment and are never printed.
//
//	PADQA_BUSINESS_DSN=... [PADQA_BOT_DSN=...] pad-gate-check [-business-schema public] [-bot-schema bot_context]
//	PADQA_BUSINESS_DSN=... pad-gate-check -read-qa -rid N -date YYYY-MM-DD [-domain host]
//	   (repair-semantic1: runs selected REAL ported read functions + Go Scan in a
//	   READ ONLY tx; aggregate counts only; not HTTP/concurrency/write QA)
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/botstore"
	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/sqldb"
)

type result struct {
	Gate  string `json:"gate"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func main() {
	businessSchema := flag.String("business-schema", sqldb.SchemaBusiness, "business schema")
	botSchema := flag.String("bot-schema", sqldb.SchemaBotContext, "bot store schema")
	readQA := flag.Bool("read-qa", false, "manual read-only ported query QA (needs -rid, -date)")
	rid := flag.Int("rid", 0, "operator-chosen restaurant id for -read-qa")
	date := flag.String("date", "", "YYYY-MM-DD for -read-qa")
	domain := flag.String("domain", "", "optional domain for the ownership lookup")
	flag.Parse()
	bdsn := os.Getenv("PADQA_BUSINESS_DSN")
	if bdsn == "" {
		fail("PADQA_BUSINESS_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *readQA {
		if *rid <= 0 {
			fail("-read-qa needs -rid")
		}
		db := open(ctx, bdsn, *businessSchema)
		defer db.Close()
		res := api.RunManualReadQA(ctx, db, *rid, *date, *domain)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "read-qa", "read_only": true, "scope": "read query + Go Scan only; not HTTP/auth/concurrency/writes", "results": res})
		return
	}
	var out []result
	ok := true
	run := func(name string, f func() error) {
		err := f()
		r := result{Gate: name, OK: err == nil}
		if err != nil {
			r.Error, ok = err.Error(), false
		}
		out = append(out, r)
	}
	bdb := open(ctx, bdsn, *businessSchema)
	defer bdb.Close()
	run("business.native_keys", func() error { return appdb.RequireNativeKeys(ctx, bdb) })
	run("business.mysql_origin_bot_tables", func() error { return tableCount(ctx, bdb, len(botstore.MySQLTables)) })
	run("business.onupdate_triggers", func() error { return appdb.RequireOnUpdateTriggers(ctx, bdb) })
	if dsn := os.Getenv("PADQA_BOT_DSN"); dsn != "" {
		botdb := open(ctx, dsn, *botSchema)
		defer botdb.Close()
		// repair-wiring2: the bot context schema owns only SQLite-origin tables;
		// MySQL-origin bot tables there would be empty duplicates (never used).
		run("bot.no_mysql_origin_tables", func() error { return tableCount(ctx, botdb, 0) })
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": "pad-gate-check", "read_only": true, "ok": ok, "results": out})
	if !ok {
		os.Exit(1)
	}
}

func open(ctx context.Context, dsn, schema string) *sql.DB {
	db, err := sqldb.OpenPostgresReadOnly(dsn, schema)
	if err != nil {
		fail("open: " + err.Error())
	}
	db.SetMaxOpenConns(1)
	var ro string
	if err := db.QueryRowContext(ctx, pgOnlySQL("SHOW transaction_read_only")).Scan(&ro); err != nil || ro != "on" {
		fail(fmt.Sprintf("refused: session is not read-only (transaction_read_only=%q)", ro))
	}
	return db
}

// tableCount requires exactly want of the MySQL-origin bot tables
// (botstore.MySQLTables) in the pool's current_schema(). Read-only.
func tableCount(ctx context.Context, db *sql.DB, want int) error {
	names := make([]string, 0, len(botstore.MySQLTables))
	for _, t := range botstore.MySQLTables {
		names = append(names, t.Name)
	}
	var n int
	if err := db.QueryRowContext(ctx, pgOnlySQL("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ANY($1)"), names).Scan(&n); err != nil {
		return err
	}
	if n != want {
		return fmt.Errorf("found %d of %d MySQL-origin bot tables in current_schema(), want %d", n, len(names), want)
	}
	return nil
}

// pgOnlySQL marks PostgreSQL-only statement text (inventory marker).
func pgOnlySQL(q string) string { return q }

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "pad-gate-check:", msg)
	os.Exit(2)
}
