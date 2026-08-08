package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantMemberCompensationCreate_DB verifies member_compensation_create:
// confirmation flow and real persistence with audit row.
func TestAssistantMemberCompensationCreate_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9014

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9014, 'forky-mcw', 'MCW') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO restaurant_members (id, restaurant_id, first_name, last_name, is_active) VALUES (9124, 9014, 'Gema', 'Rosa', 1) ON DUPLICATE KEY UPDATE first_name=VALUES(first_name)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_compensation_audit WHERE restaurant_id = 9014`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_compensations WHERE restaurant_id = 9014`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id = 9124`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9014`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9608, Role: "admin"}})

	input := `{"member_id":9124,"pay_type":"MONTHLY","gross_amount":1400,"monthly_hours":160,"employer_cost_pct":30,"effective_from":"2026-09-01","confirmed":false,"confirmation_token":""}`
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "member_compensation_create", json.RawMessage(input))
	if err != nil {
		t.Fatalf("compensation(pre): %v", err)
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
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "member_compensation_create", json.RawMessage(final))
	if err != nil {
		t.Fatalf("compensation(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("compensation failed: %s", out)
	}

	var compCount, auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_compensations WHERE restaurant_id=? AND restaurant_member_id=?`, rid, 9124).Scan(&compCount); err != nil {
		t.Fatal(err)
	}
	if compCount != 1 {
		t.Fatalf("compensations = %d, want 1", compCount)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_compensation_audit WHERE restaurant_id=?`, rid).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount < 1 {
		t.Fatalf("compensation audit rows = %d, want >=1", auditCount)
	}

	if _, err := s.assistantExecuteTool(context.Background(), rid, "member_compensation_create", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied member_compensation_create")
	}
}
