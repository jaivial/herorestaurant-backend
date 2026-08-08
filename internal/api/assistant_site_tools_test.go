package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestAssistantSitePublish_DB verifies site_publish: confirmation flow, version
// snapshot created and site marked published.
func TestAssistantSitePublish_DB(t *testing.T) {
	db := requireToolTestDB(t)
	ctx := context.Background()
	rid := 9020
	siteID := "site-test-0001"

	seed := []string{
		`INSERT INTO restaurants (id, slug, name) VALUES (9020, 'forky-sbp', 'SBP') ON DUPLICATE KEY UPDATE name=VALUES(name)`,
		`INSERT INTO site_builder_sites (id, restaurant_id, name, subdomain, theme_config, settings, status) VALUES
			('site-test-0001', 9020, 'Test Site', 'forky-sbp-sub', JSON_OBJECT('primary','#111'), JSON_OBJECT('lang','es'), 'draft')
			ON DUPLICATE KEY UPDATE name=VALUES(name), status=VALUES(status)`,
	}
	for _, stmt := range seed {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM site_builder_versions WHERE site_id = 'site-test-0001'`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM site_builder_sites WHERE id = 'site-test-0001'`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM restaurants WHERE id = 9020`)
	})

	s := &Server{db: db}
	authCtx := withBOAuth(ctx, boAuth{ActiveRestaurantID: rid, Role: "admin", User: boUser{ID: 9612, Role: "admin"}})

	input := `{"site_id":"` + siteID + `","confirmed":false,"confirmation_token":""}`
	out, err := s.assistantExecuteToolUnsafe(authCtx, rid, "site_publish", json.RawMessage(input))
	if err != nil {
		t.Fatalf("publish(pre): %v", err)
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
	out, err = s.assistantExecuteToolUnsafe(authCtx, rid, "site_publish", json.RawMessage(final))
	if err != nil {
		t.Fatalf("publish(exec): %v", err)
	}
	if !strings.Contains(out, `"success":true`) || !strings.Contains(out, `"version_number":1`) {
		t.Fatalf("publish failed: %s", out)
	}

	var status, pubVersionID string
	if err := db.QueryRowContext(ctx, `SELECT status, published_version_id FROM site_builder_sites WHERE id=?`, siteID).Scan(&status, &pubVersionID); err != nil {
		t.Fatal(err)
	}
	if status != "published" || pubVersionID == "" {
		t.Fatalf("site status=%s version=%q, want published + version", status, pubVersionID)
	}
	var versionCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_builder_versions WHERE site_id=? AND status='published'`, siteID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 1 {
		t.Fatalf("published versions = %d, want 1", versionCount)
	}

	// Cross-restaurant isolation is enforced by canAccessSite (boAuth
	// ActiveRestaurantID must equal the site's restaurant).
	if _, err := s.assistantExecuteTool(context.Background(), rid, "site_publish", json.RawMessage(`{}`)); err == nil {
		t.Error("anonymous must be denied site_publish")
	}
}
