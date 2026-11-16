package botstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Coordination id: wa_bot_store_pg_mysql_v1 - the MySQL bot-owned tables
// moved to PG: imported from a read-only point-in-time snapshot
// ({"tables":{name:[{col:"string"|null}]}}, every value CAST AS CHAR),
// verified row by row, exportable back to the same format.
//
// wa_bot_op_events stays in MySQL: its "proposed"/"executed" rows are
// written inside the same MySQL transaction as wa_bot_op_proposals and the
// booking/config change they audit, and the approval rate limit reads it.
// Moving it would split that transaction across two databases.

// MyCol kinds: int, text, ts0 (timestamp/datetime), ts3 (datetime(3)), num (decimal(14,8)).
type MyCol struct{ Name, Kind string }

type MyTable struct {
	Name       string
	Key        []string
	Cols       []MyCol // key columns first
	Seq        string  // identity column ("" = none)
	Quarantine bool    // imported rows are never runnable (bot_inbound_queue)
}

var MySQLTables = []MyTable{
	{"bot_inbound_queue", []string{"id"}, []MyCol{{"id", "int"}, {"restaurant_id", "int"}, {"chat_key", "text"}, {"message_id", "text"}, {"payload_json", "text"}, {"state", "text"}, {"created_at", "ts0"}, {"started_at", "ts0"}}, "id", true},
	{"whatsapp_bot_sessions", []string{"restaurant_id", "user_phone"}, []MyCol{{"restaurant_id", "int"}, {"user_phone", "text"}, {"push_name", "text"}, {"last_message_at", "ts3"}, {"created_at", "ts0"}, {"bot_paused_until", "ts3"}}, "", false},
	{"whatsapp_message_logs", []string{"id"}, []MyCol{{"id", "int"}, {"restaurant_id", "int"}, {"direction", "text"}, {"message_type", "text"}, {"user_phone", "text"}, {"content", "text"}, {"source", "text"}, {"status", "text"}, {"tool_name", "text"}, {"provider_message_id", "text"}, {"ai_model", "text"}, {"input_tokens", "int"}, {"output_tokens", "int"}, {"cost_usd", "num"}, {"correlation_id", "text"}, {"created_at", "ts3"}}, "id", false},
	{"bot_conversation_messages", []string{"id"}, []MyCol{{"id", "int"}, {"phone_number", "text"}, {"role", "text"}, {"content", "text"}, {"timestamp", "ts0"}, {"message_id", "text"}, {"from_name", "text"}, {"restaurant_id", "int"}}, "id", false},
	{"whatsapp_bot_messages", []string{"id"}, []MyCol{{"id", "int"}, {"restaurant_id", "int"}, {"user_phone", "text"}, {"role", "text"}, {"content", "text"}, {"tool_name", "text"}, {"created_at", "ts3"}}, "id", false},
}

// MySQLRetained are snapshot tables deliberately left in MySQL (reported, never imported).
var MySQLRetained = []string{"wa_bot_op_events"}

// Snapshot is the read-only MySQL export.
type Snapshot struct {
	Source    string                          `json:"source"`
	Tables    map[string][]map[string]*string `json:"tables"`
	Cols      map[string][][]string           `json:"columns,omitempty"`
	Knowledge []map[string]any                `json:"knowledge,omitempty"`
}

func LoadSnapshot(path string) (*Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	return &s, nil
}

func qcol(c string) string { return `"` + c + `"` }

// CheckSnapshotColumns fails closed when the snapshot has a column this tool
// does not map (it would be silently dropped) or misses a mapped one.
func CheckSnapshotColumns(s *Snapshot) error {
	for _, t := range MySQLTables {
		meta, ok := s.Cols[t.Name]
		if !ok {
			if _, has := s.Tables[t.Name]; !has {
				return fmt.Errorf("snapshot lacks table %s", t.Name)
			}
			continue
		}
		want := map[string]bool{}
		for _, c := range t.Cols {
			want[c.Name] = true
		}
		if len(meta) != len(t.Cols) {
			return fmt.Errorf("%s: snapshot has %d columns, mapped %d", t.Name, len(meta), len(t.Cols))
		}
		for _, m := range meta {
			if len(m) == 0 || !want[m[0]] {
				return fmt.Errorf("%s: unmapped column %v", t.Name, m)
			}
		}
	}
	return nil
}

func snapshotRows(s *Snapshot, t MyTable) (map[string][]sql.NullString, error) {
	out := map[string][]sql.NullString{}
	for i, r := range s.Tables[t.Name] {
		if len(r) != len(t.Cols) {
			return nil, fmt.Errorf("%s row %d: %d values, want %d", t.Name, i, len(r), len(t.Cols))
		}
		vals := make([]sql.NullString, len(t.Cols))
		for j, c := range t.Cols {
			v, ok := r[c.Name]
			if !ok {
				return nil, fmt.Errorf("%s row %d: missing %s", t.Name, i, c.Name)
			}
			if v != nil {
				vals[j] = sql.NullString{String: *v, Valid: true}
			}
		}
		key := rowKey(vals[:len(t.Key)])
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%s: duplicate key %q", t.Name, key)
		}
		out[key] = vals
	}
	return out, nil
}

// pgSelect renders each column as the snapshot wrote it (CAST AS CHAR).
func pgSelect(t MyTable) string {
	parts := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		switch c.Kind {
		case "int", "num":
			parts[i] = qcol(c.Name) + "::text"
		case "ts0":
			parts[i] = "to_char(" + qcol(c.Name) + ", 'YYYY-MM-DD HH24:MI:SS')"
		case "ts3":
			parts[i] = "to_char(" + qcol(c.Name) + ", 'YYYY-MM-DD HH24:MI:SS.MS')"
		default:
			parts[i] = qcol(c.Name)
		}
	}
	return "SELECT " + strings.Join(parts, ", ") + " FROM " + t.Name
}

func pgRows(ctx context.Context, q querier, t MyTable) (map[string][]sql.NullString, error) {
	rows, err := q.QueryContext(ctx, pgOnlySQL(pgSelect(t)))
	if err != nil {
		return nil, fmt.Errorf("%s: read: %w", t.Name, err)
	}
	defer rows.Close()
	out := map[string][]sql.NullString{}
	for rows.Next() {
		vals := make([]sql.NullString, len(t.Cols))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out[rowKey(vals[:len(t.Key)])] = vals
	}
	return out, rows.Err()
}

func myInsert(t MyTable) string {
	cols := make([]string, len(t.Cols))
	ph := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		cols[i] = qcol(c.Name)
		switch c.Kind {
		case "ts0":
			ph[i] = "CAST(CAST(? AS text) AS timestamp(0))"
		case "ts3":
			ph[i] = "CAST(CAST(? AS text) AS timestamp(3))"
		case "num":
			ph[i] = "CAST(CAST(? AS text) AS numeric(14,8))"
		default:
			ph[i] = "?"
		}
	}
	if t.Quarantine {
		cols = append(cols, "quarantined")
		ph = append(ph, "TRUE")
	}
	return "INSERT INTO " + t.Name + " (" + strings.Join(cols, ",") + ") VALUES (" + strings.Join(ph, ",") + ")"
}

func myCompare(name string, src, dst map[string][]sql.NullString) TableReport {
	sd, sh := tableDigest(src)
	dd, dh := tableDigest(dst)
	r := TableReport{Table: name, Source: len(src), Target: len(dst), SrcDigest: sd, DstDigest: dd}
	for k, h := range sh {
		if dh[k] != h && len(r.Mismatches) < 20 {
			r.Mismatches = append(r.Mismatches, name+":"+k)
		}
	}
	for k := range dh {
		if _, ok := sh[k]; !ok && len(r.Mismatches) < 20 {
			r.Mismatches = append(r.Mismatches, name+":"+k+"(extra)")
		}
	}
	sort.Strings(r.Mismatches)
	return r
}

func pgNext(ctx context.Context, q rowQuerier, t MyTable) (int64, error) {
	var seq string
	if err := q.QueryRowContext(ctx, pgOnlySQL("SELECT pg_get_serial_sequence($1, $2)"), t.Name, t.Seq).Scan(&seq); err != nil {
		return 0, err
	}
	var last int64
	var called bool
	if err := q.QueryRowContext(ctx, pgOnlySQL("SELECT last_value, is_called FROM "+seq)).Scan(&last, &called); err != nil {
		return 0, err
	}
	if called {
		return last + 1, nil
	}
	return last, nil
}

// ImportMySQL loads the snapshot into an already context-verified PG in ONE
// transaction. Refused when the import marker exists or any target table has
// rows (idempotency guard); the SQLite-context tables are never touched.
func ImportMySQL(ctx context.Context, pg *DB, s *Snapshot, snapshotSHA string) ([]TableReport, error) {
	// repair-wiring2: in the unified DB (non-empty schema) the MySQL-origin bot
	// tables are owned by the business schema (full business import); writing
	// them into the context schema would create empty duplicates. Legacy only.
	if pg != nil && pg.Schema != "" {
		return nil, fmt.Errorf("unified DB (schema %q): MySQL-origin bot tables live in the business schema; bot store MySQL import/rollback is legacy-only", pg.Schema)
	}
	if !pg.Postgres {
		return nil, fmt.Errorf("mysql-import target must be postgres")
	}
	if err := CheckSnapshotColumns(s); err != nil {
		return nil, err
	}
	if err := pg.Init(); err != nil {
		return nil, err
	}
	var ctxOK, done int
	if err := pg.DB.QueryRowContext(ctx, pgOnlySQL("SELECT COUNT(*) FROM bot_store_meta WHERE key='migration_verified'")).Scan(&ctxOK); err != nil {
		return nil, err
	}
	if ctxOK == 0 {
		return nil, fmt.Errorf("context migration marker missing: run -mode copy first")
	}
	if err := pg.DB.QueryRowContext(ctx, pgOnlySQL("SELECT COUNT(*) FROM bot_store_meta WHERE key='mysql_import_verified'")).Scan(&done); err != nil {
		return nil, err
	}
	if done > 0 {
		return nil, fmt.Errorf("mysql import already verified: refusing a second import")
	}
	tx, err := pg.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	txq := txQuerier{tx: tx, d: pg}
	var reports []TableReport
	for _, t := range MySQLTables {
		var n int
		if err := tx.QueryRowContext(ctx, pgOnlySQL("SELECT COUNT(*) FROM "+t.Name)).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, fmt.Errorf("%s: target not empty (%d rows): refusing to merge", t.Name, n)
		}
		src, err := snapshotRows(s, t)
		if err != nil {
			return nil, err
		}
		ins := pg.Rebind(myInsert(t))
		for k, vals := range src {
			if err := validText(Table{Name: t.Name, Cols: colNames(t)}, k, vals); err != nil {
				return nil, err
			}
			args := make([]any, len(vals))
			for i, v := range vals {
				if !v.Valid {
					continue
				}
				if t.Cols[i].Kind == "int" {
					x, perr := strconv.ParseInt(v.String, 10, 64)
					if perr != nil {
						return nil, fmt.Errorf("%s key %s: column %s non-integer", t.Name, k, t.Cols[i].Name)
					}
					args[i] = x
				} else {
					args[i] = v.String
				}
			}
			if _, err := tx.ExecContext(ctx, pgOnlySQL(ins), args...); err != nil {
				return nil, fmt.Errorf("%s key %s: insert: %w", t.Name, k, err)
			}
		}
		got, err := pgRows(ctx, txq, t)
		if err != nil {
			return nil, err
		}
		r := myCompare(t.Name, src, got)
		if t.Seq != "" {
			r.MaxID = maxKey(src)
			r.SrcNextID = r.MaxID + 1 // AUTO_INCREMENT is not in the snapshot: max+1
			if _, err := tx.ExecContext(ctx, pgOnlySQL("SELECT setval(pg_get_serial_sequence($1, $2), $3, false)"), t.Name, t.Seq, r.SrcNextID); err != nil {
				return nil, err
			}
			if r.DstNextID, err = pgNext(ctx, tx, t); err != nil {
				return nil, err
			}
		}
		reports = append(reports, r)
		if !r.OK() {
			return reports, fmt.Errorf("%s: verification failed inside the import transaction (rolled back)", t.Name)
		}
	}
	if _, err := tx.ExecContext(ctx, pgOnlySQL("INSERT INTO bot_store_meta (key, value, updated_at_ms) VALUES ('mysql_import_verified', $1, $2)"),
		"snapshot_sha256="+snapshotSHA+";"+summarize(reports), time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	return reports, tx.Commit()
}

// VerifyMySQL compares snapshot and PG, read-only.
func VerifyMySQL(ctx context.Context, pg *DB, s *Snapshot) ([]TableReport, bool, error) {
	if err := CheckSnapshotColumns(s); err != nil {
		return nil, false, err
	}
	ok := true
	var out []TableReport
	for _, t := range MySQLTables {
		src, err := snapshotRows(s, t)
		if err != nil {
			return nil, false, err
		}
		got, err := pgRows(ctx, pg, t)
		if err != nil {
			return nil, false, err
		}
		r := myCompare(t.Name, src, got)
		if t.Seq != "" {
			r.MaxID = maxKey(src)
			r.SrcNextID = r.MaxID + 1
			if r.DstNextID, err = pgNext(ctx, pg.DB, t); err != nil {
				return nil, false, err
			}
		}
		ok = ok && r.OK()
		out = append(out, r)
	}
	return out, ok, nil
}

// RetainedReport digests the snapshot tables that stay in MySQL.
func RetainedReport(s *Snapshot) []TableReport {
	var out []TableReport
	for _, name := range MySQLRetained {
		rows := map[string][]sql.NullString{}
		for i, r := range s.Tables[name] {
			keys := make([]string, 0, len(r))
			for k := range r {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			vals := make([]sql.NullString, 0, len(keys))
			for _, k := range keys {
				if v := r[k]; v != nil {
					vals = append(vals, sql.NullString{String: k + "=" + *v, Valid: true})
				} else {
					vals = append(vals, sql.NullString{})
				}
			}
			rows[strconv.Itoa(i)] = vals
		}
		d, _ := tableDigest(rows)
		out = append(out, TableReport{Table: name + " (retained in MySQL, not imported)", Source: len(rows), SrcDigest: d})
	}
	return out
}

// ExportMySQL writes the PG copy back in the snapshot format (rollback
// artefact for re-loading MySQL). File mode 0600.
func ExportMySQL(ctx context.Context, pg *DB, path string) (int, error) {
	out := Snapshot{Source: "pg-export-wa_bot_store_pg_mysql_v1", Tables: map[string][]map[string]*string{}}
	total := 0
	for _, t := range MySQLTables {
		rows, err := pgRows(ctx, pg, t)
		if err != nil {
			return 0, err
		}
		keys := make([]string, 0, len(rows))
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		list := make([]map[string]*string, 0, len(rows))
		for _, k := range keys {
			m := map[string]*string{}
			for i, c := range t.Cols {
				if v := rows[k][i]; v.Valid {
					s := v.String
					m[c.Name] = &s
				} else {
					m[c.Name] = nil
				}
			}
			list = append(list, m)
		}
		out.Tables[t.Name] = list
		total += len(list)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return 0, err
	}
	return total, os.WriteFile(path, raw, 0o600)
}

// RollbackMySQL removes the imported MySQL tables' rows and marker from PG
// in one transaction (pre-cutover undo; MySQL is still the source of truth).
// The SQLite-context tables and their marker are not touched.
func RollbackMySQL(ctx context.Context, pg *DB) error {
	// repair-wiring2: in the unified DB (non-empty schema) the MySQL-origin bot
	// tables are owned by the business schema (full business import); writing
	// them into the context schema would create empty duplicates. Legacy only.
	if pg != nil && pg.Schema != "" {
		return fmt.Errorf("unified DB (schema %q): MySQL-origin bot tables live in the business schema; bot store MySQL import/rollback is legacy-only", pg.Schema)
	}
	tx, err := pg.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range MySQLTables {
		if _, err := tx.ExecContext(ctx, pgOnlySQL("DELETE FROM "+t.Name)); err != nil {
			return err
		}
		if t.Seq != "" {
			if _, err := tx.ExecContext(ctx, pgOnlySQL("SELECT setval(pg_get_serial_sequence($1, $2), 1, false)"), t.Name, t.Seq); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, pgOnlySQL("DELETE FROM bot_store_meta WHERE key='mysql_import_verified'")); err != nil {
		return err
	}
	return tx.Commit()
}

func colNames(t MyTable) []string {
	out := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		out[i] = c.Name
	}
	return out
}

// KnowledgeValue renders a knowledge field (string, number or null) as text.
func KnowledgeValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case json.Number:
		return x.String(), true
	}
	return "", false
}

// InspectMySQL validates the snapshot against the mapped schema and digests
// it, without any database (wa_bot_store_pg_mysql_v1).
func InspectMySQL(s *Snapshot) ([]TableReport, error) {
	if err := CheckSnapshotColumns(s); err != nil {
		return nil, err
	}
	var out []TableReport
	for _, t := range MySQLTables {
		src, err := snapshotRows(s, t)
		if err != nil {
			return nil, err
		}
		for k, vals := range src {
			if err := validText(Table{Name: t.Name, Cols: colNames(t)}, k, vals); err != nil {
				return nil, err
			}
		}
		r := myCompare(t.Name, src, src)
		if t.Seq != "" {
			r.MaxID = maxKey(src)
			r.SrcNextID = r.MaxID + 1
		}
		out = append(out, r)
	}
	return append(out, RetainedReport(s)...), nil
}
