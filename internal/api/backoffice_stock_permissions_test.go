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

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/config"
)

// Step 8 splits technical-sheet authority out of the coarse stock.recipes.*
// permissions so a cook can edit steps without being able to publish a sheet
// or spend AI credits on images.

func TestStockSheetPermissionKeysAreRegistered(t *testing.T) {
	want := []string{
		"stock.sheets.view",
		"stock.sheets.manage",
		"stock.sheets.publish",
		"stock.sheets.delete",
		"stock.sheets.steps.manage",
		"stock.sheets.images.manage",
		"stock.sheets.images.ai",
		"comida.production_type.manage",
	}
	have := map[string]bool{}
	for _, key := range stockPermissionKeys {
		have[key] = true
	}
	for _, key := range want {
		if !have[key] {
			t.Fatalf("permission %q missing from stockPermissionKeys", key)
		}
	}
}

func TestStockPermissionKeysIncludeLegacyKeys(t *testing.T) {
	// The catalogue must stay a superset: removing an existing key would
	// silently drop a role override row on the next PUT.
	legacy := []string{
		stockPermissionView,
		stockPermissionItemsManage,
		stockPermissionWarehousesManage,
		stockPermissionAdjust,
		stockPermissionWasteRecord,
		stockPermissionTransfer,
		stockPermissionCountPerform,
		stockPermissionCountClose,
		stockPermissionRecipesView,
		stockPermissionRecipesManage,
		stockPermissionProduction,
		stockPermissionForecastView,
		stockPermissionOCRUpload,
		stockPermissionOCRConfirm,
		stockPermissionCostsView,
		stockPermissionCostsManage,
		stockPermissionSettingsManage,
	}
	have := map[string]bool{}
	for _, key := range stockPermissionKeys {
		have[key] = true
	}
	for _, key := range legacy {
		if !have[key] {
			t.Fatalf("legacy permission %q missing from stockPermissionKeys", key)
		}
	}
}

func TestStockPermissionKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, key := range stockPermissionKeys {
		if seen[key] {
			t.Fatalf("duplicate permission key %q", key)
		}
		seen[key] = true
	}
}

func TestStockPermissionKeysAreNamespaced(t *testing.T) {
	// Keys are persisted in stock_role_permissions.permission_key VARCHAR(64).
	for _, key := range stockPermissionKeys {
		if len(key) > 64 {
			t.Fatalf("permission key %q exceeds the VARCHAR(64) column", key)
		}
		if !strings.HasPrefix(key, "stock.") && !strings.HasPrefix(key, "comida.") {
			t.Fatalf("permission key %q is not namespaced", key)
		}
	}
}

// --- HTTP role-permission catalogue (requires a real schema) ---

func stockPermsTestServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM stock_role_permissions WHERE restaurant_id=1`)
		db.Close()
	})
	if _, err := db.Exec(`INSERT IGNORE INTO restaurants(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	return NewServer(db, config.Config{})
}

func stockPermsRequest(t *testing.T, method, path, slug, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("slug", slug)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(withBOAuth(ctx, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
}

func TestStockRolePermissionsGetDefaultsByRole(t *testing.T) {
	s := stockPermsTestServer(t)

	for _, tc := range []struct {
		role    string
		allowed bool
	}{
		{"admin", true},
		{"root", true},
		{"staff", false},
	} {
		rec := httptest.NewRecorder()
		s.handleBOStockRolePermissionsGet(rec, stockPermsRequest(t, "GET", "/stock/roles/"+tc.role+"/permissions", tc.role, ""))
		if rec.Code != 200 {
			t.Fatalf("role %s: status %d body %s", tc.role, rec.Code, rec.Body.String())
		}
		var out struct {
			Permissions []struct {
				Key     string `json:"key"`
				Allowed bool   `json:"allowed"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Permissions) != len(stockPermissionKeys) {
			t.Fatalf("role %s: got %d keys want %d", tc.role, len(out.Permissions), len(stockPermissionKeys))
		}
		for _, p := range out.Permissions {
			if p.Allowed != tc.allowed {
				t.Fatalf("role %s key %s: allowed=%v want %v", tc.role, p.Key, p.Allowed, tc.allowed)
			}
		}
	}
}

func TestStockRolePermissionsPutPersistsExactSelection(t *testing.T) {
	s := stockPermsTestServer(t)

	body := `{"permissions":["stock.view","stock.sheets.view","stock.sheets.steps.manage"]}`
	rec := httptest.NewRecorder()
	s.handleBOStockRolePermissionsPut(rec, stockPermsRequest(t, "PUT", "/stock/roles/cocina/permissions", "cocina", body))
	if rec.Code != 200 {
		t.Fatalf("put status %d body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleBOStockRolePermissionsGet(rec, stockPermsRequest(t, "GET", "/stock/roles/cocina/permissions", "cocina", ""))
	var out struct {
		Permissions []struct {
			Key     string `json:"key"`
			Allowed bool   `json:"allowed"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	granted := map[string]bool{}
	for _, p := range out.Permissions {
		granted[p.Key] = p.Allowed
	}
	for _, key := range []string{"stock.view", "stock.sheets.view", "stock.sheets.steps.manage"} {
		if !granted[key] {
			t.Fatalf("expected %q granted", key)
		}
	}
	// A cook who may edit steps must NOT inherit publish/delete/AI authority.
	for _, key := range []string{"stock.sheets.publish", "stock.sheets.delete", "stock.sheets.images.ai"} {
		if granted[key] {
			t.Fatalf("expected %q denied", key)
		}
	}
}

func TestStockRolePermissionsPutRejectsUnknownRole(t *testing.T) {
	s := stockPermsTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOStockRolePermissionsPut(rec, stockPermsRequest(t, "PUT", "/stock/roles//permissions", "", `{"permissions":[]}`))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

func TestStockRolePermissionsPutIgnoresUnknownKeys(t *testing.T) {
	s := stockPermsTestServer(t)
	body := `{"permissions":["stock.view","stock.totally.made.up"]}`
	rec := httptest.NewRecorder()
	s.handleBOStockRolePermissionsPut(rec, stockPermsRequest(t, "PUT", "/stock/roles/cocina/permissions", "cocina", body))
	if rec.Code != 200 {
		t.Fatalf("put status %d body %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM stock_role_permissions WHERE restaurant_id=1 AND role_slug='cocina' AND permission_key='stock.totally.made.up'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("unknown key was persisted (%d rows)", n)
	}
}
