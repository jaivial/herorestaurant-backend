package dbmigrate

import (
	"strings"
)

// NativeInventoryTSV: one row per generated unique expression index
// (docs/pgnative/native-ddl-inventory.tsv).
func NativeInventoryTSV(s *Schema) string {
	var b strings.Builder
	b.WriteString("table\tmysql_unique_index\tcolumns\ttext_columns\tnorm_functions\tpg_index\tddl\ton_conflict_target\traw_unique_kept_for_fk\n")
	for _, u := range NativeUniques(s) {
		keep := "no"
		if u.KeepRaw {
			keep = "yes:" + u.RawName
		}
		b.WriteString(strings.Join([]string{u.Table, u.SourceIndex, strings.Join(u.Cols, ","), strings.Join(u.TextCols, ","), strings.Join(u.Funcs, ","), u.PGName, u.DDL(), "ON CONFLICT " + u.ConflictTarget(), keep}, "\t") + "\n")
	}
	return b.String()
}

// doBlock wraps a catalog loop in an anonymous plpgsql block.
func doBlock(body string) string { return "DO $pgn$ DECLARE r record; BEGIN\n" + body + "\nEND $pgn$" }

// ConvertScratchSQL converts a scratch imported the OLD way (ci_ai, __wk,
// __pad, ixwk/ixpad, shadow FKs) to the native schema of p. Idempotent; ends
// in ROLLBACK (the coordinator switches it to COMMIT on the scratch only).
func ConvertScratchSQL(p *Plan, s *Schema) string {
	var st []string
	add := func(x ...string) { st = append(st, x...) }
	add(AppNormSQL...)
	add("CREATE TEMP TABLE IF NOT EXISTS _pgn_views ON COMMIT DROP AS SELECT c.relname::text AS relname, pg_get_viewdef(c.oid) AS def FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind = 'v'")
	add(doBlock("FOR r IN SELECT relname FROM _pgn_views LOOP EXECUTE format('DROP VIEW IF EXISTS public.%I', r.relname); END LOOP;"))
	// shadow FKs first, then UNIQUE/other constraints on __wk/__pad columns
	add(doBlock(`FOR r IN SELECT DISTINCT con.conrelid::regclass::text AS t, con.conname, con.contype FROM pg_constraint con
  JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = ANY (con.conkey)
  WHERE con.connamespace = 'public'::regnamespace AND (a.attname LIKE '%__wk' OR a.attname LIKE '%__pad')
  ORDER BY (con.contype = 'f') DESC, 1, 2
LOOP EXECUTE format('ALTER TABLE %s DROP CONSTRAINT IF EXISTS %I', r.t, r.conname); END LOOP;`))
	add(doBlock(`FOR r IN SELECT c.oid::regclass::text AS i FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public' AND c.relkind = 'i' AND (c.relname LIKE 'ixwk__%' OR c.relname LIKE 'ixpad__%')
LOOP EXECUTE format('DROP INDEX IF EXISTS %s', r.i); END LOOP;`))
	add(doBlock(`FOR r IN SELECT a.attrelid::regclass::text AS t, a.attname FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public' AND c.relkind = 'r' AND a.attnum > 0 AND NOT a.attisdropped AND (a.attname LIKE '%__wk' OR a.attname LIKE '%__pad')
  ORDER BY (a.attgenerated <> '') DESC, 1, 2
LOOP EXECUTE format('ALTER TABLE %s DROP COLUMN IF EXISTS %I', r.t, r.attname); END LOOP;`))
	// helper functions as planned (on_update_ts no longer knows __wk)
	for _, f := range []string{helperOnUpdate, helperOnUpdateMark} {
		add(strings.Replace(f, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1))
	}
	for _, u := range NativeUniques(s) {
		if !u.KeepRaw {
			add("DROP INDEX IF EXISTS " + pq(u.RawName))
		}
	}
	add(doBlock(`FOR r IN SELECT c.oid::regclass::text AS i FROM pg_index x JOIN pg_class c ON c.oid = x.indexrelid JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public' AND pg_get_indexdef(x.indexrelid) LIKE '%dbmigrate.ci_ai%'
LOOP RAISE EXCEPTION 'pgnative convert: index % still uses dbmigrate.ci_ai in an expression (unplanned object)', r.i; END LOOP;`))
	// pgnative-B: STORED generated columns on tables with dbmigrate-collated
	// columns block ALTER ... TYPE (e.g. pos_shifts.open_terminal_key): save,
	// drop, and re-add them after the collation change (values recomputed;
	// the column moves to the end of the table).
	add("CREATE TEMP TABLE IF NOT EXISTS _pgn_gen ON COMMIT DROP AS SELECT a.attrelid::regclass::text AS t, a.attname, format_type(a.atttypid, a.atttypmod) AS typ, pg_get_expr(d.adbin, d.adrelid) AS expr, a.attnotnull AS nn, a.attnum FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind = 'r' AND a.attgenerated = 's' AND NOT a.attisdropped AND EXISTS (SELECT 1 FROM pg_attribute b JOIN pg_collation co ON co.oid = b.attcollation JOIN pg_namespace cn ON cn.oid = co.collnamespace WHERE b.attrelid = a.attrelid AND cn.nspname = 'dbmigrate' AND b.attnum > 0 AND NOT b.attisdropped)")
	add(doBlock("FOR r IN SELECT t, attname FROM _pgn_gen ORDER BY t, attnum LOOP EXECUTE format('ALTER TABLE %s DROP COLUMN %I', r.t, r.attname); END LOOP;"))
	add(doBlock(`FOR r IN SELECT a.attrelid::regclass::text AS t, a.attname, format_type(a.atttypid, a.atttypmod) AS typ
  FROM pg_attribute a JOIN pg_collation co ON co.oid = a.attcollation JOIN pg_namespace cn ON cn.oid = co.collnamespace
  JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE cn.nspname = 'dbmigrate' AND n.nspname = 'public' AND c.relkind = 'r' AND a.attnum > 0 AND NOT a.attisdropped
  ORDER BY 1, 2
LOOP EXECUTE format('ALTER TABLE %s ALTER COLUMN %I TYPE %s COLLATE "default"', r.t, r.attname, r.typ); END LOOP;`))
	add(doBlock("FOR r IN SELECT t, attname, typ, expr, nn FROM _pgn_gen ORDER BY t, attnum LOOP EXECUTE format('ALTER TABLE %s ADD COLUMN %I %s GENERATED ALWAYS AS (%s) STORED', r.t, r.attname, r.typ, r.expr); IF r.nn THEN EXECUTE format('ALTER TABLE %s ALTER COLUMN %I SET NOT NULL', r.t, r.attname); END IF; END LOOP;"))
	for _, u := range NativeUniques(s) {
		add(strings.Replace(u.DDL(), "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX IF NOT EXISTS ", 1))
	}
	for _, f := range p.ForeignKeys {
		add(doBlock("IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = " + lit(f.PGName) + " AND conrelid = " + lit(pq(f.Table)) + "::regclass) THEN\n  EXECUTE " + lit(f.DDL) + ";\nEND IF;"))
	}
	add(doBlock("FOR r IN SELECT relname, def FROM _pgn_views LOOP EXECUTE format('CREATE VIEW public.%I AS %s', r.relname, r.def); END LOOP;"))
	add("DROP COLLATION IF EXISTS dbmigrate.ci_ai")
	var b strings.Builder
	b.WriteString("-- pgnative-A: convert an OLD-way scratch (padqa2) to the native schema. Generated by dbmigrate -mode native-convert.\n-- Idempotent. NOT executed by the worker. Ends in ROLLBACK: switch to COMMIT on the scratch only.\nBEGIN;\n")
	for _, x := range st {
		b.WriteString(x + ";\n")
	}
	b.WriteString("ROLLBACK;\n")
	return b.String()
}
