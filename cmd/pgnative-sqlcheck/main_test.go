package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

const branchSrc = "package x\n" +
	"func f(st struct{ Postgres bool }, d int) {\n" +
	"\tq := `INSERT INTO t (a, b) VALUES (?, ?) ON DUPLICATE KEY UPDATE b = b`\n" +
	"\tif st.Postgres {\n" +
	"\t\tq = `INSERT INTO t (a, b) VALUES (?, ?) ON CONFLICT (a, b) DO NOTHING`\n" +
	"\t} else {\n" +
	"\t\tselectedSQL(`SELECT x FROM fts WHERE fts MATCH ?`)\n" +
	"\t}\n" +
	"\tif d != Postgres {\n" +
	"\t\tselectedSQL(`SELECT 1 FROM mysql_only`)\n" +
	"\t} else {\n" +
	"\t\tselectedSQL(`SELECT a FROM t WHERE a = ? RETURNING a`)\n" +
	"\t}\n" +
	"\tselectedSQL(q)\n" +
	"\tselectedSQL(\"SELECT last_value FROM \" + q)\n" +
	"}\n"

func TestCollectFindsDialectBranchStrings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "internal", "x", "x.go"), branchSrc)
	sites := collect(root)
	got := map[string]string{}
	for _, s := range sites {
		got[s.SQL] = s.Helper + "|" + s.Skip
	}
	pg := "INSERT INTO t (a, b) VALUES (?, ?) ON CONFLICT (a, b) DO NOTHING"
	if got[pg] != "pgbranch|" {
		t.Fatalf("PG branch statement not collected as preparable site: %q (sites %+v)", got[pg], sites)
	}
	if got["SELECT a FROM t WHERE a = ? RETURNING a"] != "pgbranch|" {
		t.Fatalf("else arm of `!= Postgres` not collected: %+v", sites)
	}
	for _, notPG := range []string{"SELECT x FROM fts WHERE fts MATCH ?", "SELECT 1 FROM mysql_only", "SELECT last_value FROM ", "INSERT INTO t (a, b) VALUES (?, ?) ON DUPLICATE KEY UPDATE b = b"} {
		if v, ok := got[notPG]; ok && strings.HasSuffix(v, "|") {
			t.Fatalf("non-PG string %q collected as preparable: %q", notPG, v)
		}
	}
}

func TestLintFlagsConflictTargetNotMatchingPlanExpressionIndex(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.sql")
	writeFile(t, plan, "CREATE TABLE \"bot_inbound_queue\" (\n  \"id\" bigint,\n  CONSTRAINT \"pk__bot_inbound_queue\" PRIMARY KEY (\"id\")\n);\n"+
		"CREATE UNIQUE INDEX \"ux__bot_inbound_queue__uq_inbound_msg\" ON \"bot_inbound_queue\" (\"restaurant_id\", app.norm_ascii(\"message_id\"));\n")
	raw := loadPlanKeys(plan, nil)
	bad := site{Pos: "a.go:1", SQL: "INSERT INTO bot_inbound_queue (restaurant_id, message_id) VALUES (?, ?) ON CONFLICT (restaurant_id, message_id) DO NOTHING"}
	if f := lint(bad, nil, raw); len(f) != 1 || f[0].Kind != "b_on_conflict" {
		t.Fatalf("raw target must be flagged, got %+v", f)
	}
	good := bad
	good.SQL = strings.Replace(bad.SQL, "(restaurant_id, message_id) DO", "(restaurant_id, app.norm_ascii(message_id::text)) DO", 1)
	if f := lint(good, nil, raw); len(f) != 0 {
		t.Fatalf("expression target must match the index, got %+v", f)
	}
	pk := site{Pos: "a.go:2", SQL: "INSERT INTO bot_inbound_queue (id) VALUES (?) ON CONFLICT (id) DO NOTHING"}
	if f := lint(pk, nil, raw); len(f) != 0 {
		t.Fatalf("PK target must match, got %+v", f)
	}
}

func TestPsqlScriptUsesGenericPlanAndParsesSQLSTATE(t *testing.T) {
	sites := []site{
		{Pos: "a.go:1", SQL: "INSERT INTO t (a) VALUES (?) ON CONFLICT (a) DO NOTHING"},
		{Pos: "a.go:2", SQL: "SELECT 1", Skip: "x"},
		{Pos: "a.go:3", SQL: "SELECT a FROM t WHERE a = ?"},
	}
	script := psqlScript(sites)
	if !strings.Contains(script, "EXPLAIN (GENERIC_PLAN, COSTS OFF) INSERT INTO t (a) VALUES ($1) ON CONFLICT (a) DO NOTHING;") {
		t.Fatalf("script must plan (not only PREPARE) each site:\n%s", script)
	}
	if !strings.Contains(script, "BEGIN READ ONLY;") || !strings.HasSuffix(script, "ROLLBACK;\n") || strings.Contains(script, "@@site 1\n") {
		t.Fatalf("script must be read-only, rolled back, and skip skipped sites:\n%s", script)
	}
	out := "@@site 0\npsql:<stdin>:7: ERROR:  42P10: there is no unique or exclusion constraint matching the ON CONFLICT specification\nLOCATION:  x\n" +
		"psql:<stdin>:8: ERROR:  25P02: current transaction is aborted\n@@site 3\n"
	res := parsePsqlResult(sites, out)
	if e := res["a.go:1|"+sites[0].SQL]; !strings.HasPrefix(e, "42P10:") {
		t.Fatalf("site 0 = %q, want 42P10", e)
	}
	if e := res["a.go:3|"+sites[2].SQL]; !strings.HasPrefix(e, "NOTRUN") {
		t.Fatalf("site 2 without marker = %q, want NOTRUN", e)
	}
}
