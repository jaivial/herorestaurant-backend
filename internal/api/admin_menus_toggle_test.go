package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// toggleDishStatusRouter registers the two toggle handlers the way server.go
// does, with the restaurant id injected by the withRestaurant middleware.
func toggleDishStatusRouter(srv *Server, restaurantID int) *chi.Mux {
	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), restaurantIDKey, restaurantID)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/toggleDishStatusDia.php", srv.handleToggleDishStatusDia)
	r.Post("/toggleDishStatus.php", srv.handleToggleDishStatusGeneric)
	return r
}

func postToggle(t *testing.T, r *chi.Mux, path string, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// activeOf returns the active flag of a v2 dish, or -1 when it is missing.
func activeOf(t *testing.T, db *sql.DB, v2ID int64) int {
	t.Helper()
	var active int
	err := db.QueryRow(`SELECT active FROM group_menu_section_dishes_v2 WHERE id = ?`, v2ID).Scan(&active)
	if err != nil {
		t.Fatalf("read active of v2 dish %d: %v", v2ID, err)
	}
	return active
}

// TestToggleDishStatusDiaWritesToV2 proves the toggle handler updates the v2
// row. It used to write to the legacy DIA table, which no longer exists.
func TestToggleDishStatusDiaWritesToV2(t *testing.T) {
	db := testDB(t)
	srv := newTestServer(t, db)
	r := toggleDishStatusRouter(srv, 1)

	// Find a legacy dish that is mapped to a v2 row.
	var legacyNum, v2ID int64
	err := db.QueryRow(`
		SELECT m.legacy_num, m.v2_dish_id
		  FROM legacy_dish_map m
		  JOIN group_menu_section_dishes_v2 d ON d.id = m.v2_dish_id
		 WHERE m.restaurant_id = 1 AND m.legacy_table = 'DIA' AND m.v2_dish_id IS NOT NULL
		 LIMIT 1`).Scan(&legacyNum, &v2ID)
	if err != nil {
		t.Skipf("no mapped legacy DIA dish available: %v", err)
	}

	before := activeOf(t, db, v2ID)
	want := 0
	if before == 0 {
		want = 1
	}

	code, body := postToggle(t, r, "/toggleDishStatusDia.php", url.Values{
		"dishId":   {strconv.Itoa(int(legacyNum))},
		"isActive": {strconv.FormatBool(want == 1)},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("success = false, body = %v", body)
	}
	if got := activeOf(t, db, v2ID); got != want {
		t.Fatalf("active = %d, want %d (legacy_num %d -> v2 %d)", got, want, legacyNum, v2ID)
	}

	// Restore the original value so the test leaves no trace.
	postToggle(t, r, "/toggleDishStatusDia.php", url.Values{
		"dishId":   {strconv.Itoa(int(legacyNum))},
		"isActive": {strconv.FormatBool(before == 1)},
	})
	if got := activeOf(t, db, v2ID); got != before {
		t.Fatalf("restore failed: active = %d, want %d", got, before)
	}
}

// TestToggleDishStatusGenericWritesToV2 proves the generic handler (which used
// to write to the FINDE and POSTRES tables, both gone) updates the v2 row
// resolved through legacy_dish_map.
func TestToggleDishStatusGenericWritesToV2(t *testing.T) {
	db := testDB(t)
	srv := newTestServer(t, db)
	r := toggleDishStatusRouter(srv, 1)

	var legacyNum, v2ID int64
	err := db.QueryRow(`
		SELECT m.legacy_num, m.v2_dish_id
		  FROM legacy_dish_map m
		  JOIN group_menu_section_dishes_v2 d ON d.id = m.v2_dish_id
		 WHERE m.restaurant_id = 1 AND m.legacy_table = 'FINDE' AND m.v2_dish_id IS NOT NULL
		 LIMIT 1`).Scan(&legacyNum, &v2ID)
	if err != nil {
		t.Skipf("no mapped legacy FINDE dish available: %v", err)
	}

	before := activeOf(t, db, v2ID)
	want := 0
	if before == 0 {
		want = 1
	}

	code, body := postToggle(t, r, "/toggleDishStatus.php", url.Values{
		"dishId":   {strconv.Itoa(int(legacyNum))},
		"table":    {"FINDE"},
		"isActive": {strconv.FormatBool(want == 1)},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("success = false, body = %v", body)
	}
	if got := activeOf(t, db, v2ID); got != want {
		t.Fatalf("active = %d, want %d (legacy_num %d -> v2 %d)", got, want, legacyNum, v2ID)
	}

	postToggle(t, r, "/toggleDishStatus.php", url.Values{
		"dishId":   {strconv.Itoa(int(legacyNum))},
		"table":    {"FINDE"},
		"isActive": {strconv.FormatBool(before == 1)},
	})
	if got := activeOf(t, db, v2ID); got != before {
		t.Fatalf("restore failed: active = %d, want %d", got, before)
	}
}

// TestToggleDishStatusRejectsUnknownTable keeps the allow-list behaviour.
func TestToggleDishStatusRejectsUnknownTable(t *testing.T) {
	db := testDB(t)
	srv := newTestServer(t, db)
	r := toggleDishStatusRouter(srv, 1)

	code, body := postToggle(t, r, "/toggleDishStatus.php", url.Values{
		"dishId":   {"1"},
		"table":    {"DROP_TABLE"},
		"isActive": {"true"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %v", code, body)
	}
	if ok, _ := body["success"].(bool); ok {
		t.Fatalf("an unknown table was accepted: %v", body)
	}
}
