package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"preactvillacarmen/internal/config"
)

// preferencesTestServer opens the migrated restaurant database so the
// user_preferences table exists, mirroring the fichaje/POS test setup.
func preferencesTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(db, config.Config{}), db
}

func seedBOPreferenceUser(t *testing.T, db *sql.DB) (int, func()) {
	t.Helper()
	res, err := db.Exec(`INSERT INTO bo_users (email, name, password_hash) VALUES (?, ?, ?)`, "pref-test@example.com", "Pref Test", "x")
	if err != nil {
		t.Fatalf("insert bo_user: %v", err)
	}
	uid64, _ := res.LastInsertId()
	uid := int(uid64)
	return uid, func() { _, _ = db.Exec(`DELETE FROM bo_users WHERE id = ?`, uid) }
}

func TestValidBOPreference(t *testing.T) {
	cases := []struct {
		name            string
		key, value      string
		wantValid       bool
		wantNormalized  string
	}{
		{name: "tabla", key: "reservasDisplayMode", value: "tabla", wantValid: true, wantNormalized: "tabla"},
		{name: "grid", key: "reservasDisplayMode", value: "grid", wantValid: true, wantNormalized: "grid"},
		{name: "uppercase normalized", key: "reservasDisplayMode", value: "GRID", wantValid: true, wantNormalized: "grid"},
		{name: "unknown key", key: "theme", value: "dark", wantValid: false},
		{name: "unknown value", key: "reservasDisplayMode", value: "cards", wantValid: false},
		{name: "empty key", key: "", value: "grid", wantValid: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := normalizeBOPreference(c.key, c.value)
			if ok != c.wantValid {
				t.Fatalf("normalizeBOPreference(%q,%q) valid=%v want %v", c.key, c.value, ok, c.wantValid)
			}
			if ok && got != c.wantNormalized {
				t.Fatalf("normalizeBOPreference(%q,%q) norm=%q want %q", c.key, c.value, got, c.wantNormalized)
			}
		})
	}
}

func TestUserPreferences_RoundTrip(t *testing.T) {
	s, db := preferencesTestServer(t)
	uid, cleanupUser := seedBOPreferenceUser(t, db)
	rid, cleanupRest := seedRestaurant(t, db, "pref-rt")
	t.Cleanup(func() {
		cleanupUser()
		cleanupRest()
		_, _ = db.Exec(`DELETE FROM user_preferences WHERE user_id = ?`, uid)
	})
	ctx := context.Background()

	// Empty to start.
	prefs, err := s.getUserPreferences(ctx, uid, rid)
	if err != nil {
		t.Fatalf("getUserPreferences empty: %v", err)
	}
	if len(prefs) != 0 {
		t.Fatalf("expected empty prefs, got %v", prefs)
	}

	// Set grid.
	if err := s.setUserPreference(ctx, uid, rid, "reservasDisplayMode", "grid"); err != nil {
		t.Fatalf("setUserPreference: %v", err)
	}
	prefs, err = s.getUserPreferences(ctx, uid, rid)
	if err != nil {
		t.Fatalf("getUserPreferences after set: %v", err)
	}
	if prefs["reservasDisplayMode"] != "grid" {
		t.Fatalf("expected reservasDisplayMode=grid, got %v", prefs)
	}

	// Update to tabla (upsert, no duplicate row).
	if err := s.setUserPreference(ctx, uid, rid, "reservasDisplayMode", "tabla"); err != nil {
		t.Fatalf("setUserPreference update: %v", err)
	}
	prefs, err = s.getUserPreferences(ctx, uid, rid)
	if err != nil {
		t.Fatalf("getUserPreferences after update: %v", err)
	}
	if prefs["reservasDisplayMode"] != "tabla" {
		t.Fatalf("expected reservasDisplayMode=tabla, got %v", prefs)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_preferences WHERE user_id = ? AND restaurant_id = ?`, uid, rid).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row after upsert, got %d", n)
	}

	// Isolation: another restaurant must not see the preference.
	rid2, cleanupRest2 := seedRestaurant(t, db, "pref-rt2")
	t.Cleanup(cleanupRest2)
	prefs2, err := s.getUserPreferences(ctx, uid, rid2)
	if err != nil {
		t.Fatalf("getUserPreferences other restaurant: %v", err)
	}
	if len(prefs2) != 0 {
		t.Fatalf("expected isolation by restaurant, got %v", prefs2)
	}
}

func TestHandleBOPreferencesSet(t *testing.T) {
	s, db := preferencesTestServer(t)
	uid, cleanupUser := seedBOPreferenceUser(t, db)
	rid, cleanupRest := seedRestaurant(t, db, "pref-handler")
	t.Cleanup(func() {
		cleanupUser()
		cleanupRest()
		_, _ = db.Exec(`DELETE FROM user_preferences WHERE user_id = ?`, uid)
	})

	doSet := func(body string) (bool, map[string]string) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/me/preferences", strings.NewReader(body))
		ctx := withBOAuth(req.Context(), boAuth{User: boUser{ID: uid}, ActiveRestaurantID: rid})
		rec := httptest.NewRecorder()
		s.handleBOPreferencesSet(rec, req.WithContext(ctx))
		var out struct {
			Success     bool              `json:"success"`
			Preferences map[string]string `json:"preferences"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return out.Success, out.Preferences
	}

	// Valid upsert.
	ok, prefs := doSet(`{"key":"reservasDisplayMode","value":"grid"}`)
	if !ok || prefs["reservasDisplayMode"] != "grid" {
		t.Fatalf("valid set: ok=%v prefs=%v", ok, prefs)
	}

	// Invalid value rejected (no DB mutation).
	ok, _ = doSet(`{"key":"reservasDisplayMode","value":"cards"}`)
	if ok {
		t.Fatalf("invalid value unexpectedly accepted")
	}
	prefs, err := s.getUserPreferences(context.Background(), uid, rid)
	if err != nil {
		t.Fatalf("getUserPreferences: %v", err)
	}
	if prefs["reservasDisplayMode"] != "grid" {
		t.Fatalf("rejected write mutated DB: %v", prefs)
	}

	// Invalid key rejected.
	ok, _ = doSet(`{"key":"theme","value":"dark"}`)
	if ok {
		t.Fatalf("invalid key unexpectedly accepted")
	}

	// Malformed JSON rejected.
	req := httptest.NewRequest(http.MethodPut, "/api/admin/me/preferences", strings.NewReader(`{not json`))
	ctx := withBOAuth(req.Context(), boAuth{User: boUser{ID: uid}, ActiveRestaurantID: rid})
	rec := httptest.NewRecorder()
	s.handleBOPreferencesSet(rec, req.WithContext(ctx))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: status=%d want 400", rec.Code)
	}
}
