// Command dbmigrate is the S1 tooling of the full MySQL -> PostgreSQL
// migration (coordination id: dbmigrate_s1_v1). Implemented modes:
//
//	schema    -mysql DSN -db NAME -out FILE      information_schema -> schema JSON (no data)
//	export    -mysql DSN -db NAME -dir DIR       consistent snapshot -> raw NDJSON + row hashes + manifest (0600)
//	plan      -schema FILE -out DIR              PG DDL (plan.sql, plan.json) + coverage.json; exit 2 on unsupported
//	inventory -root DIR -out FILE                go/ast SQL call-site inventory
//	crosscheck -dir EXPORT -independent FILE     export digests vs independent text-bytes reference
//	import    -dir EXPORT -plan DIR -independent FILE   EMPTY PG db, one transaction (PG_DSN, unix socket)
//	verify    -dir EXPORT -plan DIR              read-only re-check of an imported PG db
//	export-pg -plan DIR -out NEWDIR              PG -> canonical NDJSON + manifest (refuses existing dir)
//	compare   -a MANIFEST -b MANIFEST            per-table rows/digest equality
//	pgcompat  -dir EXPORT -plan DIR              NUL / invalid UTF-8 / json \u0000 vs planned storage (counts only)
//
// The MySQL DSN must use the unix socket, e.g. root@unix(/var/run/mysqld/mysqld.sock)/
// (read it from MYSQL_DSN to keep it out of argv). Nothing prints row values.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"preactvillacarmen/internal/dbmigrate"
)

func main() {
	mode := flag.String("mode", "", "schema | export | plan | inventory")
	dbName := flag.String("db", "villacarmen_source", "source database")
	out0 := flag.String("out", "", "output file or dir")
	dir := flag.String("dir", "", "export dir")
	schemaFile := flag.String("schema", "", "schema.json (plan)")
	root := flag.String("root", ".", "repo root (inventory)")
	viewOverride := flag.String("view-override", "", "reviewed view override JSON (plan)")
	storageOverride := flag.String("storage-override", "", "reviewed storage override JSON (plan)")
	unsignedFull := flag.Bool("unsigned-full-domain", false, "plan: bigint unsigned -> numeric(20,0) everywhere, AUTO_INCREMENT -> owned sequence default (future plan; R5 unaffected)")
	orphanAllow := flag.String("orphan-allowlist", "", "STAGING-ONLY exact FK orphan allowlist JSON (import)")
	planDir := flag.String("plan", "", "plan dir with plan.json + schema.json copy")
	independent := flag.String("independent", "", "independent text-bytes reference NDJSON")
	ma := flag.String("a", "", "manifest A")
	mb := flag.String("b", "", "manifest B")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	switch *mode {
	case "schema":
		db := openMySQL(*dbName)
		s, err := dbmigrate.ExtractSchema(ctx, db, *dbName)
		check(err)
		writeJSON(need(*out0, "-out"), s)
		fmt.Printf("{\"mode\":\"schema\",\"tables\":%d,\"views\":%d,\"collations\":%d}\n", len(s.Tables), len(s.Views), len(s.Collations))
		if s.CollationMetaError != "" {
			// Fail closed: PAD attribute is a blocker, never guessed.
			fmt.Fprintln(os.Stderr, "blocker: collation metadata unavailable:", s.CollationMetaError)
			os.Exit(3)
		}
	case "padplan":
		// Source-only proposal from an existing schema.json; never executed, never merged into plan.sql.
		raw, err := os.ReadFile(need(*schemaFile, "-schema"))
		check(err)
		var s dbmigrate.Schema
		check(json.Unmarshal(raw, &s))
		props := dbmigrate.PadPlan(&s)
		writeJSON(need(*out0, "-out"), map[string]any{"applied": false, "collation_metadata_source": s.CollationMetaSource, "collations": len(s.Collations), "proposals": props})
		fmt.Printf("{\"mode\":\"padplan\",\"proposals\":%d,\"collation_metadata\":%t}\n", len(props), len(s.Collations) > 0)
	case "native-inventory", "native-convert":
		// pgnative-A: source-only artifacts from schema.json (no DB access)
		var s dbmigrate.Schema
		check(readJSON(need(*schemaFile, "-schema"), &s))
		out := dbmigrate.NativeInventoryTSV(&s)
		if *mode == "native-convert" {
			out = dbmigrate.ConvertScratchSQL(dbmigrate.BuildPlan(&s, nil, nil), &s)
		}
		check(os.WriteFile(need(*out0, "-out"), []byte(out), 0o644))
		fmt.Printf("{\"mode\":%q,\"native_uniques\":%d}\n", *mode, len(dbmigrate.NativeUniques(&s)))
	case "export":
		db := openMySQL(*dbName)
		m, err := dbmigrate.ExportMySQL(ctx, db, *dbName, need(*dir, "-dir"))
		check(err)
		summary(m)
		if !m.OK {
			os.Exit(2)
		}
	case "plan":
		raw, err := os.ReadFile(need(*schemaFile, "-schema"))
		check(err)
		var s dbmigrate.Schema
		check(json.Unmarshal(raw, &s))
		var views []dbmigrate.ViewOverride
		if *viewOverride != "" {
			vr, err := os.ReadFile(*viewOverride)
			check(err)
			check(json.Unmarshal(vr, &views))
		}
		var storage []dbmigrate.StorageOverride
		if *storageOverride != "" {
			sr, err := os.ReadFile(*storageOverride)
			check(err)
			check(json.Unmarshal(sr, &storage))
		}
		p := dbmigrate.BuildPlanOpts(&s, views, storage, dbmigrate.PlanOptions{UnsignedFullDomain: *unsignedFull})
		o := need(*out0, "-out")
		check(os.MkdirAll(o, 0o700))
		check(os.WriteFile(filepath.Join(o, "schema.json"), raw, 0o600))
		check(os.WriteFile(filepath.Join(o, "plan.sql"), []byte(p.SQL()), 0o600))
		writeJSON(filepath.Join(o, "plan.json"), p)
		writeJSON(filepath.Join(o, "coverage.json"), p.Coverage)
		c := p.Coverage
		fmt.Printf("{\"mode\":\"plan\",\"ready_for_cutover\":%t,\"pending\":%d,\"tables\":%d,\"columns\":%d,\"indexes\":%d,\"unique\":%d,\"fulltext\":%d,\"fks\":%d,\"checks\":%d,\"generated\":%d,\"on_update\":%d,\"enums\":%d,\"auto_increment\":%d,\"views\":%d,\"unsupported\":%d,\"noted_columns\":%d}\n",
			p.Ready, len(c.Pending), c.Tables, c.Columns, c.Indexes, c.Unique, c.Fulltext, c.FKs, c.Checks, c.Generated, c.OnUpdate, c.Enums, c.AutoInc, len(c.Views), len(c.Unsupported), len(c.Notes))
		if len(c.Unsupported) > 0 {
			os.Exit(2)
		}
	case "inventory":
		var mixed []string
		if *schemaFile != "" { // portable audit: schema identifiers with upper case
			var s dbmigrate.Schema
			check(readJSON(*schemaFile, &s))
			seen := map[string]bool{}
			for _, t := range s.Tables {
				for _, n := range append([]string{t.Name}, colNamesOf(t)...) {
					if strings.ToLower(n) != n && !seen[n] {
						seen[n] = true
						mixed = append(mixed, n)
					}
				}
			}
		}
		inv, err := dbmigrate.SQLInventory(*root, mixed...)
		check(err)
		writeJSON(need(*out0, "-out"), inv)
		fmt.Printf("{\"mode\":\"inventory\",\"files\":%d,\"sites\":%d,\"static\":%d,\"dynamic\":%d,\"native\":%d,\"portable_audit_failures\":%d}\n", inv.Files, inv.Sites, inv.Static, inv.Dynamic, inv.Native, inv.AuditFail)
	case "crosscheck":
		var mf dbmigrate.Manifest
		var s dbmigrate.Schema
		check(readJSON(filepath.Join(need(*dir, "-dir"), "manifest.json"), &mf))
		check(readJSON(filepath.Join(*dir, "schema.json"), &s))
		bad, err := dbmigrate.CrossCheck(&mf, &s, need(*independent, "-independent"))
		check(err)
		emit(map[string]any{"mode": "crosscheck", "ok": len(bad) == 0, "tables": len(mf.Tables), "rows": mf.TotalRows, "mismatch": bad})
		if len(bad) > 0 {
			os.Exit(2)
		}
	case "import", "verify":
		var p dbmigrate.Plan
		var s dbmigrate.Schema
		check(readJSON(filepath.Join(need(*planDir, "-plan"), "plan.json"), &p))
		check(readJSON(filepath.Join(*planDir, "schema.json"), &s))
		db := openPG()
		var rep *dbmigrate.ImportReport
		var err error
		if *mode == "import" {
			var allow *dbmigrate.OrphanAllowlist
			if *orphanAllow != "" {
				allow = &dbmigrate.OrphanAllowlist{}
				check(readJSON(*orphanAllow, allow))
			}
			rep, err = dbmigrate.Import(ctx, db, need(*dir, "-dir"), need(*independent, "-independent"), &p, &s, allow)
		} else {
			rep, err = dbmigrate.Verify(ctx, db, need(*dir, "-dir"), &s, p.Storage)
		}
		if err != nil {
			rep.Error = err.Error()
		}
		emit(rep)
		if err != nil {
			os.Exit(2)
		}
	case "pgcompat":
		var s dbmigrate.Schema
		var p dbmigrate.Plan
		check(readJSON(filepath.Join(need(*planDir, "-plan"), "schema.json"), &s))
		check(readJSON(filepath.Join(*planDir, "plan.json"), &p))
		bad, err := dbmigrate.PGCompat(need(*dir, "-dir"), &s, p.Storage)
		check(err)
		emit(map[string]any{"mode": "pgcompat", "ok": len(bad) == 0, "incompatible": bad})
		if len(bad) > 0 {
			os.Exit(2)
		}
	case "export-pg":
		var s dbmigrate.Schema
		var p dbmigrate.Plan
		check(readJSON(filepath.Join(need(*planDir, "-plan"), "schema.json"), &s))
		check(readJSON(filepath.Join(*planDir, "plan.json"), &p))
		mf, err := dbmigrate.ExportPG(ctx, openPG(), need(*out0, "-out"), &s, p.Storage)
		check(err)
		emit(map[string]any{"mode": "export-pg", "tables": len(mf.Tables), "rows": mf.TotalRows})
	case "compare":
		var a, b dbmigrate.Manifest
		check(readJSON(need(*ma, "-a"), &a))
		check(readJSON(need(*mb, "-b"), &b))
		d := dbmigrate.CompareManifests(&a, &b)
		emit(map[string]any{"mode": "compare", "ok": len(d) == 0, "differences": d})
		if len(d) > 0 {
			os.Exit(2)
		}
	default:
		check(fmt.Errorf("unknown -mode %q", *mode))
	}
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// openMySQL selects -db safely: set it when the DSN has none, refuse a DSN
// naming another database, and confirm with SELECT DATABASE().
func openMySQL(dbName string) *sql.DB {
	raw := os.Getenv("MYSQL_DSN")
	if raw == "" {
		check(fmt.Errorf("need MYSQL_DSN (unix socket)"))
	}
	if !dbNameRe.MatchString(dbName) {
		check(fmt.Errorf("invalid -db"))
	}
	cfg, err := mysql.ParseDSN(raw)
	check(err)
	if cfg.Net != "unix" {
		check(fmt.Errorf("MYSQL_DSN must use the unix socket"))
	}
	if cfg.DBName == "" {
		cfg.DBName = dbName
	} else if cfg.DBName != dbName {
		check(fmt.Errorf("MYSQL_DSN database differs from -db"))
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	check(err)
	db.SetMaxOpenConns(2)
	check(db.Ping())
	var cur string
	check(db.QueryRow(mysqlOnlySQL("SELECT DATABASE()")).Scan(&cur))
	if cur != dbName {
		check(fmt.Errorf("connected database differs from -db"))
	}
	return db
}

func colNamesOf(t dbmigrate.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

func openPG() *sql.DB {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		check(fmt.Errorf("need PG_DSN (unix socket)"))
	}
	db, err := sql.Open("pgx", dsn)
	check(err)
	db.SetMaxOpenConns(2)
	check(db.Ping())
	return db
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func emit(v any) {
	raw, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(raw))
}

func summary(m *dbmigrate.Manifest) {
	type t struct {
		Table    string   `json:"table"`
		Rows     int      `json:"rows"`
		AI       *int64   `json:"auto_increment,omitempty"`
		Digest   string   `json:"sha256"`
		Problems []string `json:"problems,omitempty"`
	}
	list := make([]t, 0, len(m.Tables))
	for _, x := range m.Tables {
		list = append(list, t{x.Table, x.Rows, x.AutoIncrement, x.Digest[:16], x.Problems})
	}
	raw, _ := json.MarshalIndent(map[string]any{"mode": "export", "ok": m.OK, "tables": len(m.Tables), "total_rows": m.TotalRows, "schema_sha256": m.SchemaSHA, "per_table": list}, "", "  ")
	fmt.Println(string(raw))
}

func need(v, flagName string) string {
	if v == "" {
		check(fmt.Errorf("need %s", flagName))
	}
	return v
}

func writeJSON(path string, v any) {
	raw, err := json.MarshalIndent(v, "", "  ")
	check(err)
	check(os.WriteFile(path, raw, 0o600))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "[dbmigrate_s1_v1] error:", err)
		os.Exit(1)
	}
}
