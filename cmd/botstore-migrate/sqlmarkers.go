package main

// selectedSQL marks SQL against the SQLite context source of this migration
// tool (identity; no SQL change). The PG side goes through botstore.
func selectedSQL(q string) string { return q }
