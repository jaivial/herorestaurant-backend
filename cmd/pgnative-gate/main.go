// Command pgnative-gate runs read-only catalog checks of the NATIVE PostgreSQL
// schema (coordination id: pgnative_gate_v1, pgnative-A). Only SELECTs, inside
// a READ ONLY transaction that is rolled back. DSN from PG_DSN (never printed).
//
//	PG_DSN=... pgnative-gate -schema schema.json
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	appdb "preactvillacarmen/internal/db"
	"preactvillacarmen/internal/dbmigrate"
)

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail any    `json:"detail,omitempty"`
}

func main() {
	schemaFile := flag.String("schema", "", "schema.json the native plan was built from")
	flag.Parse()
	dsn := os.Getenv("PG_DSN")
	if dsn == "" || *schemaFile == "" {
		fmt.Fprintln(os.Stderr, "need PG_DSN env and -schema")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*schemaFile)
	die(err)
	var s dbmigrate.Schema
	die(json.Unmarshal(raw, &s))
	plan := dbmigrate.BuildPlan(&s, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	die(err)
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	die(err)
	defer tx.Rollback()
	var out []check
	count := func(q string, args ...any) int64 {
		var n int64
		die(tx.QueryRowContext(ctx, q, args...).Scan(&n))
		return n
	}

	n := count(`SELECT count(*) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace ns ON ns.oid=c.relnamespace
		WHERE ns.nspname='public' AND a.attnum>0 AND NOT a.attisdropped AND (a.attname LIKE '%__wk' OR a.attname LIKE '%__pad')`)
	out = append(out, check{"no_wk_pad_columns", n == 0, n})
	n = count(`SELECT count(*) FROM pg_attribute a JOIN pg_collation co ON co.oid=a.attcollation JOIN pg_namespace cn ON cn.oid=co.collnamespace
		WHERE cn.nspname='dbmigrate' AND a.attnum>0 AND NOT a.attisdropped`) +
		count(`SELECT count(*) FROM pg_index x JOIN pg_class c ON c.oid=x.indexrelid JOIN pg_namespace ns ON ns.oid=c.relnamespace
		WHERE ns.nspname='public' AND (pg_get_indexdef(x.indexrelid) LIKE '%dbmigrate.%' OR c.relname LIKE 'ixwk__%' OR c.relname LIKE 'ixpad__%')`) +
		count(`SELECT count(*) FROM pg_collation co JOIN pg_namespace cn ON cn.oid=co.collnamespace WHERE cn.nspname='dbmigrate'`)
	out = append(out, check{"no_dbmigrate_collation_or_wk_pad_index", n == 0, n})

	// pgnative-B: shared check (exists, unique, valid, ready, non-partial,
	// table, key list compared after removing quotes, parentheses, ::casts).
	var missing []string
	uniq := dbmigrate.NativeUniques(&s)
	for _, u := range uniq {
		ix := appdb.NativeKeyIndex{Table: u.Table, Index: u.PGName, Expr: strings.Join(u.Exprs, ", ")}
		if why := appdb.CheckNativeKeyIndex(ctx, tx, ix); why != "" {
			missing = append(missing, u.PGName+": "+why)
		}
	}
	out = append(out, check{"unique_expression_indexes", len(missing) == 0, map[string]any{"expected": len(uniq), "missing_or_invalid": missing}})

	rows, err := tx.QueryContext(ctx, `SELECT conname FROM pg_constraint WHERE contype='f' AND NOT convalidated AND connamespace='public'::regnamespace ORDER BY 1`)
	die(err)
	notValid := map[string]bool{}
	for rows.Next() {
		var c string
		die(rows.Scan(&c))
		notValid[c] = true
	}
	rows.Close()
	var orphans int64
	per := map[string]int64{}
	for _, f := range plan.ForeignKeys {
		if notValid[f.PGName] {
			k := count(f.OrphanCheck)
			per[f.PGName] = k
			orphans += k
		}
	}
	fkTotal := count(`SELECT count(*) FROM pg_constraint WHERE contype='f' AND connamespace='public'::regnamespace`)
	out = append(out, check{"foreign_keys_report", fkTotal == int64(len(plan.ForeignKeys)), map[string]any{"planned": len(plan.ForeignKeys), "present": fkTotal, "not_valid": len(notValid), "orphans_total": orphans, "orphans_expected": 731, "orphans_per_fk": per}})

	trg := count(`SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace ns ON ns.oid=c.relnamespace WHERE ns.nspname='public' AND NOT t.tgisinternal`)
	out = append(out, check{"triggers", trg == int64(len(plan.Triggers)), map[string]any{"present": trg, "planned": len(plan.Triggers)}})

	imm := count(`SELECT count(*) FROM pg_proc p JOIN pg_namespace ns ON ns.oid=p.pronamespace WHERE ns.nspname='app' AND p.proname IN ('norm','norm_ascii') AND p.provolatile='i'`)
	out = append(out, check{"app_norm_immutable", imm == 2, imm})

	all := true
	for _, c := range out {
		all = all && c.OK
	}
	b, _ := json.MarshalIndent(map[string]any{"pgnative_gate": all, "checks": out}, "", " ")
	fmt.Println(string(b))
	if !all {
		os.Exit(1)
	}
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgnative-gate:", err)
		os.Exit(2)
	}
}
