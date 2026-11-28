package main

import "preactvillacarmen/internal/sqldb"

// dialect selects the explicit SQL twins; set from DB_DRIVER at start
// (db_dialect_selection_v1). PG starts only through db.OpenBusinessForTool
// (DB_PG_TOOLS_QA_APPROVED=1 + DATABASE_URL + import marker) and is
// report-only until runtime QA. utility_port_v1.
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
