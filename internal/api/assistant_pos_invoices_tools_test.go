package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantPOSInvoicesPlatformTools_DB exercises the typed read tools for
// POS, invoices and platform against the real dev schema (empty datasets are
// valid; the goal is proving dispatch + auth + no 500s).
func TestAssistantPOSInvoicesPlatformTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9008

	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name) VALUES (9008, 'forky-pip', 'PIP') ON DUPLICATE KEY UPDATE name=VALUES(name)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9008`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, User: boUser{ID: 9602, Role: "admin"}})

	cases := []struct {
		tool  string
		input string
		key   string
	}{
		{"pos_visits_list", `{}`, `"visits"`},
		{"pos_tickets_list", `{}`, `"items"`},
		{"pos_cash_closures_list", `{}`, `"items"`},
		{"pos_cash_summary", `{}`, `"summary"`},
		{"invoices_list", `{}`, `"invoices"`},
		{"integrations_get", `{}`, `"success":true`},
		{"branding_get", `{}`, `"success":true`},
	}
	for _, tc := range cases {
		out, err := s.assistantExecuteToolUnsafe(authCtx, rid, tc.tool, json.RawMessage(tc.input))
		if err != nil {
			t.Fatalf("%s: %v", tc.tool, err)
		}
		if !strings.Contains(out, tc.key) {
			t.Fatalf("%s missing %s: %s", tc.tool, tc.key, out)
		}
	}

	// invoice_get for a missing id must surface the handler's not-found.
	if _, err := s.assistantExecuteToolUnsafe(authCtx, rid, "invoice_get", json.RawMessage(`{"id":99999999}`)); err == nil {
		t.Error("invoice_get missing id must error")
	}

	// Anonymous denied on all.
	for _, tc := range cases {
		if _, err := s.assistantExecuteTool(context.Background(), rid, tc.tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous must be denied %s", tc.tool)
		}
	}
}
