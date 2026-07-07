package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestLegalPagesMigrationApplied asserts the 051_legal_pages migration seeded
// the three placeholder rows for restaurant 1. Requires TEST_DB_DSN pointing at
// a DB where migrations have been applied (skipped otherwise).
func TestLegalPagesMigrationApplied(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}

	rows, err := db.Query("SELECT slug, title FROM legal_pages WHERE restaurant_id=1 ORDER BY slug")
	if err != nil {
		t.Fatalf("query legal_pages: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var slug, title string
		if err := rows.Scan(&slug, &title); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[slug] = title
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	want := map[string]string{
		"aviso-legal":      "Aviso Legal",
		"booking-policies": "Políticas de Reserva",
		"proteccion-datos": "Protección de Datos",
	}
	for slug, title := range want {
		if got[slug] != title {
			t.Errorf("slug %q: got title %q, want %q", slug, got[slug], title)
		}
	}
}

// chiRequestWithSlug builds a request carrying a chi route param {slug}.
func chiRequestWithSlug(method, target, slug, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withAdmin injects a boAuth session for the active restaurant into the request.
func withAdmin(r *http.Request, restaurantID, userID int) *http.Request {
	a := boAuth{ActiveRestaurantID: restaurantID, User: boUser{ID: userID}}
	return r.WithContext(withBOAuth(r.Context(), a))
}

// TestPublicLegalPageSeedHasNoAsistencia inserts a booking-policies row with the
// expected substrings and asserts the public handler returns them.
func TestPublicLegalPageSeedHasNoAsistencia(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	s := newTestServer(t, db)

	const html = `<h2>IV. Política de No Asistencia (No-Show)</h2><p>...</p><h2>VI. Reserva de Arroces</h2>`
	if _, err := db.Exec(`
		INSERT INTO legal_pages (restaurant_id, slug, title, content_json, content_html, updated_by_user_id)
		VALUES (1, 'booking-policies', 'Políticas de Reserva', '[]', ?, NULL)
		ON DUPLICATE KEY UPDATE content_html = VALUES(content_html), content_json = VALUES(content_json)
	`, html); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	r := httptest.NewRequest("GET", "/api/public/legal-page?slug=booking-policies", nil)
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicLegalPageGet(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeJSON(t, w.Body.Bytes())
	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v", resp["success"])
	}
	content, _ := resp["contentHtml"].(string)
	if !strings.Contains(content, "No Asistencia") {
		t.Error("contentHtml should mention No-Show policy")
	}
	if !strings.Contains(content, "Arroces") {
		t.Error("contentHtml should mention Arroces section")
	}
}

// TestPublicLegalPageInvalidSlug rejects unknown slugs.
func TestPublicLegalPageInvalidSlug(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	s := newTestServer(t, db)
	r := httptest.NewRequest("GET", "/api/public/legal-page?slug=bogus", nil)
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicLegalPageGet(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid slug, got %d", w.Code)
	}
}

// TestAdminLegalPageUpsertAuth: POST without a session returns 401.
func TestAdminLegalPageUpsertAuth(t *testing.T) {
	s := &Server{}
	r := chiRequestWithSlug("POST", "/api/admin/legal-pages/aviso-legal", "aviso-legal", `{"title":"X","contentJson":"[]","contentHtml":"<p>x</p>"}`)
	w := httptest.NewRecorder()
	s.handleAdminLegalPageUpsert(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without session, got %d", w.Code)
	}
}

// TestAdminLegalPageUpsertRoundTrip: POST then GET returns the same values.
func TestAdminLegalPageUpsertRoundTrip(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Skipf("test DB unreachable: %v", err)
	}
	s := newTestServer(t, db)

	const (
		wantTitle = "Aviso Legal RoundTrip"
		wantHTML  = "<p>roundtrip body</p>"
		wantJSON  = `[{"type":"paragraph"}]`
	)

	// Restore the seeded title afterwards so the migration test stays green.
	t.Cleanup(func() {
		_, _ = db.Exec(`UPDATE legal_pages SET title='Aviso Legal', content_json='[]', content_html='<p>Placeholder</p>', updated_by_user_id=NULL WHERE restaurant_id=1 AND slug='aviso-legal'`)
	})

	body, _ := json.Marshal(map[string]string{"title": wantTitle, "contentJson": wantJSON, "contentHtml": wantHTML})
	rPost := chiRequestWithSlug("POST", "/api/admin/legal-pages/aviso-legal", "aviso-legal", string(body))
	rPost = withAdmin(rPost, 1, 0)
	wPost := httptest.NewRecorder()
	s.handleAdminLegalPageUpsert(wPost, rPost)
	if wPost.Code != http.StatusOK {
		t.Fatalf("upsert: expected 200, got %d (%s)", wPost.Code, wPost.Body.String())
	}

	rGet := chiRequestWithSlug("GET", "/api/admin/legal-pages/aviso-legal", "aviso-legal", "")
	rGet = withAdmin(rGet, 1, 0)
	wGet := httptest.NewRecorder()
	s.handleAdminLegalPageGet(wGet, rGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d (%s)", wGet.Code, wGet.Body.String())
	}
	resp := decodeJSON(t, wGet.Body.Bytes())
	page, _ := resp["page"].(map[string]any)
	if page == nil {
		t.Fatalf("missing page in response: %s", wGet.Body.String())
	}
	if page["title"] != wantTitle {
		t.Errorf("title: got %v, want %q", page["title"], wantTitle)
	}
	if page["contentHtml"] != wantHTML {
		t.Errorf("contentHtml: got %v, want %q", page["contentHtml"], wantHTML)
	}
	if page["contentJson"] != wantJSON {
		t.Errorf("contentJson: got %v, want %q", page["contentJson"], wantJSON)
	}
}
