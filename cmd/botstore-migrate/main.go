// Command botstore-migrate copies/verifies the WhatsApp bot context store
// between SQLite and PostgreSQL (coordination id: wa_bot_store_pg_migrate_v1).
//
// Modes (the PG DSN is read from BOT_CONTEXT_POSTGRES_DSN only, never printed):
//
//	inspect          -sqlite SRC                 counts, row digests, next ids (no PG)
//	copy             -sqlite SRC                 SRC (read-only) -> empty PG, one transaction
//	verify           -sqlite SRC                 SRC vs PG, read-only
//	rollback         -sqlite NEW                 PG -> NEW SQLite file (refuses an existing path)
//	compare-sqlite   -sqlite A -sqlite2 B        A vs B, both read-only (no PG)
//	export-knowledge -sqlite SRC -out FILE       knowledge chunks + sha256, file mode 0600 (no PG)
//	mysql-inspect    -mysql SNAP                 validate + digest the snapshot (no PG)
//	mysql-import     -mysql SNAP                 MySQL bot tables snapshot -> PG, one transaction, once
//	mysql-verify     -mysql SNAP                 snapshot (or a mysql-export file) vs PG, read-only
//	mysql-export     -out FILE                   PG MySQL-bot tables -> snapshot format, 0600
//	mysql-rollback   -confirm                    delete the imported MySQL-bot rows + marker from PG
//	reconcile-knowledge -mysql SNAP [-sqlite SRC] index chunks (SQLite, else PG) vs MySQL knowledge_json
//
// Reports carry ids, counts and hashes only, never row content.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"preactvillacarmen/internal/api"
	"preactvillacarmen/internal/botstore"
)

func main() {
	mode := flag.String("mode", "inspect", "inspect | copy | verify | rollback | compare-sqlite | export-knowledge")
	path := flag.String("sqlite", "", "SQLite file")
	path2 := flag.String("sqlite2", "", "second SQLite file (compare-sqlite)")
	out := flag.String("out", "", "output file (export-knowledge, mysql-export)")
	snap := flag.String("mysql", "", "read-only MySQL bot snapshot JSON")
	confirm := flag.Bool("confirm", false, "required by mysql-rollback")
	flag.Parse()
	needSQLite := map[string]bool{"inspect": true, "copy": true, "verify": true, "rollback": true, "compare-sqlite": true, "export-knowledge": true}
	if needSQLite[*mode] && *path == "" {
		fail(fmt.Errorf("need -sqlite"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	var (
		reports []botstore.TableReport
		ok      bool
		err     error
	)
	switch *mode {
	case "inspect":
		src := openRO(*path)
		if err = botstore.CheckSourceInventory(ctx, src); err == nil {
			reports, ok, err = botstore.Verify(ctx, src, src)
		}
	case "compare-sqlite":
		if *path2 == "" {
			fail(fmt.Errorf("need -sqlite2"))
		}
		a, b := openRO(*path), openRO(*path2)
		if err = botstore.CheckSourceInventory(ctx, a); err == nil {
			if err = botstore.CheckSourceInventory(ctx, b); err == nil {
				reports, ok, err = botstore.Verify(ctx, a, b)
			}
		}
	case "export-knowledge":
		if *out == "" {
			fail(fmt.Errorf("need -out"))
		}
		exportKnowledge(ctx, openRO(*path), *out)
		return
	case "mysql-inspect":
		s, _ := loadSnap(*snap)
		reports, err = botstore.InspectMySQL(s)
		ok = err == nil
	case "mysql-import", "mysql-verify":
		s, sum := loadSnap(*snap)
		pg := openPG()
		if *mode == "mysql-import" {
			reports, err = botstore.ImportMySQL(ctx, pg, s, sum)
			ok = err == nil
		} else {
			reports, ok, err = botstore.VerifyMySQL(ctx, pg, s)
		}
		reports = append(reports, botstore.RetainedReport(s)...)
	case "mysql-export":
		if *out == "" {
			fail(fmt.Errorf("need -out"))
		}
		n, xerr := botstore.ExportMySQL(ctx, openPG(), *out)
		if xerr != nil {
			fail(xerr)
		}
		raw, _ := os.ReadFile(*out)
		sum := sha256.Sum256(raw)
		fmt.Printf("{\"mode\":\"mysql-export\",\"rows\":%d,\"file_sha256\":%q}\n", n, hex.EncodeToString(sum[:]))
		return
	case "mysql-rollback":
		if !*confirm {
			fail(fmt.Errorf("mysql-rollback needs -confirm"))
		}
		if err = botstore.RollbackMySQL(ctx, openPG()); err != nil {
			fail(err)
		}
		fmt.Println(`{"mode":"mysql-rollback","ok":true}`)
		return
	case "reconcile-knowledge":
		s, _ := loadSnap(*snap)
		var db *botstore.DB
		if *path != "" {
			db = openRO(*path)
		} else {
			db = openPG()
		}
		reconcileKnowledge(ctx, db, s)
		return
	case "copy", "verify":
		src, pg := openRO(*path), openPG()
		if *mode == "copy" {
			reports, err = botstore.Copy(ctx, src, pg)
			ok = err == nil
		} else if err = botstore.CheckSourceInventory(ctx, src); err == nil {
			reports, ok, err = botstore.Verify(ctx, src, pg)
		}
	case "rollback":
		if _, statErr := os.Stat(*path); statErr == nil {
			fail(fmt.Errorf("rollback target %s already exists: refusing to overwrite", *path))
		}
		pg := openPG()
		dst, openErr := botstore.Open(botstore.DriverSQLite, *path)
		if openErr != nil {
			fail(openErr)
		}
		reports, err = botstore.Copy(ctx, pg, dst)
		ok = err == nil
	default:
		fail(fmt.Errorf("unknown -mode %q", *mode))
	}
	report(*mode, reports, ok && err == nil)
	if err != nil {
		fail(err)
	}
	if !ok {
		os.Exit(2)
	}
}

func openRO(path string) *botstore.DB {
	if _, err := os.Stat(path); err != nil {
		fail(err)
	}
	db, err := botstore.Open(botstore.DriverSQLite, "file:"+path+"?mode=ro")
	if err != nil {
		fail(err)
	}
	return db
}

func loadSnap(path string) (*botstore.Snapshot, string) {
	if path == "" {
		fail(fmt.Errorf("need -mysql"))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	sum := sha256.Sum256(raw)
	s, err := botstore.LoadSnapshot(path)
	if err != nil {
		fail(err)
	}
	return s, hex.EncodeToString(sum[:])
}

// reconcileKnowledge compares, per restaurant, the indexed chunks with the
// chunks rebuilt from MySQL knowledge_json by the runtime's own derivation.
func reconcileKnowledge(ctx context.Context, db *botstore.DB, s *botstore.Snapshot) {
	q := "SELECT ord, restaurant_id, chunk_id, tags, title, body FROM bot_knowledge_fts ORDER BY ord"
	if !db.Postgres {
		q = "SELECT rowid, restaurant_id, chunk_id, tags, title, body FROM bot_knowledge_fts ORDER BY rowid"
	}
	rows, err := db.QueryContext(ctx, selectedSQL(q))
	if err != nil {
		fail(err)
	}
	indexed := map[string][][4]string{}
	for rows.Next() {
		var ord int64
		var rid string
		var c [4]string
		if err := rows.Scan(&ord, &rid, &c[0], &c[1], &c[2], &c[3]); err != nil {
			fail(err)
		}
		indexed[rid] = append(indexed[rid], c)
	}
	rows.Close()
	canon := map[string]string{}
	for _, k := range s.Knowledge {
		rid, ok := botstore.KnowledgeValue(k["restaurant_id"])
		if !ok {
			continue
		}
		raw, _ := botstore.KnowledgeValue(k["knowledge_json"])
		canon[rid] = raw
	}
	hash := func(list [][4]string) string {
		h := sha256.New()
		for _, c := range list {
			for _, v := range c {
				fmt.Fprintf(h, "V%d:%s;", len(v), v)
			}
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	ids := map[string]bool{}
	for k := range indexed {
		ids[k] = true
	}
	for k := range canon {
		ids[k] = true
	}
	var report []map[string]any
	allEqual := true
	for rid := range ids {
		raw, hasRow := canon[rid]
		want := api.BotIndexedKnowledge(raw)
		got := indexed[rid]
		r := map[string]any{"restaurant_id": rid, "mysql_row": hasRow, "indexed_rows": len(got), "expected_rows": len(want), "expected_sha256": hash(want)}
		if got == nil {
			r["status"] = "not_indexed (index is built on the restaurant's first turn)"
		} else {
			r["indexed_sha256"] = hash(got)
			r["equal"] = r["indexed_sha256"] == r["expected_sha256"]
			if r["equal"] != true {
				allEqual = false
				var diff []string
				for i := 0; i < max(len(got), len(want)); i++ {
					if i >= len(got) || i >= len(want) || got[i] != want[i] {
						id := ""
						if i < len(got) {
							id = got[i][0]
						} else {
							id = want[i][0]
						}
						diff = append(diff, fmt.Sprintf("#%d:%s", i, id))
					}
				}
				r["differing_positions"] = diff
			}
		}
		report = append(report, r)
	}
	raw, _ := json.MarshalIndent(map[string]any{"mode": "reconcile-knowledge", "indexed_equal_canonical": allEqual, "restaurants": report}, "", "  ")
	fmt.Println(string(raw))
	if !allEqual {
		os.Exit(2)
	}
}

func openPG() *botstore.DB {
	dsn := os.Getenv("BOT_CONTEXT_POSTGRES_DSN")
	if dsn == "" {
		fail(fmt.Errorf("need BOT_CONTEXT_POSTGRES_DSN in the environment"))
	}
	// BOT_CONTEXT_POSTGRES_SCHEMA=bot_context targets the unified business DB.
	db, err := botstore.OpenSchema(botstore.DriverPostgres, dsn, os.Getenv("BOT_CONTEXT_POSTGRES_SCHEMA"))
	if err != nil {
		fail(err)
	}
	return db
}

// exportKnowledge writes the knowledge chunks in index order plus one sha256
// per restaurant over (chunk_id, tags, title, body), for comparison with the
// chunks rebuilt from MySQL restaurant_bot_ai_config.
func exportKnowledge(ctx context.Context, db *botstore.DB, out string) {
	rows, err := db.QueryContext(ctx, selectedSQL("SELECT rowid, restaurant_id, chunk_id, tags, title, body FROM bot_knowledge_fts ORDER BY rowid"))
	if err != nil {
		fail(err)
	}
	defer rows.Close()
	type chunk struct {
		Ord          int64  `json:"ord"`
		RestaurantID int64  `json:"restaurant_id"`
		ChunkID      string `json:"chunk_id"`
		Tags         string `json:"tags"`
		Title        string `json:"title"`
		Body         string `json:"body"`
	}
	var chunks []chunk
	per := map[int64][]chunk{}
	for rows.Next() {
		var c chunk
		if err := rows.Scan(&c.Ord, &c.RestaurantID, &c.ChunkID, &c.Tags, &c.Title, &c.Body); err != nil {
			fail(err)
		}
		chunks = append(chunks, c)
		per[c.RestaurantID] = append(per[c.RestaurantID], c)
	}
	if err := rows.Err(); err != nil {
		fail(err)
	}
	hashes := map[int64]string{}
	for rid, list := range per {
		h := sha256.New()
		for _, c := range list {
			for _, v := range []string{c.ChunkID, c.Tags, c.Title, c.Body} {
				fmt.Fprintf(h, "V%d:%s;", len(v), v)
			}
		}
		hashes[rid] = hex.EncodeToString(h.Sum(nil))
	}
	raw, _ := json.MarshalIndent(map[string]any{"checkpoint": "wa_bot_store_pg_migrate_v1", "chunks": chunks, "sha256_by_restaurant": hashes}, "", "  ")
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		fail(err)
	}
	fileSum := sha256.Sum256(raw)
	summary, _ := json.Marshal(map[string]any{"mode": "export-knowledge", "chunks": len(chunks), "file_sha256": hex.EncodeToString(fileSum[:]), "sha256_by_restaurant": hashes})
	fmt.Println(string(summary))
}

func report(mode string, reports []botstore.TableReport, ok bool) {
	raw, _ := json.MarshalIndent(map[string]any{"checkpoint": "wa_bot_store_pg_migrate_v1", "mode": mode, "ok": ok, "tables": reports}, "", "  ")
	fmt.Println(string(raw))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "[wa_bot_store_pg_migrate_v1] error:", err)
	os.Exit(1)
}
