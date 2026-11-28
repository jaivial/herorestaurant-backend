package main

// mysqlOnlySQL marks SQL of a MySQL-bound tool or bootstrap path (identity; no
// SQL change). This package opens the MySQL business DB only; it has no PG
// twin and is not part of the PostgreSQL runtime (port/retire before running
// it against PG after cutover).
func mysqlOnlySQL(q string) string { return q }
