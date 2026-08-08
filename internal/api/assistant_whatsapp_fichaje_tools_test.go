package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func confirmTool(t *testing.T, s *Server, authCtx context.Context, rid int, tool, input string) string {
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

// TestAssistantWhatsappBotConfigUpdate_DB verifies whatsapp_bot_config_update:
// confirmation flow and real upsert.
func TestAssistantWhatsappBotConfigUpdate_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9018
	if _, err := db.ExecContext(ctx, `INSERT INTO restaurants (id, slug, name) VALUES (9018, 'forky-wa', 'WA') ON DUPLICATE KEY UPDATE name=VALUES(name)`); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM whatsapp_bot_config WHERE restaurant_id = 9018`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9018`)
	})
	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9611, Role: "admin"}})

	out := confirmTool(t, s, authCtx, rid, "whatsapp_bot_config_update",
		`{"language_default":"es","tone":"cercano","contact_phone":"600000018","confirmed":false,"confirmation_token":""}`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("config update failed: %s", out)
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT config_json FROM whatsapp_bot_config WHERE restaurant_id=?`, rid).Scan(&raw); err != nil {
		t.Fatalf("config row missing: %v", err)
	}
	if !strings.Contains(raw, "cercano") || !strings.Contains(raw, "600000018") {
		t.Fatalf("config_json wrong: %s", raw)
	}
	if _, err := s.assistantExecuteTool(context.Background(), rid, "whatsapp_bot_config_update", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied whatsapp_bot_config_update")
	}
}

// TestAssistantFichajeSelfService_DB verifies fichaje_start/stop for the
// authenticated user's own clock member (no DNI/password).
func TestAssistantFichajeSelfService_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9019
	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9019, 'forky-fss', 'FSS') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO bo_users (id, email, name, password_hash) VALUES (9402, 'fss@test.local', 'Iria Sol', 'testhash') ON DUPLICATE KEY UPDATE email=VALUES(email)`,
		`INSERT INTO restaurant_members (id, restaurant_id, bo_user_id, first_name, last_name, is_active) VALUES (9126, 9019, 9402, 'Iria', 'Sol', 1) ON DUPLICATE KEY UPDATE bo_user_id=VALUES(bo_user_id)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM member_time_entries WHERE restaurant_id = 9019`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurant_members WHERE id = 9126`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM bo_users WHERE id = 9402`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9019`)
	})
	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9402, Role: "admin"}})

	out := confirmTool(t, s, authCtx, rid, "fichaje_start", `{"confirmed":false,"confirmation_token":""}`)
	if !strings.Contains(out, `"success":true`) || !strings.Contains(out, `"activeEntry"`) {
		t.Fatalf("fichaje_start failed: %s", out)
	}
	var entryID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM member_time_entries WHERE restaurant_id=? AND restaurant_member_id=? AND end_time IS NULL ORDER BY id DESC LIMIT 1`, rid, 9126).Scan(&entryID); err != nil {
		t.Fatalf("open entry missing: %v", err)
	}

	out = confirmTool(t, s, authCtx, rid, "fichaje_stop", `{"confirmed":false,"confirmation_token":""}`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("fichaje_stop failed: %s", out)
	}
	var end sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT end_time FROM member_time_entries WHERE id=?`, entryID).Scan(&end); err != nil {
		t.Fatal(err)
	}
	if !end.Valid {
		t.Fatal("entry was not closed")
	}

	if _, err := s.assistantExecuteTool(context.Background(), rid, "fichaje_start", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied fichaje_start")
	}
}
