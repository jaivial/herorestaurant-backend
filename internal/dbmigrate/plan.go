package dbmigrate

import (
	"fmt"
	"sort"
	"strings"
)

// Plan is the generated PG schema, split by load phase, plus coverage.
type Plan struct {
	Functions   []string          `json:"functions"`    // dbmigrate schema helpers
	Types       []string          `json:"types"`        // enum types
	Tables      []string          `json:"tables"`       // CREATE TABLE (PK, NOT NULL, defaults, generated, CHECK)
	Indexes     []string          `json:"indexes"`      // after data
	ForeignKeys []PlanFK          `json:"foreign_keys"` // after data, orphan-checked first
	Triggers    []string          `json:"triggers"`     // after data (never fire on import)
	Sequences   map[string]string `json:"sequences"`    // table -> identity column
	Views       []string          `json:"views"`
	Storage     map[string]string `json:"storage_overrides"` // table.column -> bytea (original kind kept for canonical form)
	Ready       bool              `json:"ready_for_cutover"` // false while anything is unsupported or pending
	Coverage    Coverage          `json:"coverage"`
}

type PlanFK struct {
	Table       string `json:"table"`
	Name        string `json:"name"`
	PGName      string `json:"pg_name"`
	OrphanCheck string `json:"orphan_check"`
	DDL         string `json:"ddl"`
}

type Coverage struct {
	Tables, Columns, Indexes, Unique, Fulltext, FKs, Checks, Generated, OnUpdate, Enums, AutoInc int
	Views                                                                                        []string            `json:"views"`
	Unsupported                                                                                  []string            `json:"unsupported"`                // stop-and-report items
	Pending                                                                                      map[string][]string `json:"pending_blocking_decisions"` // block cutover, not staging import
	ViewProv                                                                                     []ViewOverride      `json:"view_provenance"`
	Notes                                                                                        map[string][]string `json:"notes"` // informational
	Other                                                                                        map[string]int64    `json:"other_objects"`
}

const helperDate = `CREATE FUNCTION dbmigrate.date_iso(d date) RETURNS text LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$ SELECT to_char(d, 'YYYY-MM-DD') $$`

// helperOnUpdate mirrors MySQL ON UPDATE CURRENT_TIMESTAMP(fsp): the auto
// column gets the STATEMENT time truncated to its fsp ("col:kind:fsp") only
// when some other base column really changed and the UPDATE did not assign the
// column itself. repair-enums1:
//   - ALL stored generated columns of the table are excluded by catalog lookup
//     (generated values are not yet computed in BEFORE triggers, so comparing
//     them would be a false change; future shadow __pad columns are covered
//     without new args). "-col" args stay accepted.
//   - explicit assignment (including the same value) is detected by a
//     per-column BEFORE UPDATE OF <col> mark trigger (onupc__…, fires before
//     onupd__… by name order for the same row) that records "relid:col" in the
//     transaction-local setting dbmigrate.onupd_explicit; this trigger reads
//     and clears it first. Safety requires that no OTHER BEFORE UPDATE row
//     trigger on the same table returns NULL between onupc… and onupd… (the
//     source has 0 triggers; the plan creates only these): a stale mark of the
//     same table would otherwise suppress one refresh of the next row. A mark of
//     a different table is detected and raised (fail closed). Subtransaction
//     rollback reverts the setting (GUC semantics).
const helperOnUpdate = `CREATE FUNCTION dbmigrate.on_update_ts() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  a text; c text; kind text; fsp text; ts timestamptz; g name; explicit text;
  o jsonb := to_jsonb(OLD); n jsonb := to_jsonb(NEW); patch jsonb := '{}'::jsonb;
BEGIN
  explicit := COALESCE(current_setting('dbmigrate.onupd_explicit', true), '');
  PERFORM set_config('dbmigrate.onupd_explicit', '', true);
  -- Fail closed: a mark from another table means a mark was not consumed by
  -- its own refresh (e.g. a foreign BEFORE trigger returned NULL in between).
  IF explicit <> '' AND EXISTS (SELECT 1 FROM unnest(string_to_array(explicit, ',')) m WHERE split_part(m, ':', 1) <> TG_RELID::text) THEN
    RAISE EXCEPTION 'dbmigrate.on_update_ts: stale explicit-column mark % (unconsumed mark trigger)', explicit;
  END IF;
  FOR g IN SELECT attname FROM pg_attribute WHERE attrelid = TG_RELID AND attnum > 0 AND NOT attisdropped AND attgenerated <> '' LOOP
    o := o - g::text; n := n - g::text;
  END LOOP;
  FOREACH a IN ARRAY TG_ARGV LOOP
    c := split_part(ltrim(a, '-'), ':', 1);
    o := o - c; n := n - c;
  END LOOP;
  IF o IS NOT DISTINCT FROM n THEN RETURN NEW; END IF;
  FOREACH a IN ARRAY TG_ARGV LOOP
    CONTINUE WHEN left(a, 1) = '-';
    c := split_part(a, ':', 1); kind := split_part(a, ':', 2); fsp := split_part(a, ':', 3);
    CONTINUE WHEN position(',' || TG_RELID::text || ':' || c || ',' IN ',' || explicit || ',') > 0;
    ts := CASE fsp WHEN '0' THEN date_trunc('second', statement_timestamp()) WHEN '3' THEN date_trunc('milliseconds', statement_timestamp()) WHEN '6' THEN statement_timestamp() END;
    IF ts IS NULL THEN RAISE EXCEPTION 'dbmigrate.on_update_ts: unsupported fsp % for %', fsp, c; END IF;
    IF (to_jsonb(NEW) -> c) IS NOT DISTINCT FROM (to_jsonb(OLD) -> c) THEN
      patch := patch || jsonb_build_object(c, CASE WHEN kind = 'tz' THEN to_jsonb(ts) ELSE to_jsonb(ts AT TIME ZONE 'UTC') END);
    END IF;
  END LOOP;
  IF patch <> '{}'::jsonb THEN NEW := jsonb_populate_record(NEW, patch); END IF;
  RETURN NEW;
END $$`

// helperOnUpdateMark records that the current UPDATE assigned TG_ARGV[0].
const helperOnUpdateMark = `CREATE FUNCTION dbmigrate.on_update_mark() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur text := COALESCE(current_setting('dbmigrate.onupd_explicit', true), '');
BEGIN
  PERFORM set_config('dbmigrate.onupd_explicit', CASE WHEN cur = '' THEN '' ELSE cur || ',' END || TG_RELID::text || ':' || TG_ARGV[0], true);
  RETURN NEW;
END $$`

func fkRule(r string) (string, error) {
	switch r {
	case "CASCADE", "SET NULL", "RESTRICT":
		return r, nil
	case "NO ACTION": // InnoDB NO ACTION is an immediate RESTRICT
		return "RESTRICT", nil
	}
	return "", fmt.Errorf("unsupported FK rule %s", r)
}

// BuildPlan maps the whole schema; unsupported items are listed, never skipped silently.
// ViewOverride is a reviewed PG translation of a source view, with the
// provenance of the original definition (the restored snapshot may lack it).
type ViewOverride struct {
	Name         string            `json:"name"`
	SourceFile   string            `json:"source_file"`
	SourceSHA256 string            `json:"source_sha256"`
	SourceDDL    string            `json:"source_ddl"`
	SourceState  string            `json:"source_state"`
	PGSQL        string            `json:"pg_sql"`
	Requires     map[string]string `json:"requires_source_types"` // table.column -> MySQL DATA_TYPE the translation assumes
	Semantics    string            `json:"semantic_differences"`
}

// identityLinked returns the bigint-unsigned columns that must stay bigint:
// identities plus every column connected to one through foreign keys.
func identityLinked(s *Schema) map[string]bool {
	ub := map[string]bool{}
	linked := map[string]bool{}
	for _, t := range s.Tables {
		for _, c := range t.Columns {
			if c.DataType == "bigint" && strings.Contains(c.ColType, "unsigned") {
				ub[t.Name+"."+c.Name] = true
				if strings.Contains(c.Extra, "auto_increment") {
					linked[t.Name+"."+c.Name] = true
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, t := range s.Tables {
			for _, fk := range t.FKs {
				for i := range fk.Cols {
					a, b := t.Name+"."+fk.Cols[i], fk.RefTable+"."+fk.RefCols[i]
					if ub[a] && ub[b] && linked[a] != linked[b] {
						linked[a], linked[b], changed = true, true, true
					}
				}
			}
		}
	}
	return linked
}

// StorageOverride changes the PG storage of one column (e.g. text with NUL
// bytes -> bytea); the canonical value stays the original text.
type StorageOverride struct {
	Table   string `json:"table"`
	Column  string `json:"column"`
	Storage string `json:"storage"`
	Reason  string `json:"reason"`
}

// PlanOptions are explicit, reviewed plan variants; the zero value is the
// R5-compatible plan.
type PlanOptions struct {
	// UnsignedFullDomain (repair-time1): every bigint unsigned column keeps the
	// full source storage domain numeric(20,0) CHECK 0..2^64-1 (FK pairs stay
	// numeric on both sides). AUTO_INCREMENT becomes an owned bigint sequence
	// DEFAULT (PG identities need integer types): the GENERATOR stays limited
	// to 2^63-1 (recorded pending); stored values are not narrowed.
	UnsignedFullDomain bool
}

func BuildPlan(s *Schema, views []ViewOverride, storage []StorageOverride) *Plan {
	return BuildPlanOpts(s, views, storage, PlanOptions{})
}

func BuildPlanOpts(s *Schema, views []ViewOverride, storage []StorageOverride, opts PlanOptions) *Plan {
	// pgnative-A: app.norm first, no dbmigrate collation (native schema).
	p := &Plan{Functions: append(append([]string{}, AppNormSQL...), "CREATE SCHEMA dbmigrate", helperDate, helperOnUpdate, helperOnUpdateMark), Sequences: map[string]string{}}
	ux := map[string]NativeUnique{}
	for _, u := range NativeUniques(s) {
		ux[u.Table+"."+u.SourceIndex] = u
	}
	cv := &p.Coverage
	cv.Notes, cv.Other, cv.Pending = map[string][]string{}, s.Other, map[string][]string{}
	linked := identityLinked(s)
	p.Storage = map[string]string{}
	cols := map[string]Column{}
	for _, t := range s.Tables {
		for _, c := range t.Columns {
			cols[t.Name+"."+c.Name] = c
		}
	}
	for _, so := range storage {
		key := so.Table + "." + so.Column
		c, ok := cols[key]
		k, _ := ColumnKind(c)
		if !ok || so.Storage != "bytea" || k != KText {
			cv.Unsupported = append(cv.Unsupported, "storage override "+key+": only existing text columns -> bytea")
			continue
		}
		p.Storage[key] = "bytea"
		cv.Pending[key] = append(cv.Pending[key], "storage_app_adapter: text stored as bytea ("+so.Reason+"); app must bind/scan []byte until S3 ports its callers")
	}
	bad := func(where string, err error) { cv.Unsupported = append(cv.Unsupported, where+": "+err.Error()) }
	for k, n := range s.Other {
		if n > 0 {
			bad(k, fmt.Errorf("%d objects not mapped", n))
		}
	}
	have := map[string]bool{}
	for _, v := range views {
		for col, want := range v.Requires {
			if c, ok := cols[col]; !ok || c.DataType != want {
				cv.Unsupported = append(cv.Unsupported, "view "+v.Name+": requires "+col+" "+want)
			}
		}
		have[v.Name] = true
		cv.Views = append(cv.Views, v.Name)
		p.Views = append(p.Views, v.PGSQL)
		cv.ViewProv = append(cv.ViewProv, v)
		cv.Pending["view "+v.Name] = []string{"view_activation_review: " + v.SourceState + "; " + v.Semantics}
	}
	for _, v := range s.Views {
		if !have[v.Name] {
			bad("view "+v.Name, fmt.Errorf("no reviewed override"))
		}
	}
	for _, t := range s.Tables {
		cv.Tables++
		if t.Engine != "InnoDB" {
			bad(t.Name, fmt.Errorf("engine %s", t.Engine))
		}
		var cols, tchecks, onUpd, genCols, seqOwned []string
		for _, c := range t.Columns {
			cv.Columns++
			where := t.Name + "." + c.Name
			k, err := ColumnKind(c)
			if err != nil {
				bad(where, err)
				continue
			}
			typ, checks, notes, pend, err := ColumnType(t, c, linked[where] && !opts.UnsignedFullDomain)
			if err != nil {
				bad(where, err)
				continue
			}
			if p.Storage[where] == "bytea" {
				typ, pend = "bytea", nil
				for i := range checks {
					checks[i] = strings.Replace(checks[i], "octet_length("+pq(c.Name)+")", "octet_length("+pq(c.Name)+")", 1)
				}
			}
			if len(notes) > 0 {
				cv.Notes[where] = notes
			}
			if len(pend) > 0 {
				cv.Pending[where] = pend
			}
			if k == KEnum {
				cv.Enums++
				vals, err := EnumValues(c.ColType)
				if err != nil {
					bad(where, err)
					continue
				}
				q := make([]string, len(vals))
				for i, v := range vals {
					if v == "" {
						bad(where, fmt.Errorf("empty enum value"))
					}
					q[i] = lit(v)
				}
				p.Types = append(p.Types, "CREATE TYPE "+typ+" AS ENUM ("+strings.Join(q, ", ")+")")
			}
			def := pq(c.Name) + " " + typ
			extra := strings.ToLower(c.Extra)
			for _, x := range strings.Fields(strings.NewReplacer("on update", "on_update", "default_generated", "", "auto_increment", "", "stored generated", "stored_generated").Replace(extra)) {
				if !strings.HasPrefix(x, "on_update") && !strings.HasPrefix(x, "current_timestamp") && x != "stored_generated" {
					bad(where, fmt.Errorf("unsupported column extra %q", c.Extra))
				}
			}
			switch {
			case strings.Contains(extra, "virtual generated"):
				bad(where, fmt.Errorf("VIRTUAL generated column"))
				continue
			case strings.Contains(extra, "stored generated"):
				cv.Generated++
				e, err := TranslateExpr(c.GenExpr)
				if err != nil {
					bad(where+" (generated)", err)
					continue
				}
				def += " GENERATED ALWAYS AS (" + e + ") STORED"
				genCols = append(genCols, "-"+c.Name)
			default:
				if strings.Contains(extra, "auto_increment") {
					cv.AutoInc++
					if opts.UnsignedFullDomain && c.DataType == "bigint" && strings.Contains(c.ColType, "unsigned") {
						seq := pq(PGName("seq", t.Name, c.Name))
						p.Types = append(p.Types, "CREATE SEQUENCE "+seq+" AS bigint MINVALUE 1")
						def += " DEFAULT nextval('" + strings.ReplaceAll(seq, "'", "''") + "')"
						seqOwned = append(seqOwned, "ALTER SEQUENCE "+seq+" OWNED BY "+pq(t.Name)+"."+pq(c.Name))
						cv.Pending[where] = append(cv.Pending[where], "unsigned_generator_range: stored domain numeric(20,0) 0..2^64-1 preserved; the sequence generator stops at 2^63-1 (app reads ids as int64)")
					} else {
						def += " GENERATED BY DEFAULT AS IDENTITY"
					}
					p.Sequences[t.Name] = c.Name
				} else if d, err := TranslateDefault(c, k); err != nil {
					bad(where+" (default)", err)
				} else if d != "" {
					def += " DEFAULT " + d
				}
			}
			if !c.Nullable {
				def += " NOT NULL"
			}
			if strings.Contains(extra, "on update current_timestamp") {
				cv.OnUpdate++
				kind := "naive"
				if k == KTS {
					kind = "tz"
				}
				fsp := int64(0)
				if c.DTPrec != nil {
					fsp = *c.DTPrec
				}
				if _, err := pgClockExpr(fsp); err != nil {
					bad(where+" (on update)", err)
				}
				onUpd = append(onUpd, fmt.Sprintf("%s:%s:%d", c.Name, kind, fsp))
			}
			cols = append(cols, def)
			for i, ch := range checks {
				tchecks = append(tchecks, "CONSTRAINT "+pq(PGName("rng", t.Name, c.Name, fmt.Sprint(i)))+" CHECK ("+ch+")")
			}
		}
		for _, ck := range t.Checks {
			cv.Checks++
			e, err := TranslateExpr(ck.Clause)
			if err != nil {
				bad(t.Name+" check "+ck.Name, err)
				continue
			}
			tchecks = append(tchecks, "CONSTRAINT "+pq(PGName("chk", t.Name, ck.Name))+" CHECK ("+e+")")
		}
		if pk := pkCols(t); len(pk) > 0 {
			q := make([]string, len(pk))
			for i, c := range pk {
				q[i] = pq(c)
			}
			tchecks = append(tchecks, "CONSTRAINT "+pq(PGName("pk", t.Name))+" PRIMARY KEY ("+strings.Join(q, ", ")+")")
		} else {
			cv.Notes[t.Name] = append(cv.Notes[t.Name], "no_primary_key")
		}
		p.Tables = append(p.Tables, "CREATE TABLE "+pq(t.Name)+" (\n  "+strings.Join(append(cols, tchecks...), ",\n  ")+"\n)")
		p.Tables = append(p.Tables, seqOwned...)
		for _, ix := range t.Indexes {
			if ix.Name == "PRIMARY" {
				continue
			}
			cv.Indexes++
			var parts []string
			for _, c := range ix.Cols {
				if c.SubPart != nil || c.Expr != nil || c.Name == "" {
					bad(t.Name+" index "+ix.Name, fmt.Errorf("prefix or functional index part"))
				}
				x := pq(c.Name)
				if c.Desc {
					x += " DESC"
				}
				parts = append(parts, x)
			}
			name := pq(PGName("ix", t.Name, ix.Name))
			switch ix.Type {
			case "BTREE":
				u := ""
				if ix.Unique {
					cv.Unique++
					u = "UNIQUE "
				}
				if n, ok := ux[t.Name+"."+ix.Name]; ok {
					p.Indexes = append(p.Indexes, n.DDL())
					if !n.KeepRaw { // raw unique kept only when an FK references it
						break
					}
				}
				p.Indexes = append(p.Indexes, "CREATE "+u+"INDEX "+name+" ON "+pq(t.Name)+" ("+strings.Join(parts, ", ")+")")
			case "FULLTEXT":
				cv.Fulltext++
				var docs []string
				for _, c := range ix.Cols {
					docs = append(docs, "coalesce("+pq(c.Name)+"::text, '')")
				}
				p.Indexes = append(p.Indexes, "CREATE INDEX "+name+" ON "+pq(t.Name)+" USING GIN (to_tsvector('simple'::regconfig, "+strings.Join(docs, " || ' ' || ")+"))")
				cv.Notes[t.Name+" index "+ix.Name] = []string{"fulltext->gin_tsvector_simple (no MATCH callers found)"}
			default:
				bad(t.Name+" index "+ix.Name, fmt.Errorf("index type %s", ix.Type))
			}
		}
		for _, fk := range t.FKs {
			cv.FKs++
			up, e1 := fkRule(fk.OnUpdate)
			del, e2 := fkRule(fk.OnDelete)
			if e1 != nil || e2 != nil {
				bad(t.Name+" fk "+fk.Name, fmt.Errorf("rule %s/%s", fk.OnUpdate, fk.OnDelete))
				continue
			}
			var cq, rq, notNull, join []string
			for i := range fk.Cols {
				cq = append(cq, pq(fk.Cols[i]))
				rq = append(rq, pq(fk.RefCols[i]))
				notNull = append(notNull, "c."+pq(fk.Cols[i])+" IS NOT NULL")
				join = append(join, "p."+pq(fk.RefCols[i])+" = c."+pq(fk.Cols[i]))
			}
			p.ForeignKeys = append(p.ForeignKeys, PlanFK{
				Table: t.Name, Name: fk.Name, PGName: PGName("fk", t.Name, fk.Name),
				OrphanCheck: "SELECT count(*) FROM " + pq(t.Name) + " c WHERE " + strings.Join(notNull, " AND ") +
					" AND NOT EXISTS (SELECT 1 FROM " + pq(fk.RefTable) + " p WHERE " + strings.Join(join, " AND ") + ")",
				DDL: "ALTER TABLE " + pq(t.Name) + " ADD CONSTRAINT " + pq(PGName("fk", t.Name, fk.Name)) + " FOREIGN KEY (" + strings.Join(cq, ", ") +
					") REFERENCES " + pq(fk.RefTable) + " (" + strings.Join(rq, ", ") + ") ON UPDATE " + up + " ON DELETE " + del + " NOT VALID", // pgnative-A: validated by import only when orphan-free
			})
		}
		if len(onUpd) > 0 {
			args := make([]string, 0, len(onUpd)+len(genCols))
			for _, a := range append(onUpd, genCols...) {
				args = append(args, lit(a))
			}
			cv.Pending[t.Name+" on_update"] = []string{"on_update_explicit_marked_qa: explicit assignment (incl. same value) detected by BEFORE UPDATE OF mark triggers + statement-time refresh; source-designed, manual scratch QA pending"}
			for _, u := range onUpd {
				col := strings.SplitN(u, ":", 2)[0]
				p.Triggers = append(p.Triggers, "CREATE TRIGGER "+pq(PGName("onupc", t.Name, col))+" BEFORE UPDATE OF "+pq(col)+" ON "+pq(t.Name)+
					" FOR EACH ROW EXECUTE FUNCTION dbmigrate.on_update_mark("+lit(col)+")")
			}
			p.Triggers = append(p.Triggers, "CREATE TRIGGER "+pq(PGName("onupd", t.Name))+" BEFORE UPDATE ON "+pq(t.Name)+
				" FOR EACH ROW EXECUTE FUNCTION dbmigrate.on_update_ts("+strings.Join(args, ", ")+")")
		}
	}
	sort.Strings(cv.Unsupported)
	p.Ready = len(cv.Unsupported) == 0 && len(cv.Pending) == 0
	return p
}

// SQL renders the whole plan in load order (for review; import runs the
// same statements phase by phase in one transaction).
func (p *Plan) SQL() string {
	var b strings.Builder
	w := func(h string, list []string) {
		b.WriteString("-- " + h + "\n")
		for _, s := range list {
			b.WriteString(s + ";\n")
		}
	}
	w("functions", p.Functions)
	w("enum types", p.Types)
	w("tables", p.Tables)
	b.WriteString("-- data load (dbmigrate import), then identity setval\n")
	w("indexes", p.Indexes)
	fks := make([]string, len(p.ForeignKeys))
	for i, f := range p.ForeignKeys {
		fks[i] = f.DDL
	}
	w("foreign keys (each after a zero-orphan check)", fks)
	w("on-update triggers", p.Triggers)
	w("views (reviewed overrides)", p.Views)
	return b.String()
}

// OnUpdateFunctionSources returns the exact pg_proc.prosrc text (between the
// $$ delimiters) of the ON UPDATE helper functions, for the read-only catalog
// gate (internal/db RequireOnUpdateTriggers).
func OnUpdateFunctionSources() map[string]string {
	body := func(ddl string) string {
		i := strings.Index(ddl, "$$")
		j := strings.LastIndex(ddl, "$$")
		return ddl[i+2 : j]
	}
	return map[string]string{"on_update_ts": body(helperOnUpdate), "on_update_mark": body(helperOnUpdateMark)}
}
