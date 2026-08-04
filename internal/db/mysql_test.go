package db

import (
	"database/sql"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func TestConfigureMySQLPool(t *testing.T) {
	database, err := sql.Open("mysql", "user:pass@tcp(localhost:3306)/db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	configureMySQLPool(database)
	if got := database.Stats().MaxOpenConnections; got != 25 {
		t.Fatalf("MaxOpenConnections = %d", got)
	}
}
