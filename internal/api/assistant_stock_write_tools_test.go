package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantStockWriteTools_DB verifies the stock write tools: confirmation
// flow and real persistence (movement -> level, transfer -> two movements).
func TestAssistantStockWriteTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9011

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9011, 'forky-stkw', 'STKW') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO stock_warehouses (id, restaurant_id, name, code, type, is_default, is_active) VALUES
			(9305, 9011, 'Almacén A', 'A', 'STORAGE', 1, 1),
			(9306, 9011, 'Almacén B', 'B', 'STORAGE', 0, 1) ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO stock_items (id, restaurant_id, name, kind, base_dimension, base_unit, is_tracked) VALUES
			(9303, 9011, 'Harina', 'RAW', 'MASS', 'g', 1) ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO stock_item_units (id, restaurant_id, stock_item_id, code, label, factor_to_base, is_default_purchase, is_default_display, can_purchase) VALUES
			(9304, 9011, 9303, 'g', 'gramos', 1, 1, 1, 1) ON DUPLICATE KEY UPDATE code=VALUES(code)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_movements WHERE restaurant_id = 9011`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_levels WHERE restaurant_id = 9011`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_item_units WHERE id = 9304`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_items WHERE id = 9303`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM stock_warehouses WHERE id IN (9305, 9306)`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9011`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9605, Role: "admin"}})
	exec := func(tool, input string) (string, string) {
		t.Helper()
		out, err := s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%s invalid json: %s", tool, out)
		}
		tok, _ := v["confirmation_token"].(string)
		return out, tok
	}
	confirmInput := func(input, token string) string {
		if token == "" {
			t.Fatal("missing confirmation token")
		}
		input = strings.Replace(input, `"confirmed":false`, `"confirmed":true`, 1)
		return strings.Replace(input, `"confirmation_token":""`, `"confirmation_token":"`+token+`"`, 1)
	}

	// movement: create (preview -> execute).
	mvIn := `{"item_id":9303,"warehouse_id":9305,"quantity":100,"unit_id":9304,"type":"ADJUSTMENT","direction":"ADD","idempotency_key":"mv-1","confirmed":false,"confirmation_token":""}`
	_, tok := exec("stock_movement_create", mvIn)
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "stock_movement_create", json.RawMessage(confirmInput(mvIn, tok)))
	if err != nil {
		t.Fatalf("movement exec: %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("movement failed: %s", out)
	}
	var qty float64
	if err := db.QueryRowContext(ctx, `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, rid, 9303, 9305).Scan(&qty); err != nil {
		t.Fatalf("level not updated: %v", err)
	}
	if qty != 100 {
		t.Fatalf("level qty = %v, want 100", qty)
	}

	// transfer: preview -> execute.
	trIn := `{"item_id":9303,"from_warehouse_id":9305,"to_warehouse_id":9306,"quantity":40,"unit_id":9304,"idempotency_key":"tr-1","confirmed":false,"confirmation_token":""}`
	_, tok = exec("stock_transfer_create", trIn)
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "stock_transfer_create", json.RawMessage(confirmInput(trIn, tok)))
	if err != nil {
		t.Fatalf("transfer exec: %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("transfer failed: %s", out)
	}
	var fromQty, toQty float64
	if err := db.QueryRowContext(ctx, `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, rid, 9303, 9305).Scan(&fromQty); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT qty_base FROM stock_levels WHERE restaurant_id=? AND stock_item_id=? AND warehouse_id=?`, rid, 9303, 9306).Scan(&toQty); err != nil {
		t.Fatal(err)
	}
	if fromQty != 60 || toQty != 40 {
		t.Fatalf("transfer levels from=%v to=%v, want 60/40", fromQty, toQty)
	}

	// Anonymous denied.
	for _, tool := range []string{"stock_movement_create", "stock_transfer_create"} {
		if _, err := s.assistantExecuteTool(context.Background(), rid, tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous must be denied %s", tool)
		}
	}
}
