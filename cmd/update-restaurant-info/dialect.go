package main

import "preactvillacarmen/internal/sqldb"

// dialect selects the explicit SQL twins; set from DB_DRIVER at start
// (db_dialect_selection_v1). PG starts only through db.OpenBusinessForTool
// (DB_PG_TOOLS_QA_APPROVED=1 + DATABASE_URL + import marker, UTC, no MySQL
// migrations, no fallback) and is report-only until runtime QA. utility_port_v2.
var dialect = sqldb.MySQL

func nativeSQL(mysqlQ, pgQ string) string {
	if dialect == sqldb.Postgres {
		return sqldb.Rebind(sqldb.Postgres, pgQ)
	}
	return mysqlQ
}

func portableSQL(q string) string { return sqldb.Rebind(dialect, q) }
