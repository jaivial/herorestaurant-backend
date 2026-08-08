package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantHorariosMembersTools_DB exercises the handler-reuse read tools
// through the registry: auth context, dispatch and tenant scoping against the
// real dev schema.
func TestAssistantHorariosMembersTools_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9006

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9006, 'forky-hm', 'HM') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO restaurant_members (id, restaurant_id, first_name, last_name, is_active) VALUES
			(9110, 9006, 'Carlos', 'Verde', 1),
			(9111, 9006, 'Diana', 'Negra', 1) ON DUPLICATE KEY UPDATE first_name=VALUES(first_name)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id IN (9110, 9111)`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9006`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, User: boUser{ID: 9600, Role: "admin"}})

	run := func(tool string, input string) string {
		t.Helper()
		out, err := s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return out
	}

	// members_list returns both members of restaurant 9006.
	out := run("members_list", `{}`)
	for _, want := range []string{`"firstName":"Carlos"`, `"lastName":"Verde"`, `"firstName":"Diana"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("members_list missing %s: %s", want, out)
		}
	}

	// member_get returns only the requested member.
	out = run("member_get", `{"id":9110}`)
	if !strings.Contains(out, `"lastName":"Verde"`) || strings.Contains(out, "Negra") {
		t.Fatalf("member_get scoping wrong: %s", out)
	}

	// member_balance_get returns a quarterly balance.
	out = run("member_balance_get", `{"id":9110}`)
	for _, want := range []string{`"balanceHours":`, `"weeklyContractHours":40`} {
		if !strings.Contains(out, want) {
			t.Fatalf("member_balance_get missing %s: %s", want, out)
		}
	}

	// schedules_by_date returns a schedules array (empty is valid).
	out = run("schedules_by_date", `{}`)
	if !strings.Contains(out, `"schedules"`) {
		t.Fatalf("schedules_by_date missing schedules key: %s", out)
	}

	// schedules_month returns month coverage points.
	out = run("schedules_month", `{}`)
	if !strings.Contains(out, `"points"`) && !strings.Contains(out, `"success":true`) {
		t.Fatalf("schedules_month unexpected: %s", out)
	}

	// Anonymous session must be denied on all of them.
	for _, tool := range []string{"members_list", "member_get", "member_balance_get", "schedules_by_date", "schedules_month"} {
		if _, err := s.assistantExecuteTool(context.Background(), rid, tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("anonymous must be denied %s", tool)
		}
	}
}

// TestAssistantCustomersList_DB verifies customers_list against the real
// analytics_customers schema (display_name, not name).
func TestAssistantCustomersList_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9090
	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9090, 'forky-cust', 'CUST') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO analytics_customers (restaurant_id, display_name, email, phone, identity_key) VALUES
			(9090, 'Pepa Cliente', 'pepa@test.local', '600909001', 'k1'),
			(9090, '', 'anon@test.local', '600909002', 'k2')`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM analytics_customers WHERE restaurant_id = 9090`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9090`)
	})
	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9690, Role: "admin"}})

	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "customers_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("customers_list: %v", err)
	}
	if !strings.Contains(out, "Pepa Cliente") {
		t.Fatalf("customers_list missing display_name: %s", out)
	}
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "customers_list", json.RawMessage(`{"search":"anon"}`))
	if err != nil {
		t.Fatalf("customers_list(search): %v", err)
	}
	if !strings.Contains(out, "anon@test.local") {
		t.Fatalf("customers_list search fallback missing: %s", out)
	}
}

// TestAssistantTypedDomainListsSchema_DB ensures every typed-domain list tool
// runs against the real dev schema without column/table errors (empty results
// are valid; the query itself must not fail).
func TestAssistantTypedDomainListsSchema_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9091
	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name) VALUES (9091, 'forky-tdl', 'TDL') ON DUPLICATE KEY UPDATE name=VALUES(name)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9091`)
	})
	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9691, Role: "admin"}})
	for _, tool := range []string{
		"schedules_list", "customers_list", "stock_items_list", "pos_visits_list",
		"invoices_list", "recipes_list", "production_list", "waste_costs_list",
	} {
		if _, err := s.assistantExecuteToolUnsafe(authCtx, rid, tool, json.RawMessage(`{}`)); err != nil {
			t.Errorf("%s errored against real schema: %v", tool, err)
		}
	}
}
