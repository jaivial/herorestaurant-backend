package botstore

// Inventory markers (identity functions; they change no SQL). The canonical
// SQL inventory recognises these names at db call sites:
//
// selectedSQL: the text is already dialect-selected for this handle - either
// chosen by the caller (wrapper methods, per-dialect schema lists, insertSQL)
// or the SQLite half of an explicit Postgres/SQLite branch
// (sqlite_master/sqlite_sequence/rowid statements). Never runs on the other
// engine.
//
// pgOnlySQL: PostgreSQL-native text reached only on a Postgres handle (PG
// branch of an explicit check, or the MySQL->PG importer whose target is
// always PG). Placeholders are already $n.
func selectedSQL(q string) string { return q }

func pgOnlySQL(q string) string { return q }
