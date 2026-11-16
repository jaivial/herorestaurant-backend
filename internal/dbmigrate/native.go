package dbmigrate

import (
	"regexp"
	"strings"
)

// Coordination id: pgnative_unique_v1 (pgnative-A). Native PostgreSQL key
// semantics: text columns are plain text with the default collation and every
// originally UNIQUE index with a text part becomes a UNIQUE expression index
// over app.norm(col) / app.norm_ascii(col); non-text parts stay raw, NULL
// semantics unchanged (NULLs distinct, as in MySQL).

// AppNormSQL is docs/pgnative/app-norm.sql (kept identical; created first).
var AppNormSQL = []string{
	"CREATE EXTENSION IF NOT EXISTS unaccent",
	"CREATE SCHEMA IF NOT EXISTS app",
	"CREATE OR REPLACE FUNCTION app.norm(x text) RETURNS text\nLANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS\n$$ SELECT lower(public.unaccent('public.unaccent'::regdictionary,\n        regexp_replace(normalize(rtrim(x), NFKC), E'[\\u200B-\\u200D\\u2060\\uFEFF]', '', 'g'))) $$",
	"CREATE OR REPLACE FUNCTION app.norm_ascii(x text) RETURNS text\nLANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$ SELECT lower(rtrim(x)) $$",
}

// asciiIdent: column names that hold ASCII identifiers (emails, slugs,
// idempotency/delivery keys, tokens, hashes, ids, domains, URLs).
var asciiIdent = regexp.MustCompile(`(^|_)(email|slug|token|hash|guid|uuid|domain|subdomain|url|sha256)($|_)|(^|_)id$|idempotency_key$|_key$|^k$`)

// NormFunc picks the folding for one text column. Rule: MySQL general_ci
// columns and ASCII identifier columns -> app.norm_ascii (case + trailing
// space only); every other text column -> app.norm (unicode_ci-like).
func NormFunc(c Column) string {
	if (c.Collation != nil && strings.Contains(*c.Collation, "general_ci")) || asciiIdent.MatchString(strings.ToLower(c.Name)) {
		return "app.norm_ascii"
	}
	return "app.norm"
}

// NativeUnique is one generated UNIQUE expression index.
type NativeUnique struct {
	Table, SourceIndex, PGName, RawName string
	Cols, TextCols, Funcs, Exprs        []string
	KeepRaw                             bool // an FK references the raw column set
}

// ConflictTarget is the exact ON CONFLICT (...) shape callers must use.
func (u NativeUnique) ConflictTarget() string { return "(" + strings.Join(u.Exprs, ", ") + ")" }

func (u NativeUnique) DDL() string {
	return "CREATE UNIQUE INDEX " + pq(u.PGName) + " ON " + pq(u.Table) + " (" + strings.Join(u.Exprs, ", ") + ")"
}

// NativeUniques lists every source UNIQUE (non-PRIMARY) BTREE index that has
// at least one text/char column.
func NativeUniques(s *Schema) []NativeUnique {
	var out []NativeUnique
	for _, t := range s.Tables {
		cols := map[string]Column{}
		for _, c := range t.Columns {
			cols[c.Name] = c
		}
		for _, ix := range t.Indexes {
			if !ix.Unique || ix.Name == "PRIMARY" || ix.Type != "BTREE" {
				continue
			}
			u := NativeUnique{Table: t.Name, SourceIndex: ix.Name, PGName: PGName("ux", t.Name, ix.Name), RawName: PGName("ix", t.Name, ix.Name)}
			for _, ic := range ix.Cols {
				c := cols[ic.Name]
				k, _ := ColumnKind(c)
				e := pq(ic.Name)
				if k == KText || k == KChar {
					f := NormFunc(c)
					e = f + "(" + pq(ic.Name) + ")"
					u.TextCols = append(u.TextCols, ic.Name)
					u.Funcs = append(u.Funcs, f)
				}
				if ic.Desc {
					e += " DESC"
				}
				u.Cols = append(u.Cols, ic.Name)
				u.Exprs = append(u.Exprs, e)
			}
			if len(u.TextCols) == 0 {
				continue
			}
			u.KeepRaw = fkTargets(s, t.Name, u.Cols)
			out = append(out, u)
		}
	}
	return out
}

func fkTargets(s *Schema, table string, cols []string) bool {
	want := strings.Join(cols, ",")
	for _, t := range s.Tables {
		for _, fk := range t.FKs {
			if fk.RefTable == table && strings.Join(fk.RefCols, ",") == want {
				return true
			}
		}
	}
	return false
}
