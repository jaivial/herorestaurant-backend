package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantFichajeEntriesListTenantScoped_DB verifies fichaje entries are
// returned for the requested member/date only, never crossing restaurants.
func TestAssistantFichajeEntriesListTenantScoped_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	date := "2026-08-07"

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9003, 'forky-fic-a', 'Fichaje A'), (9004, 'forky-fic-b', 'Fichaje B') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO restaurant_members (id, restaurant_id, first_name, last_name, is_active) VALUES (9101, 9003, 'Ana', 'Roja', 1), (9102, 9004, 'Bea', 'Azul', 1) ON DUPLICATE KEY UPDATE first_name=VALUES(first_name)`,
		`INSERT INTO member_time_entries (restaurant_member_id, restaurant_id, work_date, start_time, end_time, minutes_worked, source) VALUES
			(9101, 9003, '` + date + `', '10:00:00', '11:00:00', 60, 'clock'),
			(9101, 9003, '` + date + `', '12:00:00', NULL, 0, 'clock'),
			(9102, 9004, '` + date + `', '09:00:00', '09:30:00', 30, 'clock')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_time_entries WHERE restaurant_id IN (9003, 9004)`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id IN (9101, 9102)`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id IN (9003, 9004)`)
	})

	s := &Server{db: db}

	// Own member: 2 entries for restaurant 9003.
	out, err := s.assistantExecuteToolUnsafe(ctx, 9003, "fichaje_entries_list", json.RawMessage(`{"date":"`+date+`","member_id":9101}`))
	if err != nil {
		t.Fatalf("entries_list: %v", err)
	}
	if !strings.Contains(out, `"memberName":"Ana Roja"`) || !strings.Contains(out, `"minutesWorked":60`) {
		t.Fatalf("missing expected entries: %s", out)
	}
	if strings.Contains(out, "Bea Azul") {
		t.Fatalf("tenant leak (Bea visible): %s", out)
	}

	// Member from another restaurant queried against 9003 -> empty.
	out, err = s.assistantExecuteToolUnsafe(ctx, 9003, "fichaje_entries_list", json.RawMessage(`{"date":"`+date+`","member_id":9102}`))
	if err != nil {
		t.Fatalf("entries_list(foreign member): %v", err)
	}
	if strings.Contains(out, "Bea Azul") {
		t.Fatalf("cross-restaurant leak: %s", out)
	}
}

// TestAssistantFichajeStateGet_DB verifies fichaje_state_get resolves the
// caller's clock member and active entry within the active restaurant.
func TestAssistantFichajeStateGet_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	date := "2026-08-07"

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9005, 'forky-fic-c', 'Fichaje C') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO bo_users (id, email, name, password_hash) VALUES (9401, 'fic-a@test.local', 'Ana Roja', 'testhash') ON DUPLICATE KEY UPDATE email=VALUES(email)`,
		`INSERT INTO restaurant_members (id, restaurant_id, bo_user_id, first_name, last_name, is_active) VALUES (9103, 9005, 9401, 'Ana', 'Roja', 1) ON DUPLICATE KEY UPDATE bo_user_id=VALUES(bo_user_id)`,
		`INSERT INTO member_time_entries (restaurant_member_id, restaurant_id, work_date, start_time, end_time, minutes_worked, source) VALUES (9103, 9005, '` + date + `', '10:00:00', NULL, 0, 'clock')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_time_entries WHERE restaurant_id = 9005`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id = 9103`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM bo_users WHERE id = 9401`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9005`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: 9005, User: boUser{ID: 9401}})
	out, err := s.assistantExecuteToolUnsafe(authCtx, 9005, "fichaje_state_get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("state_get: %v", err)
	}
	for _, want := range []string{`"fullName":"Ana Roja"`, `"activeEntry"`, `"startTime":"10:00"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("state missing %s: %s", want, out)
		}
	}
}
