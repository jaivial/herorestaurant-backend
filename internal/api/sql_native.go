package api

import (
	"strings"

	"preactvillacarmen/internal/sqldb"
)

// Coordination id: sqldb_native_pg_v1 - explicit per-site SQL, no transpiler.
//
// sqlDialect is set once by NewServer (MySQL until the local cutover; the
// server refuses DB_DRIVER=postgres while domains remain unported).
var sqlDialect = sqldb.MySQL

// SetSQLDialect selects the business SQL dialect once at startup
// (db_dialect_selection_v1); the server still refuses Postgres.
func SetSQLDialect(d sqldb.Dialect) { sqlDialect = d }

// nativeSQL picks the audited statement written for each dialect. Used where
// semantics differ (upsert conflict targets, NOW() on naive datetime columns,
// INSERT IGNORE, JSON, date functions...). Placeholders: "?" in both; the PG
// text is rebound by sqldb.Rebind (audited: no literal "?" or jsonb ?-ops).
func nativeSQL(mysqlQ, pgQ string) string {
	if sqlDialect == sqldb.Postgres {
		return sqldb.Rebind(sqldb.Postgres, pgQ)
	}
	return mysqlQ
}

// portableSQL marks a statement audited as valid and equivalent on both
// dialects (plain SELECT/INSERT/UPDATE/DELETE, LIMIT, CASE, COALESCE, 0/1
// smallint flags, no MySQL-only function). Only placeholders change on PG.
func portableSQL(q string) string {
	return sqldb.Rebind(sqlDialect, q)
}

// pgChangedRowsPredicate reproduces MySQL's CHANGED-rows RowsAffected for a
// composed "UPDATE ... SET col = ?, col2 = NULL ..." on PostgreSQL (which
// counts matched rows). Each set item must be exactly "col = ?" or
// "col = NULL" (col optionally double-quoted); pgTypes gives the PG column
// type of every column that may appear. It returns " AND ROW(cols) IS
// DISTINCT FROM ROW(values)" plus the duplicated SET args to append AFTER the
// WHERE args. Text-like types compare with COLLATE "C" (MySQL compares the
// stored record bytes). An unknown column/shape is a programming error and
// returns ok=false so the caller fails the request instead of silently
// changing the RowsAffected contract. Only used on the PG branch.
func pgChangedRowsPredicate(sets []string, setArgs []any, pgTypes map[string]string) (string, []any, bool) {
	cols, vals := make([]string, 0, len(sets)), make([]string, 0, len(sets))
	extra := make([]any, 0, len(setArgs))
	ai := 0
	for _, item := range sets {
		parts := strings.SplitN(item, " = ", 2)
		if len(parts) != 2 {
			return "", nil, false
		}
		col := strings.TrimSpace(parts[0])
		typ, ok := pgTypes[strings.Trim(col, `"`)]
		if !ok {
			return "", nil, false
		}
		left := col
		if typ == "text" {
			left = col + ` COLLATE "C"`
		}
		switch strings.TrimSpace(parts[1]) {
		case "?":
			if ai >= len(setArgs) {
				return "", nil, false
			}
			vals = append(vals, "CAST(? AS "+typ+")")
			extra = append(extra, setArgs[ai])
			ai++
		case "NULL":
			vals = append(vals, "CAST(NULL AS "+typ+")")
		default:
			return "", nil, false
		}
		cols = append(cols, left)
	}
	if ai != len(setArgs) || len(cols) == 0 {
		return "", nil, false
	}
	return " AND ROW(" + strings.Join(cols, ", ") + ") IS DISTINCT FROM ROW(" + strings.Join(vals, ", ") + ")", extra, true
}

// sqlFragment picks an audited per-dialect fragment WITHOUT rebinding "?":
// the composed statement is rebound once by portableSQL at the call site, so
// placeholder numbering stays global across fragments.
func sqlFragment(mysqlQ, pgQ string) string {
	if sqlDialect == sqldb.Postgres {
		return pgQ
	}
	return mysqlQ
}

// selectedSQL marks a query value already produced by nativeSQL/portableSQL
// (e.g. stored in a seed table) at the call site; it changes nothing.
func selectedSQL(q string) string { return q }

// pgOnlySQL is a statement that only runs on the PostgreSQL branch of an
// explicit "if sqlDialect == sqldb.Postgres" (e.g. a two-step renumbering that
// replaces MySQL UPDATE ... ORDER BY). Reaching it on MySQL is a bug.
// mysqlOnlySQL is the MySQL side of such an explicit dialect branch.
func mysqlOnlySQL(q string) string {
	if sqlDialect != sqldb.MySQL {
		panic("mysqlOnlySQL used outside the MySQL branch")
	}
	return q
}

// blockedOnPGSQL marks a MySQL statement that has NO source-faithful PG port
// yet (unresolved semantics: multi-key upsert, SQL UUID v1...). The caller
// must skip it on PG explicitly; the inventory counts it as BLOCKED, never as
// native. Reaching it on PG is a bug.
func blockedOnPGSQL(q string) string {
	if sqlDialect != sqldb.MySQL {
		panic("blockedOnPGSQL reached on a non-MySQL dialect")
	}
	return q
}

// provisionalSQL / pgProvisionalSQL behave exactly like nativeSQL / pgOnlySQL
// but mark a PG variant that is syntax-ported and still an OPEN cutover gate
// (not source-equivalent); the inventory lists them separately.
func provisionalSQL(mysqlQ, pgQ string) string { return nativeSQL(mysqlQ, pgQ) }

func pgProvisionalSQL(q string) string { return pgOnlySQL(q) }

func pgOnlySQL(q string) string {
	if sqlDialect != sqldb.Postgres {
		panic("pgOnlySQL used outside the PostgreSQL branch")
	}
	return sqldb.Rebind(sqldb.Postgres, q)
}

// mysqlUpperEnumArg (repair-timeqa1) is the stored label MySQL produces for an
// ENUM whose labels are all upper-case ASCII: input is matched under the
// column collation (ci, PAD SPACE: trailing spaces ignored) and stored as the
// canonical label. PG native enums accept only the exact label, so the value is
// canonicalized before binding on both dialects (identical MySQL result).
// Leading spaces are NOT removed (MySQL rejects them too); a non-matching value
// still fails on both engines. Only for all-upper-case ASCII label sets.
func mysqlUpperEnumArg(v string) string {
	return strings.ToUpper(strings.TrimRight(v, " "))
}

// mysqlLowerEnumArg: as mysqlUpperEnumArg for ENUMs whose labels are all
// lower-case ASCII (repair-enums2). Leading spaces kept (MySQL rejects them).
func mysqlLowerEnumArg(v string) string {
	return strings.ToLower(strings.TrimRight(v, " "))
}

// mysqlLowerEnumPtr keeps NULL (nil) as NULL.
func mysqlLowerEnumPtr(v *string) any {
	if v == nil {
		return nil
	}
	return mysqlLowerEnumArg(*v)
}
