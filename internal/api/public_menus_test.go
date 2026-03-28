package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"

	"preactvillacarmen/internal/config"
)

// testDB returns a *sql.DB from TEST_DB_DSN env var, or skips the test.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set, skipping integration test")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return db
}

// newTestServer creates a Server suitable for testing with the given DB.
func newTestServer(t *testing.T, db *sql.DB) *Server {
	t.Helper()
	return NewServer(db, config.Config{
		BunnyPullBaseURL: "https://example.b-cdn.net",
	})
}

// testRouter creates a chi.Mux with the withRestaurant middleware using a fixed restaurantID.
func testRouter(t *testing.T, srv *Server, restaurantID int) *chi.Mux {
	t.Helper()
	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), restaurantIDKey, restaurantID)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	// Register the new routes. Static paths before parametric.
	r.Get("/menus/sidebar", srv.handlePublicMenusSidebar)
	r.Get("/menus/home", srv.handlePublicMenusHome)
	r.Get("/menus/{menuID}", srv.handlePublicMenuByRouteID)
	// Keep legacy route for backward-compat check.
	r.Get("/menus/public", srv.handlePublicMenus)
	return r
}

func doGET(t *testing.T, router *chi.Mux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode json: %v\nbody: %s", err, string(body))
	}
	return result
}

// ──────────────────────────────────────────────
// RED: Tests for GET /menus/{menuID}
// ──────────────────────────────────────────────

func TestMenuByID_Success(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	// Find a real active menu ID from the DB.
	var menuID int64
	err := db.QueryRow(`SELECT id FROM menus WHERE restaurant_id = 1 AND active = 1 AND is_draft = 0 LIMIT 1`).Scan(&menuID)
	if err != nil {
		t.Skipf("no active menu in test DB: %v", err)
	}

	rec := doGET(t, router, fmt.Sprintf("/menus/%d", menuID))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] != true {
		t.Fatalf("expected success=true, got %v", body)
	}
	menuRaw, ok := body["menu"]
	if !ok {
		t.Fatal("expected 'menu' key in response")
	}
	menu, ok := menuRaw.(map[string]any)
	if !ok {
		t.Fatal("expected 'menu' to be an object")
	}
	if menu["id"] == nil {
		t.Fatal("expected menu.id to be present")
	}
	// Must have heavy fields: sections, entrantes, principales, postre, settings.
	for _, key := range []string{"sections", "entrantes", "principales", "postre", "settings"} {
		if _, found := menu[key]; !found {
			t.Errorf("expected menu.%s to be present", key)
		}
	}
}

func TestMenuByID_NotFound(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/999999999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] == true {
		t.Fatal("expected success=false for not found")
	}
}

func TestMenuByID_InvalidID(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/abc")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] == true {
		t.Fatal("expected success=false for invalid id")
	}
}

// ──────────────────────────────────────────────
// RED: Tests for GET /menus/sidebar
// ──────────────────────────────────────────────

func TestMenuSidebar_Success(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/sidebar")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] != true {
		t.Fatalf("expected success=true, got %v", body)
	}

	menusRaw, ok := body["menus"]
	if !ok {
		t.Fatal("expected 'menus' key")
	}
	menus, ok := menusRaw.([]any)
	if !ok {
		t.Fatal("expected 'menus' to be an array")
	}

	if len(menus) == 0 {
		t.Skip("no active menus in test DB, cannot verify sidebar fields")
	}

	// Verify each menu item only has the expected lightweight fields.
	expectedFields := map[string]bool{
		"id": true, "menu_title": true, "menu_type": true, "slug": true, "active": true, "legacy_source_table": true,
	}
	for i, item := range menus {
		menu, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("menus[%d] is not an object", i)
		}
		for key := range menu {
			if !expectedFields[key] {
				t.Errorf("menus[%d]: unexpected field %q in sidebar response", i, key)
			}
		}
		for field := range expectedFields {
			if field == "legacy_source_table" {
				continue // omitempty — may be absent when empty
			}
			if _, found := menu[field]; !found {
				t.Errorf("menus[%d]: missing expected field %q", i, field)
			}
		}
	}
}

// ──────────────────────────────────────────────
// RED: Tests for GET /menus/home
// ──────────────────────────────────────────────

func TestMenuHome_Success(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/home")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] != true {
		t.Fatalf("expected success=true, got %v", body)
	}

	menusRaw, ok := body["menus"]
	if !ok {
		t.Fatal("expected 'menus' key")
	}
	menus, ok := menusRaw.([]any)
	if !ok {
		t.Fatal("expected 'menus' to be an array")
	}

	if len(menus) == 0 {
		t.Skip("no active menus in test DB, cannot verify home fields")
	}

	// Verify each menu has the expected home fields and NO heavy fields.
	expectedFields := map[string]bool{
		"id": true, "slug": true, "menu_title": true, "menu_type": true,
		"active": true, "menu_subtitle": true,
		"show_menu_preview_image": true, "menu_preview_image_url": true,
	}
	forbiddenFields := []string{"sections", "dishes", "entrantes", "principales", "postre", "settings", "price", "comments"}
	for i, item := range menus {
		menu, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("menus[%d] is not an object", i)
		}
		for _, forbidden := range forbiddenFields {
			if _, found := menu[forbidden]; found {
				t.Errorf("menus[%d]: forbidden field %q present in home response", i, forbidden)
			}
		}
		for field := range expectedFields {
			if _, found := menu[field]; !found {
				t.Errorf("menus[%d]: missing expected field %q", i, field)
			}
		}
	}
}

// ──────────────────────────────────────────────
// Legacy compat: ensure old endpoint still works
// ──────────────────────────────────────────────

func TestLegacyPublicMenus_StillWorks(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/public?home_page=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := decodeJSON(t, rec.Body.Bytes())
	if body["success"] != true {
		t.Fatalf("expected success=true, got %v", body)
	}
	if _, ok := body["menus"]; !ok {
		t.Fatal("expected 'menus' key in legacy response")
	}
}

// ──────────────────────────────────────────────
// Integration: verify route priority (sidebar/home vs {menuID})
// ──────────────────────────────────────────────

func TestRoutePriority_SidebarIsNotTreatedAsMenuID(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/sidebar")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if _, ok := body["menus"]; !ok {
		t.Fatal("sidebar endpoint should return 'menus' array, got something else (probably treated as menuID)")
	}
}

func TestRoutePriority_HomeIsNotTreatedAsMenuID(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	rec := doGET(t, router, "/menus/home")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if _, ok := body["menus"]; !ok {
		t.Fatal("home endpoint should return 'menus' array, got something else (probably treated as menuID)")
	}
}

// normalizeV2MenuType is needed in this package; this test ensures the import works.
// The actual helpers are unexported so they're accessible within the package.

// ──────────────────────────────────────────────
// Unit: handlePublicMenuByRouteID extracts menuID from chi URL param
// ──────────────────────────────────────────────

func TestMenuByID_SpecialMenu_ReturnsMinimalResponse(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	srv := newTestServer(t, db)
	router := testRouter(t, srv, 1)

	// Find a special menu.
	var menuID int64
	err := db.QueryRow(`SELECT id FROM menus WHERE restaurant_id = 1 AND active = 1 AND is_draft = 0 AND COALESCE(NULLIF(TRIM(menu_type),''),'closed_conventional') = 'special' LIMIT 1`).Scan(&menuID)
	if err != nil {
		t.Skipf("no special menu in test DB: %v", err)
	}

	rec := doGET(t, router, fmt.Sprintf("/menus/%d", menuID))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec.Body.Bytes())
	menu, ok := body["menu"].(map[string]any)
	if !ok {
		t.Fatal("expected 'menu' key")
	}
	// Special menus should have minimal fields.
	if menu["menu_title"] == nil {
		t.Error("expected menu_title in special menu response")
	}
}

// helper for string assertion
func containsString(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected body to contain %q", needle)
	}
}
