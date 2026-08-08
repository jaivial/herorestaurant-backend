package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// requireToolTestDB returns a *sql.DB for Forky integration tests. Tests are
// skipped unless FORKY_TEST_MYSQL_DSN points at a migrated restaurant database
// (the real deployment schema: restaurants, bookings, stock, pos, ...).
func requireToolTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("FORKY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("FORKY_TEST_MYSQL_DSN not set; skipping Forky integration test")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAssistantRestaurantInfoTenantScoped_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name, contact_phone) VALUES
		(9001, 'forky-a', 'Forky A', '600000001'),
		(9002, 'forky-b', 'Forky B', '600000002')
		ON DUPLICATE KEY UPDATE name = VALUES(name), contact_phone = VALUES(contact_phone)`); err != nil {
		t.Fatalf("seed restaurants: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id IN (9001, 9002)`)
	})

	s := &Server{db: db}
	out, err := s.assistantExecuteToolUnsafe(ctx, 9001, "restaurant_info", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("restaurant_info: %v", err)
	}
	if !strings.Contains(out, `"name":"Forky A"`) || !strings.Contains(out, `"phone":"600000001"`) || strings.Contains(out, `"Forky B"`) {
		t.Fatalf("tenant leak or wrong result: %s", out)
	}

	out, err = s.assistantExecuteToolUnsafe(ctx, 9002, "restaurant_info", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("restaurant_info(2): %v", err)
	}
	if !strings.Contains(out, `"name":"Forky B"`) || strings.Contains(out, `"Forky A"`) {
		t.Fatalf("tenant leak or wrong result: %s", out)
	}
}
