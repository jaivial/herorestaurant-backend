package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantStockTools_DB exercises the stock read tools through the
// registry against the real dev schema.
func TestAssistantStockTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9007

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9007, 'forky-stk', 'Stock') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO stock_items (id, restaurant_id, name, kind, base_dimension, base_unit) VALUES
			(9301, 9007, 'Arroz Bomba', 'RAW', 'MASS', 'g') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO stock_item_units (id, restaurant_id, stock_item_id, code, label, factor_to_base, is_default_purchase, is_default_display) VALUES
			(9302, 9007, 9301, 'g', 'gramos', 1, 1, 1) ON DUPLICATE KEY UPDATE code=VALUES(code)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_item_units WHERE id = 9302`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_items WHERE id = 9301`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9007`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, User: boUser{ID: 9601, Role: "admin"}})
	run := func(tool, input string) string {
		t.Helper()
		out, err := s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}

	out := run("stock_warehouses_list", `{}`)
	if !strings.Contains(out, `"warehouses"`) {
		t.Fatalf("stock_warehouses_list: %s", out)
	}

	out = run("stock_categories_list", `{}`)
	if !strings.Contains(out, `"categories"`) {
		t.Fatalf("stock_categories_list: %s", out)
	}

	out = run("stock_items_list", `{"search":"Arroz"}`)
	if !strings.Contains(out, "Arroz Bomba") {
		t.Fatalf("stock_items_list missing item: %s", out)
	}

	out = run("stock_summary", `{}`)
	if !strings.Contains(out, `"itemsTracked"`) {
		t.Fatalf("stock_summary: %s", out)
	}

	out = run("stock_item_movements_list", `{"id":9301}`)
	if !strings.Contains(out, `"movements"`) {
		t.Fatalf("stock_item_movements_list: %s", out)
	}

	// Anonymous session must be denied.
	for _, tool := range []string{"stock_warehouses_list", "stock_items_list", "stock_summary", "stock_item_movements_list"} {
		if _, err := s.assistantExecuteTool(context.Background(), rid, tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous must be denied %s", tool)
		}
	}
}
