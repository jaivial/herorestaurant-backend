package dbmigrate

// Inventory markers (identity; no SQL change). This package has fixed engine
// roles: export.go/schema.go read the MySQL source (mysqlOnlySQL) and
// pgimport.go writes/verifies the PostgreSQL target (pgOnlySQL). Neither text
// ever runs on the other engine.
func mysqlOnlySQL(q string) string { return q }

func pgOnlySQL(q string) string { return q }
