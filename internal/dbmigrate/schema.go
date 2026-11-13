package dbmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// Coordination id: dbmigrate_s1_v1 - full MySQL -> PG migration tooling.
// Metadata comes from information_schema of the restored source, never from
// dump text. Every value is read as text; nothing is printed.

type Column struct {
	Name      string  `json:"name"`
	Pos       int     `json:"pos"`
	Default   *string `json:"default"`
	Nullable  bool    `json:"nullable"`
	DataType  string  `json:"data_type"`
	ColType   string  `json:"column_type"`
	CharLen   *int64  `json:"char_len"`
	Precision *int64  `json:"precision"`
	Scale     *int64  `json:"scale"`
	DTPrec    *int64  `json:"datetime_precision"`
	Collation *string `json:"collation"`
	Extra     string  `json:"extra"`
	GenExpr   string  `json:"generation_expression"`
}

type IndexCol struct {
	Name    string  `json:"name"`
	Desc    bool    `json:"desc"`
	SubPart *int64  `json:"sub_part"`
	Expr    *string `json:"expression"`
}

type Index struct {
	Name   string     `json:"name"`
	Unique bool       `json:"unique"`
	Type   string     `json:"type"`
	Cols   []IndexCol `json:"cols"`
}

type ForeignKey struct {
	Name     string   `json:"name"`
	Cols     []string `json:"cols"`
	RefTable string   `json:"ref_table"`
	RefCols  []string `json:"ref_cols"`
	OnUpdate string   `json:"on_update"`
	OnDelete string   `json:"on_delete"`
}

type Check struct {
	Name   string `json:"name"`
	Clause string `json:"clause"`
}

type Table struct {
	Name          string       `json:"name"`
	Engine        string       `json:"engine"`
	Collation     string       `json:"collation"`
	AutoIncrement *int64       `json:"auto_increment"`
	Columns       []Column     `json:"columns"`
	Indexes       []Index      `json:"indexes"`
	FKs           []ForeignKey `json:"foreign_keys"`
	Checks        []Check      `json:"checks"`
}

type View struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

type Schema struct {
	Database string           `json:"database"`
	Version  string           `json:"server_version"`
	Tables   []Table          `json:"tables"`
	Views    []View           `json:"views"`
	Other    map[string]int64 `json:"other_objects"` // triggers, routines, events
	// Optional (absent in archived exports): per-collation PAD_ATTRIBUTE.
	Collations          []CollationMeta `json:"collations,omitempty"`
	CollationMetaSource string          `json:"collation_metadata_source,omitempty"`
	CollationMetaError  string          `json:"collation_metadata_error,omitempty"`
}

type rowQ interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// ExtractSchema reads the full schema of db (MySQL 8 information_schema).
func ExtractSchema(ctx context.Context, q rowQ, db string) (*Schema, error) {
	s := &Schema{Database: db, Other: map[string]int64{}}
	if err := scanAll(ctx, q, "SELECT VERSION()", nil, func(r *sql.Rows) error { return r.Scan(&s.Version) }); err != nil {
		return nil, err
	}
	byName := map[string]*Table{}
	err := scanAll(ctx, q, `SELECT TABLE_NAME, TABLE_TYPE, COALESCE(ENGINE,''), COALESCE(TABLE_COLLATION,''), AUTO_INCREMENT
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, []any{db}, func(r *sql.Rows) error {
		var name, typ, eng, coll string
		var ai sql.NullInt64
		if err := r.Scan(&name, &typ, &eng, &coll, &ai); err != nil {
			return err
		}
		if typ == "VIEW" {
			s.Views = append(s.Views, View{Name: name})
			return nil
		}
		if typ != "BASE TABLE" {
			return fmt.Errorf("unsupported table type %s for %s", typ, name)
		}
		t := Table{Name: name, Engine: eng, Collation: coll}
		if ai.Valid {
			v := ai.Int64
			t.AutoIncrement = &v
		}
		s.Tables = append(s.Tables, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range s.Tables {
		byName[s.Tables[i].Name] = &s.Tables[i]
	}
	err = scanAll(ctx, q, `SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_DEFAULT, IS_NULLABLE, DATA_TYPE, COLUMN_TYPE,
		CHARACTER_MAXIMUM_LENGTH, NUMERIC_PRECISION, NUMERIC_SCALE, DATETIME_PRECISION, COLLATION_NAME, EXTRA, COALESCE(GENERATION_EXPRESSION,'')
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME, ORDINAL_POSITION`, []any{db}, func(r *sql.Rows) error {
		var tn, nullable string
		var c Column
		var def, coll sql.NullString
		var cl, np, ns, dp sql.NullInt64
		if err := r.Scan(&tn, &c.Name, &c.Pos, &def, &nullable, &c.DataType, &c.ColType, &cl, &np, &ns, &dp, &coll, &c.Extra, &c.GenExpr); err != nil {
			return err
		}
		c.Nullable = nullable == "YES"
		c.Default, c.Collation = nstr(def), nstr(coll)
		c.CharLen, c.Precision, c.Scale, c.DTPrec = nint(cl), nint(np), nint(ns), nint(dp)
		if t := byName[tn]; t != nil {
			t.Columns = append(t.Columns, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	idx := map[string]map[string]*Index{}
	order := map[string][]string{}
	err = scanAll(ctx, q, `SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME, COALESCE(COLLATION,'A'), SUB_PART, INDEX_TYPE, EXPRESSION
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, []any{db}, func(r *sql.Rows) error {
		var tn, in, coll, typ string
		var nonUnique int
		var col, expr sql.NullString
		var sub sql.NullInt64
		if err := r.Scan(&tn, &in, &nonUnique, &col, &coll, &sub, &typ, &expr); err != nil {
			return err
		}
		if idx[tn] == nil {
			idx[tn] = map[string]*Index{}
		}
		x := idx[tn][in]
		if x == nil {
			x = &Index{Name: in, Unique: nonUnique == 0, Type: typ}
			idx[tn][in] = x
			order[tn] = append(order[tn], in)
		}
		x.Cols = append(x.Cols, IndexCol{Name: col.String, Desc: coll == "D", SubPart: nint(sub), Expr: nstr(expr)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for tn, names := range order {
		if t := byName[tn]; t != nil {
			for _, n := range names {
				t.Indexes = append(t.Indexes, *idx[tn][n])
			}
		}
	}
	fks := map[string]*ForeignKey{}
	var fkOrder []string
	fkTable := map[string]string{}
	err = scanAll(ctx, q, `SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
		WHERE k.TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, []any{db}, func(r *sql.Rows) error {
		var tn, cn, col, rt, rc, ur, dr string
		if err := r.Scan(&tn, &cn, &col, &rt, &rc, &ur, &dr); err != nil {
			return err
		}
		key := tn + "\x00" + cn
		f := fks[key]
		if f == nil {
			f = &ForeignKey{Name: cn, RefTable: rt, OnUpdate: ur, OnDelete: dr}
			fks[key] = f
			fkOrder = append(fkOrder, key)
			fkTable[key] = tn
		}
		f.Cols = append(f.Cols, col)
		f.RefCols = append(f.RefCols, rc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, k := range fkOrder {
		if t := byName[fkTable[k]]; t != nil {
			t.FKs = append(t.FKs, *fks[k])
		}
	}
	err = scanAll(ctx, q, `SELECT tc.TABLE_NAME, cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
		FROM information_schema.CHECK_CONSTRAINTS cc
		JOIN information_schema.TABLE_CONSTRAINTS tc ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME AND tc.CONSTRAINT_TYPE = 'CHECK'
		WHERE cc.CONSTRAINT_SCHEMA = ? ORDER BY tc.TABLE_NAME, cc.CONSTRAINT_NAME`, []any{db}, func(r *sql.Rows) error {
		var tn string
		var c Check
		if err := r.Scan(&tn, &c.Name, &c.Clause); err != nil {
			return err
		}
		if t := byName[tn]; t != nil {
			t.Checks = append(t.Checks, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range s.Views {
		v := &s.Views[i]
		// A broken view (MySQL 1356) still has its stored text here.
		_ = scanAll(ctx, q, `SELECT COALESCE(VIEW_DEFINITION,'') FROM information_schema.VIEWS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, []any{db, v.Name},
			func(r *sql.Rows) error { return r.Scan(&v.Definition) })
	}
	for kind, qs := range map[string]string{
		"triggers": "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ?",
		"routines": "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ?",
		"events":   "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA = ?",
	} {
		var n int64
		if err := scanAll(ctx, q, qs, []any{db}, func(r *sql.Rows) error { return r.Scan(&n) }); err != nil {
			return nil, err
		}
		s.Other[kind] = n
	}
	sort.Slice(s.Tables, func(i, j int) bool { return s.Tables[i].Name < s.Tables[j].Name })
	extractCollations(ctx, q, db, s)
	return s, nil
}

func scanAll(ctx context.Context, q rowQ, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, mysqlOnlySQL(query), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func nstr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nint(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
