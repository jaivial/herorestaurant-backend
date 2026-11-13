package main

import (
	"strings"

	"preactvillacarmen/internal/sqldb"
)

// dialect selects the explicit SQL twins; set from DB_DRIVER at start
// (db_dialect_selection_v1). On PG the registry uses schema "public" and
// --apply is refused until QA.
var dialect = sqldb.MySQL

func nativeSQL(mysqlQ, pgQ string) string {
	if dialect == sqldb.Postgres {
		return sqldb.Rebind(sqldb.Postgres, pgQ)
	}
	return mysqlQ
}

func portableSQL(q string) string { return sqldb.Rebind(dialect, q) }

// sqlFragment picks a twin without rebinding (the caller rebinds once).
func sqlFragment(mysqlQ, pgQ string) string {
	if dialect == sqldb.Postgres {
		return pgQ
	}
	return mysqlQ
}

// padSpaceNullIfArg: MySQL NULLIF(?,”) treats a space-only value as ” (PAD
// SPACE); the PG twin compares exactly, so bind ” for it.
func padSpaceNullIfArg(v string) string {
	if dialect == sqldb.Postgres && strings.TrimRight(v, " ") == "" {
		return ""
	}
	return v
}
