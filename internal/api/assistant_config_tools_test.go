package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantBookingLimitsUpdate_DB verifies booking_limits_update: form
// reuse of the legacy handler (restaurantID from ctx), confirmation flow and
// real persistence in reservation_manager.
func TestAssistantBookingLimitsUpdate_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9016
	date := "2026-10-05"

	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name) VALUES (9016, 'forky-bl', 'BL') ON DUPLICATE KEY UPDATE name=VALUES(name)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM reservation_manager WHERE restaurant_id = 9016`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9016`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9609, Role: "admin"}})

	input := `{"date":"` + date + `","daily_limit":120,"confirmed":false,"confirmation_token":""}`
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "booking_limits_update", json.RawMessage(input))
	if err != nil {
		t.Fatalf("limits(pre): %v", err)
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
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "booking_limits_update", json.RawMessage(final))
	if err != nil {
		t.Fatalf("limits(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("limits failed: %s", out)
	}

	var limit int
	if err := db.QueryRowContext(ctx, `SELECT dailyLimit FROM reservation_manager WHERE restaurant_id=? AND reservationDate=?`, rid, date).Scan(&limit); err != nil {
		t.Fatalf("limit row missing: %v", err)
	}
	if limit != 120 {
		t.Fatalf("daily limit = %d, want 120", limit)
	}

	// Read-back via booking_limits_get.
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "booking_limits_get", json.RawMessage(`{"date":"`+date+`"}`))
	if err != nil {
		t.Fatalf("limits_get: %v", err)
	}
	if !strings.Contains(out, `"dailyLimit":120`) || !strings.Contains(out, `"freeBookingSeats"`) {
		t.Fatalf("limits_get unexpected: %s", out)
	}

	if _, err := s.assistantExecuteTool(context.Background(), rid, "booking_limits_update", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied booking_limits_update")
	}
	if _, err := s.assistantExecuteTool(context.Background(), rid, "booking_limits_get", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied booking_limits_get")
	}
}
