package dbmigrate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// TableManifest is the per-table evidence of an export (no values).
type TableManifest struct {
	Table         string   `json:"table"`
	Columns       []string `json:"columns"`
	Kinds         []Kind   `json:"kinds"`
	Rows          int      `json:"rows"`
	AutoIncrement *int64   `json:"auto_increment,omitempty"`
	Digest        string   `json:"table_sha256"`
	DataFile      string   `json:"data_file"`
	DataSHA256    string   `json:"data_file_sha256"`
	HashFile      string   `json:"row_hash_file"`
	Problems      []string `json:"problems,omitempty"` // "column:reason xN", never values
}

type Manifest struct {
	Source     string          `json:"source"`
	Database   string          `json:"database"`
	Version    string          `json:"server_version"`
	SchemaFile string          `json:"schema_file"`
	SchemaSHA  string          `json:"schema_sha256"`
	Tables     []TableManifest `json:"tables"`
	TotalRows  int             `json:"total_rows"`
	OK         bool            `json:"ok"`
}

func mq(id string) string { return "`" + strings.ReplaceAll(id, "`", "``") + "`" }

// ExportMySQL writes schema.json, one raw NDJSON + row-hash file per table
// and manifest.json into dir (all 0600), inside ONE consistent read-only
// snapshot. Raw values keep the source bytes (binary as hex); hashes use the
// canonical form. Problems fail the export after all tables are scanned.
func ExportMySQL(ctx context.Context, db *sql.DB, dbName, dir string) (*Manifest, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	for _, s := range []string{
		"SET SESSION time_zone = '+00:00'",
		"SET SESSION information_schema_stats_expiry = 0",
		"SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ",
		"START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY",
	} {
		if _, err := conn.ExecContext(ctx, mysqlOnlySQL(s)); err != nil {
			return nil, fmt.Errorf("session setup: %w", err)
		}
	}
	defer conn.ExecContext(context.Background(), mysqlOnlySQL("ROLLBACK"))
	schema, err := ExtractSchema(ctx, conn, dbName)
	if err != nil {
		return nil, err
	}
	m := &Manifest{Source: "mysql", Database: dbName, Version: schema.Version, SchemaFile: "schema.json", OK: true}
	if m.SchemaSHA, err = writeJSONFile(filepath.Join(dir, "schema.json"), schema); err != nil {
		return nil, err
	}
	for _, t := range schema.Tables {
		tm, err := exportTable(ctx, conn, t, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Name, err)
		}
		if len(tm.Problems) > 0 {
			m.OK = false
		}
		m.TotalRows += tm.Rows
		m.Tables = append(m.Tables, *tm)
	}
	if _, err := writeJSONFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		return nil, err
	}
	return m, nil
}

func pkCols(t Table) []string {
	for _, ix := range t.Indexes {
		if ix.Name == "PRIMARY" {
			out := make([]string, len(ix.Cols))
			for i, c := range ix.Cols {
				out[i] = c.Name
			}
			return out
		}
	}
	return nil
}

func exportTable(ctx context.Context, conn *sql.Conn, t Table, dir string) (*TableManifest, error) {
	tm := &TableManifest{Table: t.Name, AutoIncrement: t.AutoIncrement, DataFile: t.Name + ".ndjson", HashFile: t.Name + ".rowhash"}
	sel := make([]string, len(t.Columns))
	scales := make([]int, len(t.Columns))
	pos := map[string]int{}
	for i, c := range t.Columns {
		k, err := ColumnKind(c)
		if err != nil {
			return nil, err
		}
		tm.Columns, tm.Kinds = append(tm.Columns, c.Name), append(tm.Kinds, k)
		if c.Scale != nil {
			scales[i] = int(*c.Scale)
		}
		pos[c.Name] = i
		sel[i] = mq(c.Name)
		if k == KBinary {
			sel[i] = "HEX(" + mq(c.Name) + ")"
		}
	}
	pk := pkCols(t)
	rows, err := conn.QueryContext(ctx, mysqlOnlySQL("SELECT "+strings.Join(sel, ", ")+" FROM "+mq(t.Name)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	df, err := os.OpenFile(filepath.Join(dir, tm.DataFile), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer df.Close()
	dsum := sha256.New()
	w := bufio.NewWriter(df)
	problems := map[string]int{}
	keyed := map[string]string{}
	raw := make([]sql.RawBytes, len(t.Columns))
	ptrs := make([]any, len(raw))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		line := make([]*string, len(raw))
		canon := make([]*string, len(raw))
		for i, b := range raw {
			if b == nil {
				if !t.Columns[i].Nullable {
					problems[t.Columns[i].Name+":null_in_not_null"]++
				}
				continue
			}
			s := string(b)
			line[i] = &s
			cv, cerr := Canonical(tm.Kinds[i], scales[i], s)
			if cerr != nil {
				problems[t.Columns[i].Name+":"+cerr.Error()]++
				continue
			}
			canon[i] = &cv
		}
		enc, _ := json.Marshal(line)
		enc = append(enc, '\n')
		w.Write(enc)
		dsum.Write(enc)
		h := RowHash(canon)
		key := h
		if len(pk) > 0 {
			parts := make([]string, len(pk))
			for i, c := range pk {
				if v := canon[pos[c]]; v != nil {
					parts[i] = *v
				}
			}
			key = strings.Join(parts, "\x1f")
		}
		for n := 0; ; n++ { // tables without PK: duplicate rows stay distinct
			k := key
			if n > 0 {
				k = key + "#" + strconv.Itoa(n)
			}
			if _, dup := keyed[k]; !dup {
				keyed[k] = h
				break
			}
			if len(pk) > 0 {
				problems["primary_key:duplicate"]++
				break
			}
		}
		tm.Rows++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}
	tm.DataSHA256 = hex.EncodeToString(dsum.Sum(nil))
	hashes := make([]string, 0, len(keyed))
	keys := make([]string, 0, len(keyed))
	for k, h := range keyed {
		keys = append(keys, k)
		hashes = append(hashes, h)
	}
	tm.Digest = TableDigest(hashes)
	sort.Strings(keys)
	var hb strings.Builder
	for _, k := range keys {
		// The key is hashed too: row-hash files never carry primary-key values.
		ks := sha256.Sum256([]byte(k))
		hb.WriteString(hex.EncodeToString(ks[:]) + "\t" + keyed[k] + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, tm.HashFile), []byte(hb.String()), 0o600); err != nil {
		return nil, err
	}
	for p, n := range problems {
		tm.Problems = append(tm.Problems, fmt.Sprintf("%s x%d", p, n))
	}
	sort.Strings(tm.Problems)
	return tm, nil
}

func writeJSONFile(path string, v any) (string, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	s := sha256.Sum256(raw)
	return hex.EncodeToString(s[:]), nil
}
