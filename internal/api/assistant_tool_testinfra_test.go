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

// TestAssistantConfirmationDurable_DB verifies confirmation tokens are stored
// in MySQL, survive a store recreation and are single-use (replay rejected).
func TestAssistantConfirmationDurable_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9092
	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name) VALUES (9092, 'forky-conf', 'CONF') ON DUPLICATE KEY UPDATE name=VALUES(name)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM forky_confirmation_tokens WHERE restaurant_id = ?`, "9092")
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9092`)
	})
	args := json.RawMessage(`{"date":"2026-08-07","time":"20:00","people":2,"name":"Ana"}`)

	s1 := &Server{db: db, confirmationStore: newConfirmationStore(db)}
	out, err := s1.assistantRequireConfirmation(rid, "create_booking", args)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	tok, _ := v["confirmation_token"].(string)
	if tok == "" {
		t.Fatalf("missing token: %s", out)
	}

	// A fresh store (same DB) must still consume it (survives restart).
	s2 := &Server{db: db, confirmationStore: newConfirmationStore(db)}
	if err := s2.assistantConsumeConfirmation(tok, rid, "create_booking", args); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// Replay must fail.
	if err := s2.assistantConsumeConfirmation(tok, rid, "create_booking", args); err == nil {
		t.Fatal("replay accepted")
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM forky_confirmation_tokens WHERE restaurant_id = ?`, "9092").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("tokens remaining = %d, want 0", remaining)
	}
}

// TestAssistantCreateBookingRoutesDomain_DB verifies create_booking goes
// through the real domain validation (boNormalizeAndValidateBookingInput):
// a booking without a valid phone is rejected by the domain, not inserted raw.
func TestAssistantCreateBookingRoutesDomain_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9093
	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name) VALUES (9093, 'forky-bcr', 'BCR') ON DUPLICATE KEY UPDATE name=VALUES(name)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM bookings WHERE restaurant_id = 9093`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9093`)
	})
	s := &Server{db: db, confirmationStore: newConfirmationStore(db)}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9693, Role: "admin"}})

	input := `{"date":"2026-09-10","time":"20:00","people":3,"name":"Sin Telefono","confirmed":false,"confirmation_token":""}`
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "create_booking", json.RawMessage(input))
	if err != nil {
		t.Fatalf("pre: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	tok, _ := v["confirmation_token"].(string)
	if tok == "" {
		t.Fatalf("missing token: %s", out)
	}
	final := strings.Replace(input, `"confirmed":false`, `"confirmed":true`, 1)
	final = strings.Replace(final, `"confirmation_token":""`, `"confirmation_token":"`+tok+`"`, 1)
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "create_booking", json.RawMessage(final))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	// Domain validation rejects the missing/invalid phone; nothing is inserted.
	if !strings.Contains(out, "Teléfono") && !strings.Contains(out, "phone") {
		t.Fatalf("expected domain phone validation, got: %s", out)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookings WHERE restaurant_id=? AND reservation_date='2026-09-10'`, rid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("booking inserted despite validation failure: %d", count)
	}
}
