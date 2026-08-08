package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantFichajeAdminMenuWriteTools_DB verifies fichaje admin start/stop
// and menu active toggle: confirmation flow + real persistence.
func TestAssistantFichajeAdminMenuWriteTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9012

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9012, 'forky-fam', 'FAM') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO restaurant_members (id, restaurant_id, first_name, last_name, is_active) VALUES (9122, 9012, 'Fede', 'Mora', 1) ON DUPLICATE KEY UPDATE first_name=VALUES(first_name)`,
		`INSERT INTO menus (id, restaurant_id, menu_title, price, active, is_draft, menu_type, editor_version, main_dishes_limit, main_dishes_limit_number, min_party_size, show_menu_slider, slider_mode) VALUES
			(9502, 9012, 'Menú Toggle', 30.00, 1, 0, 'menu', 1, 1, 0, 2, 1, 'default')
			ON DUPLICATE KEY UPDATE menu_title=VALUES(menu_title)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_time_entries WHERE restaurant_id = 9012`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM menus WHERE id = 9502`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id = 9122`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9012`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9606, Role: "admin"}})

	execConfirm := func(tool, input string) string {
		t.Helper()
		out, err := s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s(pre): %v", tool, err)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%s bad json: %s", tool, out)
		}
		tok, _ := v["confirmation_token"].(string)
		if tok == "" {
			t.Fatalf("%s missing token: %s", tool, out)
		}
		final := strings.Replace(input, `"confirmed":false`, `"confirmed":true`, 1)
		final = strings.Replace(final, `"confirmation_token":""`, `"confirmation_token":"`+tok+`"`, 1)
		out, err = s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(final))
		if err != nil {
			t.Fatalf("%s(exec): %v", tool, err)
		}
		return out
	}

	// fichaje_admin_start
	out := execConfirm("fichaje_admin_start", `{"member_id":9122,"confirmed":false,"confirmation_token":""}`)
	if !strings.Contains(out, `"success":true`) || !strings.Contains(out, `"activeEntry"`) {
		t.Fatalf("fichaje_admin_start failed: %s", out)
	}
	var entryID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM member_time_entries WHERE restaurant_id=? AND restaurant_member_id=? AND end_time IS NULL ORDER BY id DESC LIMIT 1`, rid, 9122).Scan(&entryID); err != nil {
		t.Fatalf("open entry missing: %v", err)
	}

	// fichaje_admin_stop
	out = execConfirm("fichaje_admin_stop", `{"member_id":9122,"confirmed":false,"confirmation_token":""}`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("fichaje_admin_stop failed: %s", out)
	}
	var endTime sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT end_time FROM member_time_entries WHERE id=?`, entryID).Scan(&endTime); err != nil {
		t.Fatal(err)
	}
	if !endTime.Valid {
		t.Fatal("entry was not closed")
	}

	// menu_toggle_active
	out = execConfirm("menu_toggle_active", `{"id":9502,"confirmed":false,"confirmation_token":""}`)
	if !strings.Contains(out, `"success":true`) || !strings.Contains(out, `"active":false`) {
		t.Fatalf("menu_toggle_active failed: %s", out)
	}
	var active int
	if err := db.QueryRowContext(ctx, `SELECT active FROM menus WHERE id=9502`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("menu still active=%d", active)
	}

	// Anonymous denied.
	for _, tool := range []string{"fichaje_admin_start", "fichaje_admin_stop", "menu_toggle_active"} {
		if _, err := s.assistantExecuteTool(context.Background(), rid, tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous must be denied %s", tool)
		}
	}
}
