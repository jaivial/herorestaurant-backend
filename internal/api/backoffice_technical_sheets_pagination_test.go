package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// The sheets grid pages server-side like the stock items grid: pageSize caps
// the window, total/totalPages tell the pager where the end is, and the name
// ordering must stay stable from one page to the next.
func TestSheetListPaginationWindowsAndCounts(t *testing.T) {
	s := sheetsTestServer(t)
	for _, name := range []string{"Ficha Alfa", "Ficha Beta", "Ficha Gamma"} {
		createSheet(t, s, name)
	}

	read := func(url string) (total, totalPages, page int, names []string) {
		rec := httptest.NewRecorder()
		s.handleBOTechnicalSheetList(rec, sheetReq("GET", url, "", nil))
		if rec.Code != 200 {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Sheets []struct {
				Name string `json:"name"`
			} `json:"sheets"`
			Total      int `json:"total"`
			TotalPages int `json:"totalPages"`
			Page       int `json:"page"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		for _, sheet := range out.Sheets {
			names = append(names, sheet.Name)
		}
		return out.Total, out.TotalPages, out.Page, names
	}

	total, totalPages, _, names := read("/comida/technical-sheets?pageSize=2")
	var page int
	if total != 3 || totalPages != 2 {
		t.Fatalf("metadata total=%d totalPages=%d, want 3/2", total, totalPages)
	}
	if len(names) != 2 || names[0] != "Ficha Alfa" || names[1] != "Ficha Beta" {
		t.Fatalf("first page returned %+v, want [Ficha Alfa Ficha Beta]", names)
	}

	total, totalPages, page, names = read("/comida/technical-sheets?pageSize=2&page=2")
	if page != 2 || total != 3 || totalPages != 2 {
		t.Fatalf("second page metadata page=%d total=%d totalPages=%d, want 2/3/2", page, total, totalPages)
	}
	if len(names) != 1 || names[0] != "Ficha Gamma" {
		t.Fatalf("second page returned %+v, want [Ficha Gamma]", names)
	}

	total, _, _, names = read("/comida/technical-sheets?pageSize=2&page=99")
	if total != 3 || len(names) != 0 {
		t.Fatalf("past-the-end page returned total=%d names=%+v, want total=3 and no rows", total, names)
	}

	// No params: the historical behaviour (first 100) plus the new metadata.
	total, totalPages, _, names = read("/comida/technical-sheets")
	if total != 3 || totalPages != 1 || len(names) != 3 {
		t.Fatalf("default list total=%d totalPages=%d names=%d, want 3/1/3", total, totalPages, len(names))
	}
}

// The switcher state (show images on the sheets grid) is stored per user and
// active restaurant, and the list response carries it back so the client
// hydrates the switch without a second request.
func TestSheetListCarriesThePagePreferences(t *testing.T) {
	s := sheetsTestServer(t)
	createSheet(t, s, "Ficha con preferencia")

	// user_preferences carries an FK to bo_users, and the auth fixture in
	// sheetReq speaks as user 7.
	if _, err := s.db.Exec(
		`INSERT INTO bo_users (id, email, name, password_hash) VALUES (7, 'sheets-pref@test.local', 'Prefencia', 'testhash')
		 ON DUPLICATE KEY UPDATE email=VALUES(email)`); err != nil {
		t.Fatal(err)
	}
	if err := s.setUserPreference(context.Background(), 7, 1, "stockSheetsShowImages", "0"); err != nil {
		t.Fatalf("setUserPreference: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.db.Exec(`DELETE FROM user_preferences WHERE user_id=7 AND restaurant_id=1`)
	})

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetList(rec, sheetReq("GET", "/comida/technical-sheets", "", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Preferences map[string]string `json:"preferences"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Preferences["stockSheetsShowImages"] != "0" {
		t.Fatalf("preferences = %+v, want stockSheetsShowImages=0", out.Preferences)
	}
}

// The WebSocket search pages with the same filter set as REST, so the two
// transports can never disagree about what a search matches.
func TestSheetSearchPaginatesAndFiltersLikeREST(t *testing.T) {
	s := sheetsTestServer(t)
	for _, name := range []string{"Sopa A", "Sopa B", "Sopa C"} {
		createSheet(t, s, name)
	}
	// ACTIVE is what the publish handler writes; the API filter says PUBLISHED
	// and maps onto it.
	if _, err := s.db.Exec(`UPDATE stock_recipes SET status='ACTIVE' WHERE restaurant_id=1 AND name='Sopa A'`); err != nil {
		t.Fatal(err)
	}

	sheets, total, _, _, err := s.searchSheets(context.Background(), 1, "Sopa", "", 0, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(sheets) != 2 {
		t.Fatalf("first window len=%d total=%d, want 2/3", len(sheets), total)
	}

	sheets, total, _, _, err = s.searchSheets(context.Background(), 1, "Sopa", "", 0, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(sheets) != 1 {
		t.Fatalf("second window len=%d total=%d, want 1/3", len(sheets), total)
	}

	// Zero page/pageSize: the historical LIMIT 25 window. searchSheets now
	// returns the clamped page/pageSize it used, which would be (1, 25).
	sheets, total, page, pageSize, err := s.searchSheets(context.Background(), 1, "", "", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page != 1 || pageSize != 25 {
		t.Fatalf("default window page=%d pageSize=%d, want 1/25", page, pageSize)
	}
	if total != 3 || len(sheets) != 3 {
		t.Fatalf("default window len=%d total=%d, want 3/3", len(sheets), total)
	}

	sheets, total, _, _, err = s.searchSheets(context.Background(), 1, "", "PUBLISHED", 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(sheets) != 1 || sheets[0]["name"] != "Sopa A" {
		t.Fatalf("status filter len=%d total=%d sheets=%+v, want only Sopa A", len(sheets), total, sheets)
	}
}
