package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantPOSTicketLineAdd_DB verifies pos_ticket_line_add: confirmation
// flow and real line insertion with ticket recalculation.
func TestAssistantPOSTicketLineAdd_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9017

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9017, 'forky-posl', 'POSL') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO pos_products (id, restaurant_id, name, price_gross_cents) VALUES (9308, 9017, 'Arroz Senyoret', 1000) ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO pos_visits (id, restaurant_id, channel, service_date, opened_by, open_idempotency_key) VALUES
			(9411, 9017, 'DINE_IN', CURDATE(), 1, 'v-1') ON DUPLICATE KEY UPDATE status=VALUES(status)`,
		`INSERT INTO pos_tickets (id, restaurant_id, visit_id, ticket_number, creation_idempotency_key, opened_by) VALUES
			(9412, 9017, 9411, 'T-1', 't-1', 1) ON DUPLICATE KEY UPDATE status=VALUES(status)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_ticket_lines WHERE restaurant_id = 9017`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_tickets WHERE id = 9412`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_visits WHERE id = 9411`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_products WHERE id = 9308`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9017`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9610, Role: "admin"}})

	input := `{"ticket_id":9412,"product_id":9308,"quantity":2,"idempotency_key":"line-1","confirmed":false,"confirmation_token":""}`
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "pos_ticket_line_add", json.RawMessage(input))
	if err != nil {
		t.Fatalf("line(pre): %v", err)
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
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "pos_ticket_line_add", json.RawMessage(final))
	if err != nil {
		t.Fatalf("line(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("line failed: %s", out)
	}

	var lineCount, lineTotal int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(line_total_gross_cents),0) FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=?`, rid, 9412).Scan(&lineCount, &lineTotal); err != nil {
		t.Fatal(err)
	}
	if lineCount != 1 || lineTotal != 2000 {
		t.Fatalf("lines=%d total=%d, want 1/2000", lineCount, lineTotal)
	}

	if _, err := s.assistantExecuteTool(context.Background(), rid, "pos_ticket_line_add", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied pos_ticket_line_add")
	}
}
