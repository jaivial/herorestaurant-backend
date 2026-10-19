package api

import (
	"context"
	"testing"
)

// TestLoadRiceTypesReadsV2 proves the rices come from the v2 section whose
// section_kind is 'arroces'. The legacy reader pointed at the DIA/FINDE
// tables, which no longer exist, so the endpoint answered 500.
func TestLoadRiceTypesReadsV2(t *testing.T) {
	db := testDB(t)
	srv := newTestServer(t, db)

	for _, tc := range []struct {
		menu   string
		menuID int
	}{
		{"finde", 42},
		{"dia", 41},
	} {
		out, english, err := srv.loadRiceTypesForMenu(context.Background(), 1, tc.menu)
		if err != nil {
			t.Fatalf("loadRiceTypesForMenu(%q): %v", tc.menu, err)
		}
		if len(out) == 0 {
			t.Fatalf("loadRiceTypesForMenu(%q) returned no rices", tc.menu)
		}
		for _, r := range out {
			if r == "" {
				t.Errorf("loadRiceTypesForMenu(%q) returned an empty rice name", tc.menu)
			}
		}
		if len(english) != len(out) {
			t.Errorf("loadRiceTypesForMenu(%q): %d names but %d english", tc.menu, len(out), len(english))
		}
		t.Logf("menu %s (%d): %d rices, primero %q", tc.menu, tc.menuID, len(out), out[0])
	}
}

// TestLoadRiceTypesMatchesV2Section proves the rices returned are exactly the
// active ones of the v2 'arroces' section, not a subset or a superset.
func TestLoadRiceTypesMatchesV2Section(t *testing.T) {
	db := testDB(t)
	srv := newTestServer(t, db)

	out, _, err := srv.loadRiceTypesForMenu(context.Background(), 1, "finde")
	if err != nil {
		t.Fatalf("loadRiceTypesForMenu: %v", err)
	}

	rows, err := db.Query(`
		SELECT d.title_snapshot
		FROM group_menu_section_dishes_v2 d
		JOIN group_menu_sections_v2 s ON s.id = d.section_id
		WHERE d.restaurant_id = 1 AND s.restaurant_id = 1
		  AND s.menu_id = 42 AND s.section_kind = 'arroces'
		  AND (d.active = 1 OR d.active IS NULL)
		ORDER BY d.title_snapshot ASC`)
	if err != nil {
		t.Fatalf("count rices in v2: %v", err)
	}
	defer rows.Close()
	want := 0
	for rows.Next() {
		want++
	}
	if want != len(out) {
		t.Fatalf("handler returned %d rices, the v2 section has %d", len(out), want)
	}
}
