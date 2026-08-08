package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestAssistantSchedulesWriteTools_DB verifies the horarios write tools:
// confirmation token flow (requires_confirmation -> execute) and real
// persistence via the domain handlers.
func TestAssistantSchedulesWriteTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9010
	date := "2026-09-01"

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9010, 'forky-schw', 'SCHW') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO restaurant_members (id, restaurant_id, first_name, last_name, is_active) VALUES (9120, 9010, 'Eva', 'Lila', 1) ON DUPLICATE KEY UPDATE first_name=VALUES(first_name)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_work_schedules WHERE restaurant_id = 9010`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id = 9120`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9010`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, User: boUser{ID: 9604, Role: "admin"}})

	// Anonymous must be denied.
	if _, err := s.assistantExecuteTool(context.Background(), rid, "schedules_create", json.RawMessage(`{}`)); err == nil {
		t.Fatal("anonymous must be denied schedules_create")
	}

	// 1. create: confirm flow.
	create := `{"date":"` + date + `","member_id":9120,"start_time":"09:00","end_time":"12:00","confirmed":false}`
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "schedules_create", json.RawMessage(create))
	if err != nil {
		t.Fatalf("schedules_create(pre): %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	tok, _ := v["confirmation_token"].(string)
	if tok == "" {
		t.Fatalf("missing confirmation token: %s", out)
	}
	confirmed := `{"date":"` + date + `","member_id":9120,"start_time":"09:00","end_time":"12:00","confirmed":true,"confirmation_token":"` + tok + `"}`
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "schedules_create", json.RawMessage(confirmed))
	if err != nil {
		t.Fatalf("schedules_create(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("schedules_create failed: %s", out)
	}

	var scheduleID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM member_work_schedules WHERE restaurant_id=? AND restaurant_member_id=? AND work_date=?`, rid, 9120, date).Scan(&scheduleID); err != nil {
		t.Fatalf("schedule row missing: %v", err)
	}

	// 2. update: confirm flow.
	upd := `{"id":` + strconv.FormatInt(scheduleID, 10) + `,"start_time":"10:00","end_time":"13:00","confirmed":false}`
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "schedules_update", json.RawMessage(upd))
	if err != nil {
		t.Fatalf("schedules_update(pre): %v", err)
	}
	_ = json.Unmarshal([]byte(out), &v)
	tok, _ = v["confirmation_token"].(string)
	if tok == "" {
		t.Fatalf("missing update token: %s", out)
	}
	upd = `{"id":` + strconv.FormatInt(scheduleID, 10) + `,"start_time":"10:00","end_time":"13:00","confirmed":true,"confirmation_token":"` + tok + `"}`
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "schedules_update", json.RawMessage(upd))
	if err != nil {
		t.Fatalf("schedules_update(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("schedules_update failed: %s", out)
	}

	// 3. delete: confirm flow.
	del := `{"id":` + strconv.FormatInt(scheduleID, 10) + `,"confirmed":false}`
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "schedules_delete", json.RawMessage(del))
	if err != nil {
		t.Fatalf("schedules_delete(pre): %v", err)
	}
	_ = json.Unmarshal([]byte(out), &v)
	tok, _ = v["confirmation_token"].(string)
	if tok == "" {
		t.Fatalf("missing delete token: %s", out)
	}
	del = `{"id":` + strconv.FormatInt(scheduleID, 10) + `,"confirmed":true,"confirmation_token":"` + tok + `"}`
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "schedules_delete", json.RawMessage(del))
	if err != nil {
		t.Fatalf("schedules_delete(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("schedules_delete failed: %s", out)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_work_schedules WHERE id=?`, scheduleID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("schedule not deleted, remaining=%d", remaining)
	}
}
