package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantMenusAnalyticsTools_DB exercises the menús typed reads and the
// extended analytics metrics against the real dev schema.
func TestAssistantMenusAnalyticsTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9009

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9009, 'forky-mna', 'MNA') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO menus (id, restaurant_id, menu_title, price, active, is_draft, menu_type, editor_version, main_dishes_limit, main_dishes_limit_number, min_party_size, show_menu_slider, slider_mode) VALUES
			(9501, 9009, 'Menú Degustación', 45.00, 1, 0, 'menu', 1, 1, 0, 2, 1, 'default')
			ON DUPLICATE KEY UPDATE menu_title=VALUES(menu_title)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM menus WHERE id = 9501`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9009`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, User: boUser{ID: 9603, Role: "admin"}})
	run := func(tool, input string) string {
		t.Helper()
		out, err := s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}

	out := run("menus_list", `{}`)
	if !strings.Contains(out, "Menú Degustación") {
		t.Fatalf("menus_list missing menu: %s", out)
	}
	out = run("menu_get", `{"id":9501}`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("menu_get failed: %s", out)
	}

	// Analytics: each metric returns a valid shape on empty datasets.
	for _, tc := range []struct{ metric, key string }{
		{"revenue", `"value_cents"`},
		{"revenue_by_day", `"chart"`},
		{"bookings_by_hour", `"chart"`},
		{"bookings", `"series"`},
	} {
		out = run("analytics_report", `{"metric":"`+tc.metric+`","date_from":"2026-08-01","date_to":"2026-08-31"}`)
		if !strings.Contains(out, tc.key) {
			t.Fatalf("analytics_report %s missing %s: %s", tc.metric, tc.key, out)
		}
	}

	// Anonymous denied.
	for _, tool := range []string{"menus_list", "menu_get", "menu_sections_get", "analytics_report"} {
		if _, err := s.assistantExecuteTool(context.Background(), rid, tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous must be denied %s", tool)
		}
	}
}
