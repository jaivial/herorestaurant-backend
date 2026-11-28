package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"preactvillacarmen/internal/dbmigrate"
)

// Coordination id: temporal_onupdate_gate_v1 (repair-enums2)
//
// RequireOnUpdateTriggers validates, read-only, that the PG business DB runs
// exactly the planner's ON UPDATE contract: dbmigrate.on_update_ts and
// dbmigrate.on_update_mark exist with the planner's exact body (sha256 of
// pg_proc.prosrc vs the planner source text), and each of the 136 tables has
// exactly the expected 273 BEFORE UPDATE FOR EACH ROW triggers (name, function,
// args, UPDATE OF column) and NO other non-internal BEFORE UPDATE row trigger
// (a foreign one returning NULL between mark and refresh would leak a mark).
// The R5 copy (old 2-part args) fails this gate until the reviewed forward
// migration is applied. Nothing is created.

type onUpdateTrigger struct {
	Table, Name, Column, Function string
	Args                          []string
}

func RequireOnUpdateTriggers(ctx context.Context, db *sql.DB) error {
	fail := func(err error) error { return fmt.Errorf("postgres business DB refused: ON UPDATE triggers: %w", err) }
	for name, src := range dbmigrate.OnUpdateFunctionSources() {
		var prosrc string
		var nargs int
		var rettrigger, plpgsql bool
		err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT p.prosrc, p.pronargs, p.prorettype = 'trigger'::regtype, l.lanname = 'plpgsql'
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
			WHERE n.nspname = 'dbmigrate' AND p.proname = $1`), name).Scan(&prosrc, &nargs, &rettrigger, &plpgsql)
		if err != nil {
			return fail(fmt.Errorf("function dbmigrate.%s: %w", name, err))
		}
		want, got := sha256.Sum256([]byte(src)), sha256.Sum256([]byte(prosrc))
		if nargs != 0 || !rettrigger || !plpgsql || want != got {
			return fail(fmt.Errorf("function dbmigrate.%s differs from planner source (sha256 want %s got %s)", name, hex.EncodeToString(want[:8]), hex.EncodeToString(got[:8])))
		}
	}
	perTable := map[string]int{}
	for _, t := range requiredOnUpdateTriggers {
		perTable[t.Table]++
		var tgtype int
		var args []byte
		var attrs, fn string
		err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT t.tgtype::int, t.tgargs, t.tgattr::text, n2.nspname || '.' || p.proname
			FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_proc p ON p.oid = t.tgfoid JOIN pg_namespace n2 ON n2.oid = p.pronamespace
			WHERE n.nspname = current_schema() AND c.relname = $1 AND t.tgname = $2 AND NOT t.tgisinternal AND t.tgenabled <> 'D'`),
			t.Table, t.Name).Scan(&tgtype, &args, &attrs, &fn)
		if err != nil {
			return fail(fmt.Errorf("trigger %s on %s: %w", t.Name, t.Table, err))
		}
		// tgtype bits: ROW=1, BEFORE=2, UPDATE=16; nothing else.
		if tgtype != 19 || fn != "dbmigrate."+t.Function {
			return fail(fmt.Errorf("trigger %s on %s: type %d / function %s differ", t.Name, t.Table, tgtype, fn))
		}
		want := []byte{}
		for _, a := range t.Args {
			want = append(append(want, a...), 0)
		}
		if !bytes.Equal(args, want) {
			return fail(fmt.Errorf("trigger %s on %s: args differ", t.Name, t.Table))
		}
		var wantAttrs string
		if t.Column != "" {
			if err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT a.attnum::text FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname = current_schema() AND c.relname = $1 AND a.attname = $2 AND a.attnum > 0 AND NOT a.attisdropped`), t.Table, t.Column).Scan(&wantAttrs); err != nil {
				return fail(fmt.Errorf("trigger %s: column %s: %w", t.Name, t.Column, err))
			}
		}
		if attrs != wantAttrs {
			return fail(fmt.Errorf("trigger %s on %s: UPDATE OF columns %q, want %q", t.Name, t.Table, attrs, wantAttrs))
		}
	}
	for table, want := range perTable {
		var n int
		if err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT COUNT(*) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relname = $1 AND NOT t.tgisinternal AND (t.tgtype::int & 19) = 19`), table).Scan(&n); err != nil {
			return fail(err)
		}
		if n != want {
			return fail(fmt.Errorf("%s has %d BEFORE UPDATE row triggers, want exactly %d (no foreign trigger between mark and refresh)", table, n, want))
		}
	}
	return nil
}
