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

// These tests exercise the margin-scope HTTP handlers against a real MySQL
// schema. Set MIGRATIONS_TEST_MYSQL_DSN to a THROWAWAY database.
func marginScopesTestServer(t *testing.T) (*Server, int) {
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
		db.Exec(`DELETE FROM stock_margin_scopes WHERE restaurant_id=1`)
		db.Close()
	})
	s := NewServer(db, config.Config{BunnyPrivateStorageZone: "private-zone"})
	// Ensure a restaurant exists.
	if _, err := db.Exec(`INSERT IGNORE INTO restaurants(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	return s, 1
}

func marginScopeRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strReader(body))
	routeCtx := chi.NewRouteContext()
	if p := path; len(p) > 0 {
		// capture last segment as :id for delete
	}
	_ = routeCtx
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(withBOAuth(ctx, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
}

func strReader(s string) *strings.Reader { return strings.NewReader(s) }

func TestMarginScopePutResolveRoundTrip(t *testing.T) {
	s, _ := marginScopesTestServer(t)

	putBody := `{"scopeKind":"GLOBAL","label":"Global VC","bands":[
		{"zone":"PURPLE","max":25},
		{"zone":"GREEN","min":25,"max":35},
		{"zone":"AMBER","min":35,"max":40},
		{"zone":"RED","min":40}
	]}`
	rec := httptest.NewRecorder()
	s.handleBOStockMarginScopePut(rec, marginScopeRequest(http.MethodPut, "/stock/margin-scopes", putBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT GLOBAL status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Resolve a category with no specific scope -> should fall back to the
	// configured GLOBAL, source=configured, level=GLOBAL.
	rec = httptest.NewRecorder()
	s.handleBOStockMarginScopeResolve(rec, marginScopeRequest(http.MethodGet, "/stock/margin-scopes/resolve?scopeKind=COMIDA_CATEGORY&foodType=platos&categoryId=3", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Source string            `json:"source"`
		Level  string            `json:"level"`
		Bands  []stockMarginBand `json:"bands"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Source != "configured" || res.Level != "GLOBAL" {
		t.Fatalf("expected configured/GLOBAL, got source=%s level=%s", res.Source, res.Level)
	}
	if len(res.Bands) != 4 {
		t.Fatalf("expected 4 bands, got %d", len(res.Bands))
	}
}

func TestMarginScopePutRejectsGap(t *testing.T) {
	s, _ := marginScopesTestServer(t)
	// GREEN max 33 leaves a gap to AMBER min 35 -> must be 400.
	body := `{"scopeKind":"GLOBAL","label":"Bad","bands":[
		{"zone":"PURPLE","max":25},
		{"zone":"GREEN","min":25,"max":33},
		{"zone":"AMBER","min":35,"max":40},
		{"zone":"RED","min":40}
	]}`
	rec := httptest.NewRecorder()
	s.handleBOStockMarginScopePut(rec, marginScopeRequest(http.MethodPut, "/stock/margin-scopes", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("gap must be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMarginScopeDefaultsEndpoint(t *testing.T) {
	s, _ := marginScopesTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOStockMarginScopeDefaults(rec, marginScopeRequest(http.MethodGet, "/stock/margin-scopes/defaults", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var res struct {
		Bands []stockMarginBand `json:"bands"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Bands) != 4 || res.Bands[0].Zone != "PURPLE" || res.Bands[3].Zone != "RED" {
		t.Fatalf("unexpected defaults: %+v", res.Bands)
	}
}
