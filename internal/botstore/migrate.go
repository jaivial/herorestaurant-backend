package botstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Coordination id: wa_bot_store_pg_migrate_v1 - lossless copy of the bot
// context store between dialects (SQLite -> Postgres cutover, Postgres ->
// SQLite rollback): ids preserved, sequences advanced, every row digested.

// Table is one migrated table: PK columns first, then the rest.
type Table struct {
	Name   string
	PK     []string
	Cols   []string // all columns, PK first, in digest order
	SeqCol string   // identity column whose sequence is preserved ("" = none)
	FTS    bool     // knowledge index: SQLite FTS5 (ord = rowid) / PG tsvector
}

// Tables are every table with content. The knowledge index is copied too
// (content + metadata + order); only its FTS5 shadow tables are skipped,
// because they are rebuilt by SQLite/PG from that content.
var Tables = []Table{
	{"conversation_messages", []string{"id"}, []string{"id", "restaurant_id", "user_phone", "role", "content", "tool_name", "source", "include_in_context", "created_at_ms"}, "id", false},
	{"pipeline_decisions", []string{"id"}, []string{"id", "restaurant_id", "user_phone", "message", "decision_json", "model_used", "created_at_ms"}, "id", false},
	{"management_requests", []string{"id"}, []string{"id", "restaurant_id", "user_phone", "reason", "summary", "group_sent", "created_at_ms"}, "id", false},
	{"handoff_state", []string{"restaurant_id", "user_phone"}, []string{"restaurant_id", "user_phone", "topic", "reason", "repeats", "updated_at_ms"}, "", false},
	{"chat_images", []string{"restaurant_id", "chat_jid", "message_id"}, []string{"restaurant_id", "chat_jid", "message_id", "mime", "at_ms", "reading_source", "reading_text"}, "", false},
	{"bot_knowledge_fts", []string{"ord"}, []string{"ord", "restaurant_id", "chunk_id", "tags", "title", "body"}, "ord", true},
}

// knownSQLiteObjects are the tables a source may contain besides Tables.
var knownSQLiteObjects = map[string]bool{
	"sqlite_sequence": true, "bot_knowledge_fts_data": true, "bot_knowledge_fts_idx": true,
	"bot_knowledge_fts_content": true, "bot_knowledge_fts_docsize": true, "bot_knowledge_fts_config": true,
}

// TableReport is the per-table evidence of a copy or verify.
type TableReport struct {
	Table      string   `json:"table"`
	Source     int      `json:"source_rows"`
	Target     int      `json:"target_rows"`
	SrcDigest  string   `json:"source_sha256"`
	DstDigest  string   `json:"target_sha256"`
	MaxID      int64    `json:"max_id,omitempty"`
	SrcNextID  int64    `json:"source_next_id,omitempty"`
	DstNextID  int64    `json:"target_next_id,omitempty"`
	Mismatches []string `json:"mismatched_keys,omitempty"`
}

func (r TableReport) OK() bool {
	return r.Source == r.Target && r.SrcDigest == r.DstDigest && len(r.Mismatches) == 0 && r.DstNextID >= r.SrcNextID && (r.DstNextID == 0 || r.DstNextID > r.MaxID)
}

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// readRows returns pk -> row values (NULL kept distinct from "").
func readRows(ctx context.Context, q querier, t Table, pg bool) (map[string][]sql.NullString, error) {
	cols := strings.Join(t.Cols, ",")
	if t.FTS && !pg {
		cols = strings.Replace(cols, "ord", "rowid AS ord", 1)
	}
	rows, err := q.QueryContext(ctx, selectedSQL("SELECT "+cols+" FROM "+t.Name))
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
			return nil, fmt.Errorf("%s: scan: %w", t.Name, err)
		}
		key := rowKey(vals[:len(t.PK)])
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%s: duplicate key %q", t.Name, key)
		}
		out[key] = vals
	}
	return out, rows.Err()
}

func rowKey(vals []sql.NullString) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = v.String
	}
	return strings.Join(parts, "|")
}

// rowHash is sha256 over length-prefixed values; NULL is encoded apart.
func rowHash(vals []sql.NullString) string {
	h := sha256.New()
	for _, v := range vals {
		if !v.Valid {
			h.Write([]byte("N;"))
			continue
		}
		fmt.Fprintf(h, "V%d:%s;", len(v.String), v.String)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// tableDigest is order-independent: sha256 of the sorted row hashes.
func tableDigest(rows map[string][]sql.NullString) (string, map[string]string) {
	hashes := make(map[string]string, len(rows))
	list := make([]string, 0, len(rows))
	for k, v := range rows {
		hashes[k] = rowHash(v)
		list = append(list, hashes[k])
	}
	sort.Strings(list)
	h := sha256.New()
	for _, x := range list {
		h.Write([]byte(x))
	}
	return hex.EncodeToString(h.Sum(nil)), hashes
}

func compare(t Table, src, dst map[string][]sql.NullString) TableReport {
	sd, sh := tableDigest(src)
	dd, dh := tableDigest(dst)
	r := TableReport{Table: t.Name, Source: len(src), Target: len(dst), SrcDigest: sd, DstDigest: dd}
	for k, h := range sh {
		if dh[k] != h && len(r.Mismatches) < 20 {
			r.Mismatches = append(r.Mismatches, t.Name+":"+k)
		}
	}
	for k := range dh {
		if _, ok := sh[k]; !ok && len(r.Mismatches) < 20 {
			r.Mismatches = append(r.Mismatches, t.Name+":"+k+"(extra)")
		}
	}
	sort.Strings(r.Mismatches)
	return r
}

// CheckSourceInventory fails closed when the SQLite source carries a table
// this tool does not know (it would be silently left behind).
func CheckSourceInventory(ctx context.Context, src *DB) error {
	if src.Postgres {
		return nil
	}
	rows, err := src.QueryContext(ctx, selectedSQL("SELECT name FROM sqlite_master WHERE type='table'"))
	if err != nil {
		return err
	}
	defer rows.Close()
	known := map[string]bool{}
	for _, t := range Tables {
		known[t.Name] = true
	}
	var unknown []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		if !known[n] && !knownSQLiteObjects[n] {
			unknown = append(unknown, n)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(unknown) > 0 {
		return fmt.Errorf("source has unmapped tables %v: refusing a partial migration", unknown)
	}
	return nil
}

// validText rejects what Postgres TEXT cannot hold, so a copy never alters data.
func validText(t Table, key string, vals []sql.NullString) error {
	for i, v := range vals {
		if v.Valid && (strings.IndexByte(v.String, 0) >= 0 || !utf8.ValidString(v.String)) {
			return fmt.Errorf("%s key %s column %s: NUL byte or invalid UTF-8 cannot be stored losslessly", t.Name, key, t.Cols[i])
		}
	}
	return nil
}

// Copy copies every table src -> dst in ONE transaction, verifies counts and
// row digests inside it, advances identity sequences and writes the
// migration marker; any mismatch rolls the whole copy back. dst must be empty.
func Copy(ctx context.Context, src, dst *DB) ([]TableReport, error) {
	if err := CheckSourceInventory(ctx, src); err != nil {
		return nil, err
	}
	if err := dst.Init(); err != nil {
		return nil, err
	}
	tx, err := dst.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	txq := txQuerier{tx: tx, d: dst}
	reports := make([]TableReport, 0, len(Tables))
	for _, t := range Tables {
		var existing int
		if err := tx.QueryRowContext(ctx, selectedSQL("SELECT COUNT(*) FROM "+t.Name)).Scan(&existing); err != nil {
			return nil, err
		}
		if existing > 0 {
			return nil, fmt.Errorf("%s: target not empty (%d rows): refusing to merge", t.Name, existing)
		}
		rows, err := readRows(ctx, src, t, src.Postgres)
		if err != nil {
			return nil, err
		}
		srcNext, err := nextID(ctx, src, src.DB, t)
		if err != nil {
			return nil, err
		}
		ins := insertSQL(dst, t)
		for k, vals := range rows {
			if err := validText(t, k, vals); err != nil {
				return nil, err
			}
			args := make([]any, len(vals))
			for i, v := range vals {
				args[i], err = bindValue(t.Cols[i], v)
				if err != nil {
					return nil, fmt.Errorf("%s key %s: %w", t.Name, k, err)
				}
			}
			if t.FTS && dst.Postgres {
				args = append(args, FTSFold(vals[3].String), FTSFold(vals[4].String), FTSFold(vals[5].String))
			}
			if _, err := tx.ExecContext(ctx, selectedSQL(ins), args...); err != nil {
				return nil, fmt.Errorf("%s key %s: insert: %w", t.Name, k, err)
			}
		}
		got, err := readRows(ctx, txq, t, dst.Postgres)
		if err != nil {
			return nil, err
		}
		r := compare(t, rows, got)
		if t.SeqCol != "" {
			r.MaxID = maxKey(rows)
			r.SrcNextID = srcNext
			want := max(srcNext, r.MaxID+1)
			if err := advanceSequence(ctx, tx, dst, t, want); err != nil {
				return nil, err
			}
			if r.DstNextID, err = nextID(ctx, dst, tx, t); err != nil {
				return nil, err
			}
		}
		reports = append(reports, r)
		if !r.OK() {
			return reports, fmt.Errorf("%s: verification failed inside the copy transaction (rolled back)", t.Name)
		}
	}
	if dst.Postgres {
		if _, err := tx.ExecContext(ctx, pgOnlySQL(dst.Rebind(`INSERT INTO bot_store_meta (key,value,updated_at_ms) VALUES ('migration_verified',?,?)
			ON CONFLICT (key) DO UPDATE SET value=excluded.value, updated_at_ms=excluded.updated_at_ms`)), summarize(reports), time.Now().UnixMilli()); err != nil {
			return nil, err
		}
	}
	return reports, tx.Commit()
}

// Verify compares the two stores without writing anything.
func Verify(ctx context.Context, src, dst *DB) ([]TableReport, bool, error) {
	ok := true
	out := make([]TableReport, 0, len(Tables))
	for _, t := range Tables {
		a, err := readRows(ctx, src, t, src.Postgres)
		if err != nil {
			return nil, false, err
		}
		b, err := readRows(ctx, dst, t, dst.Postgres)
		if err != nil {
			return nil, false, err
		}
		r := compare(t, a, b)
		if t.SeqCol != "" {
			r.MaxID = maxKey(a)
			if r.SrcNextID, err = nextID(ctx, src, src.DB, t); err != nil {
				return nil, false, err
			}
			if r.DstNextID, err = nextID(ctx, dst, dst.DB, t); err != nil {
				return nil, false, err
			}
		}
		ok = ok && r.OK()
		out = append(out, r)
	}
	return out, ok, nil
}

// insertSQL inserts every column; FTS rows map ord to the SQLite rowid and
// get their tsvectors computed on Postgres.
func insertSQL(d *DB, t Table) string {
	cols := append([]string(nil), t.Cols...)
	ph := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	if t.FTS && d.Postgres {
		cols = append(cols, "tags_tsv", "doc_tsv")
		ph += ", to_tsvector('simple', ?), setweight(to_tsvector('simple', ?), 'A') || setweight(to_tsvector('simple', ?), 'B')"
	} else if t.FTS {
		cols[0] = "rowid"
	}
	return d.Rebind("INSERT INTO " + t.Name + " (" + strings.Join(cols, ",") + ") VALUES (" + ph + ")")
}

func maxKey(rows map[string][]sql.NullString) int64 {
	var m int64
	for k := range rows {
		if n, err := strconv.ParseInt(k, 10, 64); err == nil && n > m {
			m = n
		}
	}
	return m
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// nextID is the id the store would hand out next: PG sequence state, SQLite
// sqlite_sequence (which can be above max(id) after deletes) or, for FTS5,
// max(rowid)+1. q is the handle to read through (the copy transaction or the
// plain DB), queried with dialect-native SQL.
func nextID(ctx context.Context, d *DB, q rowQuerier, t Table) (int64, error) {
	if t.SeqCol == "" {
		return 0, nil
	}
	if d.Postgres {
		var seq string
		if err := q.QueryRowContext(ctx, pgOnlySQL("SELECT pg_get_serial_sequence($1, $2)"), t.Name, t.SeqCol).Scan(&seq); err != nil {
			return 0, fmt.Errorf("%s: sequence name: %w", t.Name, err)
		}
		var last int64
		var called bool
		if err := q.QueryRowContext(ctx, pgOnlySQL("SELECT last_value, is_called FROM "+seq)).Scan(&last, &called); err != nil {
			return 0, fmt.Errorf("%s: sequence state: %w", t.Name, err)
		}
		if called {
			return last + 1, nil
		}
		return last, nil
	}
	var n int64
	if t.FTS {
		err := q.QueryRowContext(ctx, selectedSQL("SELECT COALESCE(MAX(rowid),0)+1 FROM "+t.Name)).Scan(&n)
		return n, err
	}
	var seq, maxID int64
	_ = q.QueryRowContext(ctx, selectedSQL("SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = ?),0)"), t.Name).Scan(&seq)
	if err := q.QueryRowContext(ctx, selectedSQL("SELECT COALESCE(MAX(id),0) FROM "+t.Name)).Scan(&maxID); err != nil {
		return 0, err
	}
	return max(seq, maxID) + 1, nil
}

// advanceSequence makes next = want on the target.
func advanceSequence(ctx context.Context, tx *sql.Tx, d *DB, t Table, want int64) error {
	if d.Postgres {
		_, err := tx.ExecContext(ctx, pgOnlySQL("SELECT setval(pg_get_serial_sequence($1, $2), $3, false)"), t.Name, t.SeqCol, want)
		return err
	}
	if t.FTS || want <= 1 {
		return nil // FTS5 rowids continue from max(rowid)
	}
	if _, err := tx.ExecContext(ctx, selectedSQL("DELETE FROM sqlite_sequence WHERE name = ?"), t.Name); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, selectedSQL("INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)"), t.Name, want-1)
	return err
}

func summarize(rs []TableReport) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s=%d:%s", r.Table, r.Target, r.DstDigest[:16]))
	}
	return strings.Join(parts, ",")
}

// bindValue types integer columns as int64 (every non-text column of the
// schema is an integer) and keeps NULL as NULL.
func bindValue(col string, v sql.NullString) (any, error) {
	if !v.Valid {
		return nil, nil
	}
	if col == "id" || col == "ord" || col == "restaurant_id" || col == "include_in_context" || col == "group_sent" || col == "repeats" || strings.HasSuffix(col, "_ms") {
		n, err := strconv.ParseInt(v.String, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("column %s: non-integer value", col)
		}
		return n, nil
	}
	return v.String, nil
}

type txQuerier struct {
	tx *sql.Tx
	d  *DB
}

func (q txQuerier) QueryContext(ctx context.Context, s string, args ...any) (*sql.Rows, error) {
	return q.tx.QueryContext(ctx, selectedSQL(q.d.Rebind(s)), args...)
}
