package analytics

import "preactvillacarmen/internal/sqldb"

// Coordination id: sqldb_native_pg_v1 - explicit per-site SQL, no transpiler.
// The analytics package keeps its own dialect switch (MySQL until the local
// cutover); the cutover must call SetSQLDialect together with the api one.
var sqlDialect = sqldb.MySQL

// SetSQLDialect selects the dialect of every analytics statement.
func SetSQLDialect(d sqldb.Dialect) { sqlDialect = d }

// nativeSQL picks the audited per-dialect statement ("?" placeholders, the PG
// text rebound once); portableSQL marks SQL audited identical on both.
func nativeSQL(mysqlQ, pgQ string) string {
	if sqlDialect == sqldb.Postgres {
		return sqldb.Rebind(sqldb.Postgres, pgQ)
	}
	return mysqlQ
}

func portableSQL(q string) string { return sqldb.Rebind(sqlDialect, q) }
