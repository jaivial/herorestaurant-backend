package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.sql
var migrationFS embed.FS

func Apply(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}

	files, err := fs.Glob(migrationFS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, name := range files {
		applied, err := isApplied(ctx, db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		src, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		sqlText := strings.TrimSpace(string(src))
		if sqlText == "" {
			if err := markApplied(ctx, db, name); err != nil {
				return err
			}
			continue
		}

		if _, err := db.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("migration %s failed: %w", name, err)
		}
		if err := markApplied(ctx, db, name); err != nil {
			return err
		}
	}

	return nil
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
	`)
	return err
}

func isApplied(ctx context.Context, db *sql.DB, id string) (bool, error) {
	var tmp string
	err := db.QueryRowContext(ctx, "SELECT id FROM schema_migrations WHERE id = ? LIMIT 1", id).Scan(&tmp)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func markApplied(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (id) VALUES (?)", id)
	return err
}

