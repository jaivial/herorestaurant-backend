// Package sqldb is the dialect foundation of the native PostgreSQL port
// (coordination id: sqldb_native_pg_v1). It is NOT a SQL transpiler: callers
// write native SQL per dialect; this package only provides explicit dialect
// identity, placeholder rebinding, insert-id, error classification, advisory
// locks and the schema-pinned PG pool (business "public" vs "bot_context").
package sqldb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

type Dialect string

const (
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
)

// SchemaBusiness / SchemaBotContext are the two PG schemas of the unified DB.
const (
	SchemaBusiness   = "public"
	SchemaBotContext = "bot_context"
)

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// OpenPostgres opens a pgx pool whose every connection has search_path
// pinned to schema (no fallback to other schemas, so unqualified names of
// the two domains never collide: public.conversation_messages vs
// bot_context.conversation_messages).
func OpenPostgres(dsn, schema string) (*sql.DB, error) {
	if !identRe.MatchString(schema) {
		return nil, fmt.Errorf("invalid postgres schema name")
	}
	return openPostgresUTC(dsn, schema)
}

// OpenPostgresUTC is OpenPostgres without pinning search_path (legacy
// separate bot DB): same session TimeZone=UTC and timestamptz ScanLocation=UTC.
func OpenPostgresUTC(dsn string) (*sql.DB, error) { return openPostgresUTC(dsn, "") }

// OpenPostgresReadOnly is OpenPostgres with default_transaction_read_only=on
// pinned on every session (manual diagnostics: any write fails in PG).
func OpenPostgresReadOnly(dsn, schema string) (*sql.DB, error) {
	if !identRe.MatchString(schema) {
		return nil, fmt.Errorf("invalid postgres schema name")
	}
	return openPostgresOpts(dsn, schema, true)
}

func openPostgresUTC(dsn, schema string) (*sql.DB, error) {
	return openPostgresOpts(dsn, schema, false)
}

func openPostgresOpts(dsn, schema string, readOnly bool) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err) // pgx errors never echo the password
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	if schema != "" {
		cfg.RuntimeParams["search_path"] = schema
	}
	// Source MySQL runs session time_zone=UTC and the go-sql-driver DSN has no
	// loc (UTC): pin the PG session TimeZone to UTC (to_char/::date/::text of
	// timestamptz, now()-based expressions) independent of the server default.
	cfg.RuntimeParams["timezone"] = "UTC"
	// RebindPostgres lexes literals assuming standard strings; pin it (repair-ignore1).
	cfg.RuntimeParams["standard_conforming_strings"] = "on"
	if readOnly {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	// pgx v5.11 decodes timestamptz into time.Local by default
	// (pgtype/timestamptz.go: "loc := time.Local" unless ScanLocation is set);
	// the process runs Europe/Madrid, MySQL returned UTC. Register the codec
	// with ScanLocation=UTC on every connection (instant unchanged, only the
	// time.Location - and hence JSON/Format rendering - matches the source).
	return stdlib.OpenDB(*cfg, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC}})
		return nil
	})), nil
}

// Rebind turns "?" placeholders into $n for Postgres (MySQL text is returned
// unchanged). It uses the quote-aware RebindPostgres lexer; text the lexer
// refuses is NOT sent with guessed placeholders: it is returned as a
// statement that fails at parse time ("sqldb_rebind_refused ...") and the
// reason is logged, so a misbinding can never execute silently.
func Rebind(d Dialect, q string) string {
	if d != Postgres || !strings.Contains(q, "?") {
		return q
	}
	out, _, err := RebindPostgres(q)
	if err != nil {
		log.Printf("[sqldb_rebind_v2] refused: %v", err)
		return "sqldb_rebind_refused /* " + strings.ReplaceAll(err.Error(), "*/", "* /") + " */"
	}
	return out
}

// Execer is satisfied by *sql.DB, *sql.Tx and *sql.Conn.
type Execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// InsertID runs a native INSERT and returns the generated id: RETURNING on
// PG (pgx has no LastInsertId), LastInsertId on MySQL. q must be dialect-
// native, without RETURNING.
func InsertID(ctx context.Context, d Dialect, ex Execer, q, idCol string, args ...any) (int64, error) {
	if d == Postgres {
		var id int64
		err := ex.QueryRowContext(ctx, q+" RETURNING "+idCol, args...).Scan(&id)
		return id, err
	}
	res, err := ex.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertSelectID runs a native INSERT ... SELECT that may insert 0 or 1 row and
// returns (id, rowsInserted). PG: RETURNING, no row -> (0, 0, nil); MySQL:
// RowsAffected + LastInsertId. q must be dialect-native, without RETURNING.
func InsertSelectID(ctx context.Context, d Dialect, ex Execer, q, idCol string, args ...any) (int64, int64, error) {
	if d == Postgres {
		var id int64
		err := ex.QueryRowContext(ctx, q+" RETURNING "+idCol, args...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, nil
		}
		if err != nil {
			return 0, 0, err
		}
		return id, 1, nil
	}
	res, err := ex.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, 0, err
	}
	n, _ := res.RowsAffected()
	id, _ := res.LastInsertId()
	return id, n, nil
}

// IsUndefinedObject: missing table/column (PG 42P01/42703, MySQL 1146/1054).
func IsUndefinedObject(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "42P01" || pg.Code == "42703"
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1146 || my.Number == 1054
	}
	return false
}

// IsUniqueViolationOn reports a duplicate on ONE named unique key: MySQL 1062
// whose message names mysqlKey ("for key 'table.key'"), PG 23505 whose
// ConstraintName is the native ux__ index of mysqlKey on that table or one of
// pgConstraints (plan index names). Other errors are not matched.
func IsUniqueViolationOn(err error, mysqlKey string, pgConstraints ...string) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code != "23505" {
			return false
		}
		if pg.ConstraintName == NativeUniqueName(pg.TableName, mysqlKey) {
			return true // pgnative-B: ux__<table>__<mysql key> (app.norm expression index)
		}
		for _, c := range pgConstraints {
			if pg.ConstraintName == c {
				return true
			}
		}
		return false
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1062 && (strings.Contains(my.Message, "'"+mysqlKey+"'") || strings.Contains(my.Message, "."+mysqlKey+"'"))
	}
	return false
}

// IsForeignKeyViolation: PG 23503 (foreign_key_violation), MySQL 1451/1452.
func IsForeignKeyViolation(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23503"
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1451 || my.Number == 1452
	}
	return false
}

// IsUniqueViolation: PG 23505, MySQL 1062.
func IsUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23505"
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1062
	}
	return false
}

// TxAdvisoryLock takes a transaction-scoped lock (released at commit or
// rollback) - the PG replacement of GET_LOCK, which needs a pinned session.
func TxAdvisoryLock(ctx context.Context, tx *sql.Tx, key int64) error {
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", key)
	return err
}

// TxStatementScope runs fn inside a SAVEPOINT on PG so that a failing
// statement leaves the transaction usable, as MySQL does (InnoDB rolls back
// only the failed statement; PG would abort the whole transaction). Use it
// only where the source deliberately continues the transaction after a
// statement error. MySQL: fn runs unchanged.
// The savepoint is ALWAYS released (ROLLBACK TO keeps it alive), so repeated
// or same-name nested scopes never accumulate; fn's error is returned first,
// joined with any cleanup error.
func TxStatementScope(ctx context.Context, d Dialect, tx *sql.Tx, fn func() error) error {
	if d != Postgres {
		return fn()
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT sqldb_stmt"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT sqldb_stmt"); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		if _, relErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT sqldb_stmt"); relErr != nil {
			return errors.Join(err, relErr)
		}
		return err
	}
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT sqldb_stmt")
	return err
}

// NaiveUTCArg binds a time.Time for a naive DATETIME/timestamp column exactly
// as the MySQL driver (loc=UTC) does: the UTC wall clock of the instant. pgx
// would otherwise discard the zone and store the caller's local wall clock.
func NaiveUTCArg(d Dialect, t time.Time) any {
	if d == Postgres {
		return t.UTC()
	}
	return t
}

// NaiveUTCNullArg is NaiveUTCArg for sql.NullTime (invalid stays NULL; the
// MySQL driver path keeps the original value untouched).
func NaiveUTCNullArg(d Dialect, t sql.NullTime) any {
	if d != Postgres {
		return t
	}
	if !t.Valid {
		return nil
	}
	return t.Time.UTC()
}

// TryNamedLock takes a non-blocking named session lock on ONE pinned
// connection and returns its release. MySQL GET_LOCK / PG
// pg_try_advisory_lock are session-scoped: taking and releasing them through
// a *sql.DB pool can hit different connections (release misses, the lock
// lives until that connection closes). Here lock and release use the same
// *sql.Conn; release uses a fresh background context (also after the caller's
// ctx was cancelled) and then closes the connection, which ends the session
// and frees the lock even if the release statement failed.
func TryNamedLock(ctx context.Context, d Dialect, db *sql.DB, name string) (release func(), ok bool, err error) {
	_, release, ok, err = TryNamedLockConn(ctx, d, db, name)
	return release, ok, err
}

// TryNamedLockConn is TryNamedLock that also returns the pinned connection
// holding the session lock, so callers can fence their writes on it (a lost
// session fails those writes instead of completing a stale claim).
func TryNamedLockConn(ctx context.Context, d Dialect, db *sql.DB, name string) (lockConn *sql.Conn, release func(), ok bool, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	var got int
	if d == Postgres {
		err = conn.QueryRowContext(ctx, "SELECT CASE WHEN pg_try_advisory_lock(hashtextextended($1, 0)) THEN 1 ELSE 0 END", name).Scan(&got)
	} else {
		err = conn.QueryRowContext(ctx, "SELECT COALESCE(GET_LOCK(?, 0), 0)", name).Scan(&got)
	}
	if err != nil || got != 1 {
		_ = conn.Close()
		return nil, nil, false, err
	}
	return conn, func() {
		rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var released bool
		var rerr error
		if d == Postgres {
			rerr = conn.QueryRowContext(rctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", name).Scan(&released)
		} else {
			var v sql.NullInt64
			rerr = conn.QueryRowContext(rctx, "SELECT RELEASE_LOCK(?)", name).Scan(&v)
			released = v.Valid && v.Int64 == 1
		}
		if rerr != nil || !released {
			// conn.Close() alone would return the physical connection - and
			// its session lock - to the pool. Returning driver.ErrBadConn from
			// Raw makes database/sql discard that connection, so the server
			// session ends and the lock is freed.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, true, nil
}

// NativeUniqueName is the plan name of the native unique index of a MySQL
// unique key (same derivation as dbmigrate.PGName("ux", table, key)).
func NativeUniqueName(table, key string) string {
	n := "ux__" + table + "__" + key
	if len(n) <= 63 {
		return n
	}
	h := sha256.Sum256([]byte(n))
	return n[:54] + "_" + hex.EncodeToString(h[:])[:8]
}
