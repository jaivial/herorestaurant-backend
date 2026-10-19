package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Regression test for the rice duplication bug.
//
// Coordination id: menu_legacy_snapshot_skip_nonconventional_v1
//
// syncBOMenuV2LegacySnapshot rebuilds the three conventional blocks of the legacy
// `menus` row (entrantes / principales / postres) from the v2 sections. Its
// switch used to send every kind that was not postres/principales into
// `entrantes`, so the "arroces" section was copied into the starters array as
// bare text on every single save. The operator saw the rice twice: once in its
// own section and once, as plain text, inside Entrantes -- and those copies never
// gained allergens, supplement price or dish id, because the legacy column only
// stores strings.
//
// This test pins the invariant: only the kind the legacy column actually models
// may be written into it. The rice stays in its own section, untouched.

func storedEntrantes(t *testing.T, s *Server, menuID int) []string {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT entrantes FROM menus WHERE id = ? AND restaurant_id = ?`,
		menuID, arrozTestRestaurant,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		items, ok := decodeJSONOrFallback(raw, []any{}).([]any)
		if !ok {
			t.Fatalf("entrantes is not an array: %s", raw)
		}
		out = anySliceToStringList(items)
	}
	return out
}

// seedArrozDishesSectionWithStarters gives one menu an entrantes section with two
// real starters and an arroces section with three rice dishes, which is the
// layout every group menu of this restaurant has.
func seedArrozAndStartersSections(t *testing.T, s *Server, menuID int, starters []string, rice []string) {
	t.Helper()
	seedArrozSectionWithKind(t, s, menuID, "entrantes", starters...)
	seedArrozSectionWithKind(t, s, menuID, "arroces", rice...)
}

// seedArrozSectionWithKind is seedArrozSection with a caller-chosen kind: the
// existing helper hardcodes "arroces" so it cannot build the starters block.
func seedArrozSectionWithKind(t *testing.T, s *Server, menuID int, kind string, dishes ...string) {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
		 VALUES (?, ?, ?, ?, 0)`,
		arrozTestRestaurant, menuID, strings.Title(strings.ToLower(kind)), kind,
	)
	if err != nil {
		t.Fatal(err)
	}
	sectionID, _ := res.LastInsertId()
	for i, name := range dishes {
		if _, err := s.db.Exec(
			`INSERT INTO group_menu_section_dishes_v2
			 (restaurant_id, menu_id, section_id, title_snapshot, position, active)
			 VALUES (?, ?, ?, ?, ?, 1)`,
			arrozTestRestaurant, menuID, sectionID, name, i,
		); err != nil {
			t.Fatal(err)
		}
	}
}

// syncTheLegacySnapshot runs the exact write path a dish save triggers.
func syncTheLegacySnapshot(t *testing.T, s *Server, menuID int) {
	t.Helper()
	req := categoryReq(http.MethodPut, "/group-menus-v2/"+strconv.Itoa(menuID), "",
		arrozTestRestaurant, map[string]string{"id": strconv.Itoa(menuID)})
	if err := s.syncBOMenuV2LegacySnapshot(req, arrozTestRestaurant, int64(menuID)); err != nil {
		t.Fatalf("legacy snapshot sync: %v", err)
	}
}

func TestLegacySnapshotKeepsRiceOutOfEntrantes(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú con arroz", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []string{},
	})

	starters := []string{"Croquetas caseras", "Ensalada de pollo"}
	rice := []string{"Arroz a banda", "Arroz meloso de pato", "Paella Valenciana"}
	seedArrozAndStartersSections(t, s, menuID, starters, rice)

	syncTheLegacySnapshot(t, s, menuID)

	got := storedEntrantes(t, s, menuID)
	if len(got) != len(starters) {
		t.Fatalf("entrantes should hold only the %d starters, got %d: %v", len(starters), len(got), got)
	}
	for _, want := range starters {
		if !containsStoredTitle(got, want) {
			t.Errorf("starter %q missing from entrantes: %v", want, got)
		}
	}
	for _, riceName := range rice {
		if containsStoredTitle(got, riceName) {
			t.Errorf("rice %q leaked into entrantes as bare text: %v", riceName, got)
		}
	}
}

// A second save must not creep back: the duplication the operator reported
// happened on every save, so one snapshot is not enough to prove the fix.
func TestLegacySnapshotDoesNotReintroduceRiceOnRepeatedSaves(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú con arroz", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []string{},
	})

	seedArrozAndStartersSections(t, s, menuID,
		[]string{"Croquetas caseras"},
		[]string{"Arroz a banda", "Paella Valenciana"},
	)

	for save := 1; save <= 3; save++ {
		syncTheLegacySnapshot(t, s, menuID)
		got := storedEntrantes(t, s, menuID)
		if containsStoredTitle(got, "Arroz a banda") || containsStoredTitle(got, "Paella Valenciana") {
			t.Fatalf("save %d re-duplicated the rice into entrantes: %v", save, got)
		}
	}
}

// The rice must survive untouched: the fix routes them nowhere, it must not
// delete them or fold them into the principals block.
func TestLegacySnapshotLeavesRiceRowsIntact(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú con arroz", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []string{"Lomo de salmón"},
	})

	seedArrozSectionWithKind(t, s, menuID, "principales", "Lomo de salmón")
	seedArrozAndStartersSections(t, s, menuID,
		[]string{"Croquetas caseras"},
		[]string{"Arroz a banda"},
	)

	syncTheLegacySnapshot(t, s, menuID)

	var riceRows int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM group_menu_section_dishes_v2 d
		 JOIN group_menu_sections_v2 s ON s.id = d.section_id
		 WHERE s.menu_id = ? AND s.restaurant_id = ? AND s.section_kind = 'arroces' AND d.active = 1`,
		menuID, arrozTestRestaurant,
	).Scan(&riceRows); err != nil {
		t.Fatal(err)
	}
	if riceRows != 1 {
		t.Fatalf("the arroz section should still hold 1 dish, got %d", riceRows)
	}

	principales := storedPrincipales(t, s, menuID)
	items := anySliceToStringList(principales["items"])
	if !containsStoredTitle(items, "Lomo de salmón") {
		t.Errorf("a hand-typed principal was lost: %v", items)
	}
	if containsStoredTitle(items, "Arroz a banda") {
		t.Errorf("the rice was folded into principales.items: %v", items)
	}
}

// The full round trip a dish save takes: PUT the section dishes, then read the
// legacy row back. This is the path the backoffice editor uses.
func TestSectionDishSaveDoesNotCopyRiceIntoEntrantes(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú con arroz", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []string{},
	})

	seedArrozAndStartersSections(t, s, menuID,
		[]string{"Croquetas caseras", "Patatas bravas"},
		[]string{"Arroz a banda"},
	)

	body := `{"dishes":[{"title":"Arroz a banda","active":true}]}`
	rec := httptest.NewRecorder()
	req := categoryReq(http.MethodPut, "/group-menus-v2/sections/dishes", body,
		arrozTestRestaurant, map[string]string{"id": strconv.Itoa(menuID), "sectionId": "1"})
	s.handleBOGroupMenusV2PutSectionDishes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT dishes: status=%d body=%s", rec.Code, rec.Body.String())
	}

	got := storedEntrantes(t, s, menuID)
	if containsStoredTitle(got, "Arroz a banda") {
		t.Fatalf("saving the rice duplicated it into entrantes: %v", got)
	}
}

// containsStoredTitle reports whether a legacy string array holds an exact title.
func containsStoredTitle(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
