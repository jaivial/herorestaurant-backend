package main

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// The registry must cover the REAL schema, not a hand-written list. If this
// fails, the sweep would treat live images as orphans.
func TestRegistryCoversTheRealSchema(t *testing.T) {
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	var schema string
	if err := database.QueryRow(`SELECT DATABASE()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	discovered, err := discoverURLColumns(context.Background(), database, schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) == 0 {
		t.Fatal("no URL columns discovered; the tripwire query is broken")
	}
	if unknown := unregisteredImageColumns(discovered, registeredColumnSet()); len(unknown) > 0 {
		t.Fatalf("unregistered image columns would be treated as orphans: %v", unknown)
	}
}
