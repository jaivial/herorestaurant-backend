package botstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	_ "modernc.org/sqlite"
	"preactvillacarmen/internal/sqldb"
)

// Coordination id: wa_bot_store_pg_v1 - the WhatsApp bot context store
// (transcripts, pipeline decisions, management requests, sticky handoff,
// chat images, knowledge FTS index) runs on SQLite or PostgreSQL. Callers
// write SQLite-style "?" placeholders; Postgres gets them rebound to $n.

const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// DB is a dialect-aware handle. Postgres reports whether the target is PG.
type DB struct {
	*sql.DB
	Postgres bool
	// Schema is the pinned PG schema ("" = legacy separate bot DB, public).
	Schema string
}

// Open opens the store. sqlite: dsn is a file path (":memory:" allowed).
// postgres: dsn is a libpq/pgx URL. Never falls back to the other driver.
func Open(driver, dsn string) (*DB, error) { return OpenSchema(driver, dsn, "") }

// OpenSchema opens the store; for postgres a non-empty schema pins
// search_path (unified business DB: "bot_context") and creates the schema.
func OpenSchema(driver, dsn, schema string) (*DB, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", DriverSQLite:
		path := strings.TrimSpace(dsn)
		if path == "" {
			path = ":memory:"
		} else if path != ":memory:" && !strings.HasPrefix(path, "file:") {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, fmt.Errorf("create bot context dir: %w", err)
			}
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return nil, fmt.Errorf("open bot context sqlite: %w", err)
		}
		db.SetMaxOpenConns(1)
		return &DB{DB: db}, nil
	case DriverPostgres:
		if strings.TrimSpace(dsn) == "" {
			return nil, fmt.Errorf("bot context postgres: empty DSN")
		}
		var db *sql.DB
		var err error
		if schema == "" {
			// Legacy separate bot DB: same UTC session/scan pin, no search_path.
			db, err = sqldb.OpenPostgresUTC(dsn)
		} else {
			db, err = sqldb.OpenPostgres(dsn, schema)
		}
		if err != nil {
			return nil, fmt.Errorf("open bot context postgres: %w", err)
		}
		db.SetMaxOpenConns(8)
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping bot context postgres: %w", err)
		}
		if schema != "" {
			if _, err := db.Exec(pgOnlySQL("CREATE SCHEMA IF NOT EXISTS " + schema)); err != nil { // name validated by OpenPostgres
				_ = db.Close()
				return nil, fmt.Errorf("create bot context schema: %w", err)
			}
		}
		return &DB{DB: db, Postgres: true, Schema: schema}, nil
	}
	return nil, fmt.Errorf("unknown bot context driver %q", driver)
}

// Rebind turns "?" placeholders into $1..$n for Postgres through the shared
// quote-aware lexer (sqldb_rebind_v2: literals/comments/dollar quotes are
// skipped, ambiguous text fails closed). SQLite text is unchanged.
func (d *DB) Rebind(q string) string {
	if d == nil || !d.Postgres {
		return q
	}
	return sqldb.Rebind(sqldb.Postgres, q)
}

func (d *DB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, selectedSQL(d.Rebind(q)), args...)
}

func (d *DB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, selectedSQL(d.Rebind(q)), args...)
}

func (d *DB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, selectedSQL(d.Rebind(q)), args...)
}

func (d *DB) Exec(q string, args ...any) (sql.Result, error) {
	return d.ExecContext(context.Background(), selectedSQL(q), args...)
}

// InsertID runs an INSERT and returns the new id on both dialects.
func (d *DB) InsertID(ctx context.Context, q string, args ...any) (int64, error) {
	if d.Postgres {
		var id int64
		err := d.QueryRowContext(ctx, pgOnlySQL(q+" RETURNING id"), args...).Scan(&id)
		return id, err
	}
	res, err := d.ExecContext(ctx, selectedSQL(q), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Init creates the core schema (idempotent).
func (d *DB) Init() error {
	stmts := sqliteSchema
	if d.Postgres {
		stmts = postgresSchema
		if d.Schema != "" {
			// Unified DB: the 5 MySQL bot tables live in public (full business
			// import); the context schema holds only the SQLite-context tables.
			stmts = postgresSchema[:postgresContextStmts]
		}
	}
	for _, stmt := range stmts {
		if _, err := d.DB.Exec(selectedSQL(stmt)); err != nil {
			return fmt.Errorf("init bot context schema: %w", err)
		}
	}
	if !d.Postgres {
		// Forward-compatible column for databases created by early builds.
		if _, err := d.DB.Exec(selectedSQL(`ALTER TABLE conversation_messages ADD COLUMN include_in_context INTEGER NOT NULL DEFAULT 1`)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("migrate bot context sqlite: %w", err)
		}
	}
	return nil
}

// RequireMigrated fails closed on an unmigrated Postgres store: an empty PG
// at cutover would silently drop every conversation's context, sessions,
// queue and message log. Both verified imports (SQLite context and MySQL
// bot tables) must be present. A fresh install opts in with allowEmpty
// (BOT_CONTEXT_POSTGRES_ALLOW_EMPTY=1).
func (d *DB) RequireMigrated(allowEmpty bool) error {
	if d == nil || !d.Postgres || allowEmpty {
		return nil
	}
	if d.Schema != "" {
		var n int
		if err := d.DB.QueryRow(pgOnlySQL("SELECT COUNT(*) FROM bot_store_meta WHERE key = 'migration_verified'")).Scan(&n); err != nil {
			return fmt.Errorf("bot context postgres: read migration marker: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("bot context postgres: schema %s has no verified context migration", d.Schema)
		}
		return nil
	}
	var n int
	if err := d.DB.QueryRow(pgOnlySQL(`SELECT COUNT(*) FROM bot_store_meta WHERE key IN ('migration_verified', 'mysql_import_verified')`)).Scan(&n); err != nil {
		return fmt.Errorf("bot context postgres: read migration markers: %w", err)
	}
	if n != 2 {
		return fmt.Errorf("bot context postgres: %d/2 verified migration markers (run botstore-migrate copy and mysql-import, or set BOT_CONTEXT_POSTGRES_ALLOW_EMPTY=1 for a fresh install)", n)
	}
	return nil
}

var sqliteSchema = []string{
	`PRAGMA busy_timeout = 5000`,
	`PRAGMA journal_mode = WAL`,
	`CREATE TABLE IF NOT EXISTS conversation_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		restaurant_id INTEGER NOT NULL,
		user_phone TEXT NOT NULL,
		role TEXT NOT NULL CHECK(role IN ('user','assistant')),
		content TEXT NOT NULL,
		tool_name TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		include_in_context INTEGER NOT NULL DEFAULT 1,
		created_at_ms INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_conversation_messages_thread ON conversation_messages(restaurant_id, user_phone, id)`,
	`CREATE TABLE IF NOT EXISTS pipeline_decisions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		restaurant_id INTEGER NOT NULL,
		user_phone TEXT NOT NULL,
		message TEXT NOT NULL,
		decision_json TEXT NOT NULL,
		model_used TEXT NOT NULL DEFAULT '',
		created_at_ms INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_pipeline_decisions_restaurant ON pipeline_decisions(restaurant_id, id)`,
	`CREATE TABLE IF NOT EXISTS management_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		restaurant_id INTEGER NOT NULL,
		user_phone TEXT NOT NULL,
		reason TEXT NOT NULL,
		summary TEXT NOT NULL,
		group_sent INTEGER NOT NULL DEFAULT 0,
		created_at_ms INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_management_requests_thread ON management_requests(restaurant_id, user_phone, id)`,
	`CREATE TABLE IF NOT EXISTS handoff_state (
		restaurant_id INTEGER NOT NULL,
		user_phone TEXT NOT NULL,
		topic TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		repeats INTEGER NOT NULL DEFAULT 0,
		updated_at_ms INTEGER NOT NULL,
		PRIMARY KEY (restaurant_id, user_phone))`,
	`CREATE TABLE IF NOT EXISTS chat_images (
		restaurant_id INTEGER NOT NULL,
		chat_jid TEXT NOT NULL,
		message_id TEXT NOT NULL,
		mime TEXT NOT NULL DEFAULT '',
		at_ms INTEGER NOT NULL,
		reading_source TEXT,
		reading_text TEXT,
		PRIMARY KEY (restaurant_id, chat_jid, message_id))`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS bot_knowledge_fts USING fts5(
		restaurant_id UNINDEXED, chunk_id UNINDEXED, tags, title, body,
		tokenize = 'unicode61 remove_diacritics 2')`,
}

// postgresSchema mirrors sqliteSchema column for column (same names, TEXT for
// text, BIGINT for SQLite INTEGER) so row digests compare 1:1. Ids are
// identity columns whose sequences are advanced after an id-preserving copy.
// postgresContextStmts: the first N statements are the SQLite-context schema;
// the rest is the superseded MySQL-subset (legacy separate bot DB only).
var postgresContextStmts = func() int {
	for i, s := range postgresSchema {
		if strings.Contains(s, "bot_inbound_queue (") {
			return i
		}
	}
	panic("botstore: mysql-subset ddl not found")
}()

var postgresSchema = []string{
	`CREATE TABLE IF NOT EXISTS bot_store_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at_ms BIGINT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS conversation_messages (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		restaurant_id BIGINT NOT NULL,
		user_phone TEXT NOT NULL,
		role TEXT NOT NULL CHECK(role IN ('user','assistant')),
		content TEXT NOT NULL,
		tool_name TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		include_in_context BIGINT NOT NULL DEFAULT 1,
		created_at_ms BIGINT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_conversation_messages_thread ON conversation_messages(restaurant_id, user_phone, id)`,
	`CREATE TABLE IF NOT EXISTS pipeline_decisions (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		restaurant_id BIGINT NOT NULL,
		user_phone TEXT NOT NULL,
		message TEXT NOT NULL,
		decision_json TEXT NOT NULL,
		model_used TEXT NOT NULL DEFAULT '',
		created_at_ms BIGINT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_pipeline_decisions_restaurant ON pipeline_decisions(restaurant_id, id)`,
	`CREATE TABLE IF NOT EXISTS management_requests (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		restaurant_id BIGINT NOT NULL,
		user_phone TEXT NOT NULL,
		reason TEXT NOT NULL,
		summary TEXT NOT NULL,
		group_sent BIGINT NOT NULL DEFAULT 0,
		created_at_ms BIGINT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_management_requests_thread ON management_requests(restaurant_id, user_phone, id)`,
	`CREATE TABLE IF NOT EXISTS handoff_state (
		restaurant_id BIGINT NOT NULL,
		user_phone TEXT NOT NULL,
		topic TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		repeats BIGINT NOT NULL DEFAULT 0,
		updated_at_ms BIGINT NOT NULL,
		PRIMARY KEY (restaurant_id, user_phone))`,
	`CREATE TABLE IF NOT EXISTS chat_images (
		restaurant_id BIGINT NOT NULL,
		chat_jid TEXT NOT NULL,
		message_id TEXT NOT NULL,
		mime TEXT NOT NULL DEFAULT '',
		at_ms BIGINT NOT NULL,
		reading_source TEXT,
		reading_text TEXT,
		PRIMARY KEY (restaurant_id, chat_jid, message_id))`,
	// Knowledge index: FTS5 equivalent. Text is diacritic-folded in Go
	// (unicode61 remove_diacritics) and indexed with the 'simple' config.
	`CREATE TABLE IF NOT EXISTS bot_knowledge_fts (
		restaurant_id BIGINT NOT NULL,
		chunk_id TEXT NOT NULL,
		ord BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		tags TEXT NOT NULL,
		title TEXT NOT NULL,
		body TEXT NOT NULL,
		tags_tsv TSVECTOR NOT NULL,
		doc_tsv TSVECTOR NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_bot_knowledge_fts_tags ON bot_knowledge_fts USING GIN (tags_tsv)`,
	`CREATE INDEX IF NOT EXISTS idx_bot_knowledge_fts_doc ON bot_knowledge_fts USING GIN (doc_tsv)`,
	`CREATE INDEX IF NOT EXISTS idx_bot_knowledge_fts_rid ON bot_knowledge_fts (restaurant_id)`,
	// MySQL bot-owned tables moved to PG (wa_bot_store_pg_mysql_v1). Column
	// names/types mirror MySQL (datetime(3) -> timestamp(3), timestamp ->
	// timestamp(0), decimal -> numeric, enum -> text + CHECK) so row digests
	// compare 1:1 with the snapshot. Wall-clock values are stored as-is.
	`CREATE TABLE IF NOT EXISTS bot_inbound_queue (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		restaurant_id BIGINT NOT NULL,
		chat_key TEXT NOT NULL,
		message_id TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP(0) NOT NULL DEFAULT LOCALTIMESTAMP(0),
		started_at TIMESTAMP(0) NULL,
		quarantined BOOLEAN NOT NULL DEFAULT FALSE,
		UNIQUE (restaurant_id, message_id))`,
	`CREATE INDEX IF NOT EXISTS idx_inbound_state ON bot_inbound_queue (state, created_at)`,
	`CREATE TABLE IF NOT EXISTS whatsapp_bot_sessions (
		restaurant_id BIGINT NOT NULL,
		user_phone TEXT NOT NULL,
		push_name TEXT NOT NULL DEFAULT '',
		last_message_at TIMESTAMP(3) NOT NULL,
		created_at TIMESTAMP(0) NULL DEFAULT LOCALTIMESTAMP(0),
		bot_paused_until TIMESTAMP(3) NULL,
		PRIMARY KEY (restaurant_id, user_phone))`,
	`CREATE INDEX IF NOT EXISTS idx_wa_sessions_last ON whatsapp_bot_sessions (last_message_at)`,
	`CREATE TABLE IF NOT EXISTS whatsapp_message_logs (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		restaurant_id BIGINT NOT NULL,
		direction TEXT NOT NULL CHECK (direction IN ('inbound','outbound')),
		message_type TEXT NOT NULL DEFAULT 'text',
		user_phone TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'sent' CHECK (status IN ('received','sent','failed')),
		tool_name TEXT NOT NULL DEFAULT '',
		provider_message_id TEXT NOT NULL DEFAULT '',
		ai_model TEXT NOT NULL DEFAULT '',
		input_tokens BIGINT NOT NULL DEFAULT 0,
		output_tokens BIGINT NOT NULL DEFAULT 0,
		cost_usd NUMERIC(14,8) NOT NULL DEFAULT 0,
		correlation_id TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP(3) NOT NULL DEFAULT LOCALTIMESTAMP(3))`,
	`CREATE INDEX IF NOT EXISTS idx_wa_logs_rid_created ON whatsapp_message_logs (restaurant_id, created_at)`,
	// Legacy transcripts, archived (no runtime reader or writer).
	`CREATE TABLE IF NOT EXISTS bot_conversation_messages (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		phone_number TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		"timestamp" TIMESTAMP(0) NOT NULL,
		message_id TEXT NULL,
		from_name TEXT NULL,
		restaurant_id BIGINT NOT NULL DEFAULT 1)`,
	`CREATE TABLE IF NOT EXISTS whatsapp_bot_messages (
		id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		restaurant_id BIGINT NOT NULL,
		user_phone TEXT NOT NULL,
		role TEXT NOT NULL CHECK (role IN ('user','assistant','tool')),
		content TEXT NOT NULL,
		tool_name TEXT NULL,
		created_at TIMESTAMP(3) NOT NULL)`,
}

// ftsFolder mirrors unicode61 remove_diacritics for Spanish/English text.
var ftsFolder = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ñ", "n", "ç", "c")

// FTSFold lowercases and folds diacritics before PG indexing/querying.
func FTSFold(s string) string { return ftsFolder.Replace(strings.ToLower(s)) }
