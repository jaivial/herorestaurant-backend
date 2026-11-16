package dbmigrate

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// ImportReport is the evidence of an import/verify run (no values).
type ImportReport struct {
	OK           bool              `json:"ok"`
	Ready        bool              `json:"ready_for_cutover"`
	Tables       int               `json:"tables"`
	Rows         int               `json:"rows"`
	Mismatched   []string          `json:"mismatched_tables,omitempty"`
	Independent  map[string]string `json:"independent_mismatch,omitempty"`
	Orphans      map[string]int64  `json:"fk_orphans,omitempty"`
	Sequences    map[string]string `json:"sequences"` // table -> "next=N safe_remaining=M"
	FKs          int               `json:"fks_validated"`
	FKsNotValid  []string          `json:"fks_not_valid_staging,omitempty"` // allowlisted historical orphans
	OrphanTotal  int64             `json:"allowlisted_orphan_rows,omitempty"`
	Pending      int               `json:"pending_blocking_decisions"`
	Error        string            `json:"error,omitempty"`
	Incompatible map[string]int    `json:"pg_incompatible,omitempty"` // table.column:reason -> rows
}

func loadJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func tableIndex(s *Schema) map[string]Table {
	m := map[string]Table{}
	for _, t := range s.Tables {
		m[t.Name] = t
	}
	return m
}

func isGenerated(c Column) bool {
	return strings.Contains(strings.ToLower(c.Extra), "generated") && c.GenExpr != ""
}

// pgReadExpr renders a PG column as the text Canonical expects.
func pgReadExpr(c Column, k Kind, bytea bool) string {
	col := pq(c.Name)
	if bytea {
		return "encode(" + col + ", 'hex')"
	}
	switch k {
	case KDate:
		return "to_char(" + col + ", 'YYYY-MM-DD')"
	case KTime:
		return "to_char(" + col + ", 'HH24:MI:SS.US')"
	case KDatetime:
		return "to_char(" + col + ", 'YYYY-MM-DD HH24:MI:SS.US')"
	case KTS:
		return "to_char(" + col + " AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')"
	case KBinary:
		return "encode(" + col + ", 'hex')"
	}
	return col + "::text"
}

// pgWriteExpr casts a text parameter into the column type.
func pgWriteExpr(t Table, c Column, k Kind, param string, bytea bool) (string, error) {
	if bytea {
		return "decode(" + param + ", 'hex')", nil
	}
	typ, _, _, _, err := ColumnType(t, c, false)
	if err != nil {
		return "", err
	}
	if i := strings.Index(typ, " COLLATE"); i >= 0 {
		typ = typ[:i]
	}
	switch k {
	case KBinary:
		return "decode(" + param + ", 'hex')", nil
	case KTS:
		return "CAST(" + param + " || '+00' AS timestamptz)", nil
	}
	return "CAST(" + param + " AS " + typ + ")", nil
}

// pgTableDigest re-reads a table canonically and returns (rows, digest).
func pgTableDigest(ctx context.Context, q rowQ, t Table, storage map[string]string) (int, string, error) {
	sel := make([]string, len(t.Columns))
	kinds := make([]Kind, len(t.Columns))
	scales := make([]int, len(t.Columns))
	hexText := make([]bool, len(t.Columns))
	for i, c := range t.Columns {
		k, err := ColumnKind(c)
		if err != nil {
			return 0, "", err
		}
		hexText[i] = storage[t.Name+"."+c.Name] == "bytea"
		kinds[i], sel[i] = k, pgReadExpr(c, k, hexText[i])
		if c.Scale != nil {
			scales[i] = int(*c.Scale)
		}
	}
	rows, err := q.QueryContext(ctx, pgOnlySQL("SELECT "+strings.Join(sel, ", ")+" FROM "+pq(t.Name)))
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	var hashes []string
	vals := make([]sql.NullString, len(sel))
	ptrs := make([]any, len(vals))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return 0, "", err
		}
		canon := make([]*string, len(vals))
		for i, v := range vals {
			if !v.Valid {
				continue
			}
			s, err := unhexIf(hexText[i], v.String)
			if err != nil {
				return 0, "", fmt.Errorf("%s.%s: %v", t.Name, t.Columns[i].Name, err)
			}
			cv, err := Canonical(kinds[i], scales[i], s)
			if err != nil {
				return 0, "", fmt.Errorf("%s.%s: %v", t.Name, t.Columns[i].Name, err)
			}
			canon[i] = &cv
		}
		hashes = append(hashes, RowHash(canon))
	}
	return len(hashes), TableDigest(hashes), rows.Err()
}

// IndependentDigests reads the coordinator reference ({"table","row":[hex|null]}
// with HEX(CAST(col AS BINARY)) text bytes) and digests it canonically.
func IndependentDigests(path string, s *Schema) (map[string]string, map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	ti := tableIndex(s)
	hashes := map[string][]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	for sc.Scan() {
		var line struct {
			Table string    `json:"table"`
			Row   []*string `json:"row"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return nil, nil, fmt.Errorf("independent reference: bad line")
		}
		t, ok := ti[line.Table]
		if !ok || len(line.Row) != len(t.Columns) {
			return nil, nil, fmt.Errorf("independent reference: unknown table or width for %s", line.Table)
		}
		canon := make([]*string, len(line.Row))
		for i, hv := range line.Row {
			if hv == nil {
				continue
			}
			b, err := hex.DecodeString(*hv)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: bad hex", line.Table)
			}
			k, _ := ColumnKind(t.Columns[i])
			raw := string(b)
			if k == KBinary {
				raw = hex.EncodeToString(b)
			}
			sc := 0
			if t.Columns[i].Scale != nil {
				sc = int(*t.Columns[i].Scale)
			}
			cv, err := Canonical(k, sc, raw)
			if err != nil {
				return nil, nil, fmt.Errorf("%s.%s: %v", line.Table, t.Columns[i].Name, err)
			}
			canon[i] = &cv
		}
		hashes[line.Table] = append(hashes[line.Table], RowHash(canon))
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	out, counts := map[string]string{}, map[string]int{}
	for t, h := range hashes {
		out[t], counts[t] = TableDigest(h), len(h)
	}
	return out, counts, nil
}

// CrossCheck compares the export manifest with the independent reference.
func CrossCheck(m *Manifest, s *Schema, independent string) (map[string]string, error) {
	dig, cnt, err := IndependentDigests(independent, s)
	if err != nil {
		return nil, err
	}
	bad := map[string]string{}
	for _, t := range m.Tables {
		if dig[t.Table] != t.Digest || cnt[t.Table] != t.Rows {
			if !(t.Rows == 0 && cnt[t.Table] == 0) {
				bad[t.Table] = fmt.Sprintf("rows %d vs %d", t.Rows, cnt[t.Table])
			}
		}
	}
	return bad, nil
}

// Import loads the export into an EMPTY PG database in one transaction.
// OrphanAllowlist is the STAGING-ONLY exception for source FK orphans: exact
// table + MySQL constraint name + orphan count, bound to one export schema.
// Allowlisted FKs are created NOT VALID (new writes still enforced) and the
// marker can never be ready. Not a production integrity approval.
type OrphanAllowlist struct {
	SchemaSHA256 string `json:"schema_sha256"`
	Purpose      string `json:"purpose"`
	Entries      []struct {
		Table      string `json:"table"`
		Constraint string `json:"constraint"`
		Orphans    int64  `json:"orphans"`
	} `json:"entries"`
}

func Import(ctx context.Context, db *sql.DB, dir, independent string, plan *Plan, s *Schema, allow *OrphanAllowlist) (*ImportReport, error) {
	rep := &ImportReport{Sequences: map[string]string{}, Orphans: map[string]int64{}}
	var m Manifest
	if err := loadJSON(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return rep, err
	}
	if !m.OK || len(m.Tables) != len(s.Tables) {
		return rep, fmt.Errorf("manifest not ok or table count %d != schema %d", len(m.Tables), len(s.Tables))
	}
	allowed := map[string]int64{}
	if allow != nil {
		if allow.SchemaSHA256 != m.SchemaSHA {
			return rep, fmt.Errorf("orphan allowlist is bound to another schema: refusing")
		}
		for _, e := range allow.Entries {
			allowed[e.Table+"."+e.Constraint] = e.Orphans
		}
	}
	if len(plan.Coverage.Unsupported) > 0 {
		return rep, fmt.Errorf("plan has %d unsupported items: refusing", len(plan.Coverage.Unsupported))
	}
	compat, err := PGCompat(dir, s, plan.Storage)
	if err != nil {
		return rep, err
	}
	if len(compat) > 0 {
		rep.Incompatible = compat
		return rep, fmt.Errorf("%d columns hold values PG cannot store as mapped: refusing", len(compat))
	}
	bad, err := CrossCheck(&m, s, independent)
	if err != nil {
		return rep, err
	}
	if len(bad) > 0 {
		rep.Independent = bad
		return rep, fmt.Errorf("export differs from the independent reference in %d tables", len(bad))
	}
	var n int
	if err := db.QueryRowContext(ctx, pgOnlySQL(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast') AND c.relkind IN ('r','v','m','S','p')`)).Scan(&n); err != nil {
		return rep, err
	}
	if n > 0 {
		return rep, fmt.Errorf("target database not empty (%d relations): refusing", n)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()
	exec := func(list []string) error {
		for _, q := range list {
			if _, err := tx.ExecContext(ctx, pgOnlySQL(q)); err != nil {
				return fmt.Errorf("ddl failed: %w", err) // statement text is DDL, kept out of logs on purpose
			}
		}
		return nil
	}
	if err := exec(plan.Functions); err != nil {
		return rep, err
	}
	if err := exec(plan.Types); err != nil {
		return rep, err
	}
	if err := exec(plan.Tables); err != nil {
		return rep, err
	}
	ti := tableIndex(s)
	for _, tm := range m.Tables {
		t := ti[tm.Table]
		if err := loadTable(ctx, tx, dir, t, tm, plan.Storage); err != nil {
			return rep, fmt.Errorf("%s: %w", t.Name, err)
		}
	}
	for tname, col := range plan.Sequences {
		var tm *TableManifest
		for i := range m.Tables {
			if m.Tables[i].Table == tname {
				tm = &m.Tables[i]
			}
		}
		if tm == nil || tm.AutoIncrement == nil {
			return rep, fmt.Errorf("%s: no source AUTO_INCREMENT", tname)
		}
		var maxID sql.NullString
		if err := tx.QueryRowContext(ctx, pgOnlySQL("SELECT max("+pq(col)+")::text FROM "+pq(tname))).Scan(&maxID); err != nil {
			return rep, err
		}
		if _, err := tx.ExecContext(ctx, pgOnlySQL("SELECT setval(pg_get_serial_sequence($1, $2), $3, false)"), pq(tname), col, *tm.AutoIncrement); err != nil {
			return rep, fmt.Errorf("%s: setval: %w", tname, err)
		}
		var seqMax int64
		_ = tx.QueryRowContext(ctx, pgOnlySQL("SELECT s.seqmax FROM pg_sequence s WHERE s.seqrelid = pg_get_serial_sequence($1, $2)::regclass"), pq(tname), col).Scan(&seqMax)
		rep.Sequences[tname] = fmt.Sprintf("next=%d max_id=%s seq_max=%d safe_remaining=%d", *tm.AutoIncrement, maxID.String, seqMax, seqMax-*tm.AutoIncrement+1)
	}
	if len(rep.Sequences) != len(plan.Sequences) {
		return rep, fmt.Errorf("sequences reset %d != plan %d", len(rep.Sequences), len(plan.Sequences))
	}
	if err := exec(plan.Indexes); err != nil {
		return rep, err
	}
	for _, fk := range plan.ForeignKeys {
		var orphans int64
		if err := tx.QueryRowContext(ctx, pgOnlySQL(fk.OrphanCheck)).Scan(&orphans); err != nil {
			return rep, err
		}
		if orphans > 0 {
			rep.Orphans[fk.Table+"."+fk.Name] = orphans
		}
	}
	for key, n := range rep.Orphans {
		if want, ok := allowed[key]; !ok || want != n {
			return rep, fmt.Errorf("foreign key %s has %d orphans not matching the allowlist: refusing", key, n)
		}
	}
	for key := range allowed {
		if _, ok := rep.Orphans[key]; !ok {
			return rep, fmt.Errorf("allowlist entry %s matches no orphaned foreign key: refusing", key)
		}
	}
	for _, fk := range plan.ForeignKeys {
		ddl := fk.DDL
		key := fk.Table + "." + fk.Name
		n, orphaned := rep.Orphans[key]
		if orphaned {
			rep.FKsNotValid = append(rep.FKsNotValid, key)
			rep.OrphanTotal += n
		}
		if _, err := tx.ExecContext(ctx, pgOnlySQL(ddl)); err != nil {
			return rep, fmt.Errorf("%s: %w", key, err)
		}
		if !orphaned {
			if _, err := tx.ExecContext(ctx, pgOnlySQL("ALTER TABLE "+pq(fk.Table)+" VALIDATE CONSTRAINT "+pq(fk.PGName))); err != nil {
				return rep, fmt.Errorf("%s: validate: %w", key, err)
			}
			rep.FKs++
		}
	}
	sort.Strings(rep.FKsNotValid)
	if rep.FKs+len(rep.FKsNotValid) != len(plan.ForeignKeys) {
		return rep, fmt.Errorf("foreign keys created %d+%d != plan %d", rep.FKs, len(rep.FKsNotValid), len(plan.ForeignKeys))
	}
	if err := exec(plan.Triggers); err != nil { // after the load: never fire on import
		return rep, err
	}
	if err := exec(plan.Views); err != nil {
		return rep, err
	}
	if err := verifyAll(ctx, tx, &m, s, plan.Storage, rep); err != nil {
		return rep, err
	}
	rep.Pending = len(plan.Coverage.Pending)
	if len(rep.FKsNotValid) > 0 {
		rep.Pending++ // historical_orphans: integrity exception not approved for production
	}
	rep.Ready = plan.Ready && rep.OK && len(rep.FKsNotValid) == 0
	summary, _ := json.Marshal(rep)
	if _, err := tx.ExecContext(ctx, pgOnlySQL("CREATE TABLE dbmigrate.marker (key text PRIMARY KEY, value jsonb NOT NULL, at timestamptz NOT NULL DEFAULT now())")); err != nil {
		return rep, err
	}
	if _, err := tx.ExecContext(ctx, pgOnlySQL("INSERT INTO dbmigrate.marker (key, value) VALUES ('import_verified', $1::jsonb)"), string(summary)); err != nil {
		return rep, err
	}
	return rep, tx.Commit()
}

func verifyAll(ctx context.Context, q rowQ, m *Manifest, s *Schema, storage map[string]string, rep *ImportReport) error {
	ti := tableIndex(s)
	rep.Tables, rep.Rows, rep.Mismatched = 0, 0, nil
	for _, tm := range m.Tables {
		rows, dig, err := pgTableDigest(ctx, q, ti[tm.Table], storage)
		if err != nil {
			return err
		}
		rep.Tables++
		rep.Rows += rows
		if rows != tm.Rows || dig != tm.Digest {
			rep.Mismatched = append(rep.Mismatched, tm.Table)
		}
	}
	sort.Strings(rep.Mismatched)
	rep.OK = len(rep.Mismatched) == 0 && rep.Rows == m.TotalRows && rep.Tables == len(s.Tables)
	if !rep.OK {
		return fmt.Errorf("verification failed: %d tables differ", len(rep.Mismatched))
	}
	return nil
}

func loadTable(ctx context.Context, tx *sql.Tx, dir string, t Table, tm TableManifest, storage map[string]string) error {
	var cols, exprs []string
	var idx []int
	hexArg := map[int]bool{}
	for i, c := range t.Columns {
		if isGenerated(c) {
			continue // PG recomputes it; the re-read proves the translation
		}
		k, err := ColumnKind(c)
		if err != nil {
			return err
		}
		bytea := storage[t.Name+"."+c.Name] == "bytea"
		e, err := pgWriteExpr(t, c, k, fmt.Sprintf("$%d", len(cols)+1), bytea)
		if err != nil {
			return err
		}
		if bytea {
			hexArg[i] = true // exact bytes (NUL included) travel as hex, never as PG text
		}
		cols, exprs, idx = append(cols, pq(c.Name)), append(exprs, e), append(idx, i)
	}
	over := ""
	for _, c := range t.Columns {
		if strings.Contains(c.Extra, "auto_increment") {
			over = " OVERRIDING SYSTEM VALUE"
		}
	}
	stmt, err := tx.PrepareContext(ctx, pgOnlySQL("INSERT INTO "+pq(t.Name)+" ("+strings.Join(cols, ", ")+")"+over+" VALUES ("+strings.Join(exprs, ", ")+")"))
	if err != nil {
		return err
	}
	defer stmt.Close()
	f, err := os.Open(filepath.Join(dir, tm.DataFile))
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	n := 0
	for sc.Scan() {
		var row []*string
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil || len(row) != len(t.Columns) {
			return fmt.Errorf("bad data line %d", n+1)
		}
		args := make([]any, len(idx))
		for j, i := range idx {
			if row[i] != nil {
				args[j] = *row[i]
				if hexArg[i] {
					args[j] = hex.EncodeToString([]byte(*row[i]))
				}
			}
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("row %d: %w", n+1, err)
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if n != tm.Rows {
		return fmt.Errorf("loaded %d rows, manifest %d", n, tm.Rows)
	}
	return nil
}

// Verify re-checks an imported database read-only.
func Verify(ctx context.Context, db *sql.DB, dir string, s *Schema, storage map[string]string) (*ImportReport, error) {
	rep := &ImportReport{}
	var m Manifest
	if err := loadJSON(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return rep, err
	}
	if err := verifyAll(ctx, db, &m, s, storage, rep); err != nil {
		return rep, err
	}
	// Constraint state comes from the catalog; readiness and the pending
	// count come from the import marker (never recomputed as 0 here).
	rows, err := db.QueryContext(ctx, pgOnlySQL(`SELECT c.convalidated, c.conrelid::regclass::text || '.' || c.conname
		FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE c.contype = 'f' AND n.nspname = 'public' ORDER BY 2`))
	if err != nil {
		return rep, err
	}
	rep.FKs, rep.FKsNotValid = 0, nil
	for rows.Next() {
		var ok bool
		var name string
		if err := rows.Scan(&ok, &name); err != nil {
			rows.Close()
			return rep, err
		}
		if ok {
			rep.FKs++
		} else {
			rep.FKsNotValid = append(rep.FKsNotValid, name)
		}
	}
	rows.Close()
	var marker []byte
	if err := db.QueryRowContext(ctx, pgOnlySQL("SELECT value::text FROM dbmigrate.marker WHERE key = 'import_verified'")).Scan(&marker); err != nil {
		return rep, fmt.Errorf("import marker: %w", err)
	}
	var mk ImportReport
	if err := json.Unmarshal(marker, &mk); err != nil {
		return rep, fmt.Errorf("import marker: %w", err)
	}
	rep.Pending, rep.Ready, rep.OrphanTotal = mk.Pending, mk.Ready && len(rep.FKsNotValid) == 0, mk.OrphanTotal
	return rep, nil
}

// ExportPG writes the PG data as canonical NDJSON + a manifest into a NEW
// directory (refuses an existing one): the protected rollback artefact.
func ExportPG(ctx context.Context, db *sql.DB, dir string, s *Schema, storage map[string]string) (*Manifest, error) {
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%s exists: refusing to overwrite", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manifest{Source: "postgres", Database: s.Database, OK: true}
	for _, t := range s.Tables {
		sel := make([]string, len(t.Columns))
		kinds := make([]Kind, len(t.Columns))
		hexText := make([]bool, len(t.Columns))
		for i, c := range t.Columns {
			k, err := ColumnKind(c)
			if err != nil {
				return nil, err
			}
			hexText[i] = storage[t.Name+"."+c.Name] == "bytea"
			kinds[i], sel[i] = k, pgReadExpr(c, k, hexText[i])
		}
		rows, err := db.QueryContext(ctx, pgOnlySQL("SELECT "+strings.Join(sel, ", ")+" FROM "+pq(t.Name)))
		if err != nil {
			return nil, err
		}
		// Reverse export restores the ORIGINAL kind and bytes: bytea storage is
		// decoded back to the source text in the data file.
		tm := TableManifest{Table: t.Name, Kinds: kinds, DataFile: t.Name + ".ndjson"}
		for _, c := range t.Columns {
			tm.Columns = append(tm.Columns, c.Name)
		}
		f, err := os.OpenFile(filepath.Join(dir, tm.DataFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rows.Close()
			return nil, err
		}
		w := bufio.NewWriter(f)
		var hashes []string
		vals := make([]sql.NullString, len(sel))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			line := make([]*string, len(vals))
			canon := make([]*string, len(vals))
			for i, v := range vals {
				if !v.Valid {
					continue
				}
				s, err := unhexIf(hexText[i], v.String)
				if err != nil {
					rows.Close()
					return nil, err
				}
				line[i] = &s
				sc := 0
				if t.Columns[i].Scale != nil {
					sc = int(*t.Columns[i].Scale)
				}
				cv, err := Canonical(kinds[i], sc, s)
				if err != nil {
					rows.Close()
					return nil, err
				}
				canon[i] = &cv
			}
			enc, _ := json.Marshal(line)
			w.Write(append(enc, '\n'))
			hashes = append(hashes, RowHash(canon))
		}
		rows.Close()
		if err := w.Flush(); err != nil {
			return nil, err
		}
		f.Close()
		tm.Rows, tm.Digest = len(hashes), TableDigest(hashes)
		m.TotalRows += tm.Rows
		m.Tables = append(m.Tables, tm)
	}
	if _, err := writeJSONFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		return nil, err
	}
	return m, nil
}

// CompareManifests: per-table rows+digest equality of two manifests.
func CompareManifests(a, b *Manifest) []string {
	bt := map[string]TableManifest{}
	for _, t := range b.Tables {
		bt[t.Table] = t
	}
	var diff []string
	for _, t := range a.Tables {
		o, ok := bt[t.Table]
		if !ok || o.Rows != t.Rows || o.Digest != t.Digest {
			diff = append(diff, t.Table)
		}
	}
	if len(a.Tables) != len(b.Tables) {
		diff = append(diff, fmt.Sprintf("table_count %d vs %d", len(a.Tables), len(b.Tables)))
	}
	return diff
}

func unhexIf(on bool, s string) (string, error) {
	if !on {
		return s, nil
	}
	b, err := hex.DecodeString(s)
	return string(b), err
}

// PGCompat scans the export for values PG cannot store as planned: NUL in
// text/char/enum, invalid UTF-8, and \u0000 inside JSON (jsonb rejects it).
// Counts per table.column:reason only; bytea-stored columns are exempt.
func PGCompat(dir string, s *Schema, storage map[string]string) (map[string]int, error) {
	out := map[string]int{}
	for _, t := range s.Tables {
		f, err := os.Open(filepath.Join(dir, t.Name+".ndjson"))
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<30)
		for sc.Scan() {
			var row []*string
			if err := json.Unmarshal(sc.Bytes(), &row); err != nil || len(row) != len(t.Columns) {
				f.Close()
				return nil, fmt.Errorf("%s: bad data line", t.Name)
			}
			for i, v := range row {
				c := t.Columns[i]
				if v == nil || storage[t.Name+"."+c.Name] == "bytea" {
					continue
				}
				k, _ := ColumnKind(c)
				if k == KBinary {
					continue
				}
				key := t.Name + "." + c.Name
				if !utf8.ValidString(*v) {
					out[key+":invalid_utf8"]++
				}
				if strings.IndexByte(*v, 0) >= 0 {
					out[key+":nul_byte"]++
				}
				if k == KJSON && strings.Contains(*v, `\u0000`) {
					out[key+":json_u0000"]++
				}
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
