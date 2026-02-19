package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"preactvillacarmen/internal/config"
)

func OpenMySQL(cfg config.MySQLConfig) (*sql.DB, error) {
	if err := ensureDatabaseExists(cfg); err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.DBName,
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func ensureDatabaseExists(cfg config.MySQLConfig) error {
	dbName := strings.TrimSpace(cfg.DBName)
	if dbName == "" {
		return fmt.Errorf("empty DB_NAME")
	}

	adminDSN := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/?charset=utf8mb4&collation=utf8mb4_unicode_ci",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
	)

	adminDB, err := sql.Open("mysql", adminDSN)
	if err != nil {
		return err
	}
	defer adminDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := adminDB.PingContext(ctx); err != nil {
		return err
	}

	stmt := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		strings.ReplaceAll(dbName, "`", "``"),
	)
	_, createErr := adminDB.ExecContext(ctx, stmt)
	if createErr == nil {
		return nil
	}

	// If user lacks CREATE DATABASE but can connect to an existing DB, allow startup.
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		dbName,
	)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()

	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("create database %q failed: %v; fallback connection failed: %w", dbName, createErr, err)
	}
	return nil
}
