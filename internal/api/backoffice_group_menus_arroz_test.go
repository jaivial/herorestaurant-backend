package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"preactvillacarmen/internal/config"
)

// Regression tests for the arroz round-trip bug.
//
// GET /api/admin/group-menus/{id} merges the arroz dishes into principales.items
// so the booking can offer them as principales
// (coordination id booking_principales_include_arroz_v1). Those merged names are
// bare text: no price, no allergens, no dish id. Persisting them means a save
// writes the derived set into the row, and every later save keeps it there.
//
// These tests pin the invariant: the merge is read-only, the stored row only ever
// holds what a human chose, and the booking keeps being offered the arroz.

const arrozTestRestaurant = 99001

func arrozTestServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(time.Minute)
	if err := db.Ping(); err != nil {
		t.Fatalf("ping test db: %v", err)
	}
	if _, err := db.Exec(
		`INSERT IGNORE INTO restaurants (id, name) VALUES (?, 'Arroz Regression Test')`,
		arrozTestRestaurant,
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Children before parents: a foreign key would silently skip the parent.
		for _, statement := range []string{
			`DELETE FROM group_menu_section_dishes_v2 WHERE restaurant_id = ?`,
			`DELETE FROM group_menu_sections_v2 WHERE restaurant_id = ?`,
			`DELETE FROM comida_items WHERE restaurant_id = ?`,
			`DELETE FROM menus WHERE restaurant_id = ?`,
			`DELETE FROM restaurants WHERE id = ?`,
		} {
			arg := any(arrozTestRestaurant)
			if strings.Contains(statement, "restaurants WHERE id") {
				arg = arrozTestRestaurant
			}
			if _, err := db.Exec(statement, arg); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
		db.Close()
	})
	return NewServer(db, config.Config{BunnyPullBaseURL: "https://example.b-cdn.net"})
}

// seedArrozMenu creates a legacy group menu with the given principales items.
func seedArrozMenu(t *testing.T, s *Server, title string, principales map[string]any) int {
	t.Helper()
	raw, err := json.Marshal(principales)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(
		`INSERT INTO menus (restaurant_id, menu_title, price, active, principales, menu_subtitle, entrantes, postre)
		 VALUES (?, ?, 45.00, 1, ?, '[]', '[]', '[]')`,
		arrozTestRestaurant, title, string(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(id)
}

// seedArrozSection gives the menu an "arroces" section, i.e. source 1 of the merge.
func seedArrozSection(t *testing.T, s *Server, menuID int, kind string, dishes ...string) {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO group_menu_sections_v2 (restaurant_id, menu_id, title, section_kind, position)
		 VALUES (?, ?, 'Arroces', ?, 0)`,
		arrozTestRestaurant, menuID, kind,
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

// seedArrozCatalogDish gives the restaurant a catalog dish of type ARROZ,
// i.e. source 2 of the merge.
func seedArrozCatalogDish(t *testing.T, s *Server, nombre, tipo, categoria string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO comida_items (restaurant_id, source_type, nombre, tipo, categoria, precio, active)
		 VALUES (?, 'platos', ?, ?, ?, 13.50, 1)`,
		arrozTestRestaurant, nombre, tipo, categoria,
	); err != nil {
		t.Fatal(err)
	}
}

func arrozGet(t *testing.T, s *Server, menuID int) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := categoryReq(http.MethodGet, "/group-menus/"+strconv.Itoa(menuID), "", arrozTestRestaurant,
		map[string]string{"id": strconv.Itoa(menuID)})
	s.handleBOGroupMenuGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET menu: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Success bool `json:"success"`
		Menu    struct {
			Principales map[string]any `json:"principales"`
		} `json:"menu"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success {
		t.Fatalf("GET did not succeed: %s", rec.Body.String())
	}
	return out.Menu.Principales
}

// arrozSave round-trips a menu through the update handler exactly the way the
// backoffice does: read the whole block, send it straight back.
func arrozSave(t *testing.T, s *Server, menuID int, principales map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"menu_title":  "Menú Arrocero",
		"price":       45.0,
		"active":      true,
		"principales": principales,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := categoryReq(http.MethodPut, "/group-menus/"+strconv.Itoa(menuID), string(body),
		arrozTestRestaurant, map[string]string{"id": strconv.Itoa(menuID)})
	s.handleBOGroupMenuUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT menu: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("PUT did not succeed: %s", rec.Body.String())
	}
}

// storedPrincipales reads the row back, i.e. what actually survived the save.
func storedPrincipales(t *testing.T, s *Server, menuID int) map[string]any {
	t.Helper()
	var raw sql.NullString
	if err := s.db.QueryRow(
		`SELECT principales FROM menus WHERE id = ? AND restaurant_id = ?`,
		menuID, arrozTestRestaurant,
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if strings.TrimSpace(raw.String) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		t.Fatalf("stored principales is not an object: %v (%s)", err, raw.String)
	}
	return out
}

func itemsOf(t *testing.T, principales map[string]any) []string {
	t.Helper()
	return anySliceToStringList(principales["items"])
}

func keySet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[arrozNameKey(it)] = true
	}
	return out
}

func assertNoArroz(t *testing.T, where string, items []string) {
	t.Helper()
	for _, it := range items {
		if strings.Contains(arrozNameKey(it), "arroz") {
			t.Errorf("%s stored the derived arroz %q in principales.items: %v", where, it, items)
		}
	}
}

// TestGuardarElMenuNoPersisteLosArrocesDerivados is the reported bug: open a menu
// that has an arroz section, save it, and the stored items must not contain the
// arroz names that only the read path added.
func TestGuardarElMenuNoPersisteLosArrocesDerivados(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []any{"Meloso de pescado", "Carrillada al vino tinto"},
	})
	seedArrozSection(t, s, menuID, "arroces", "Arroz a banda", "Arroz meloso de pulpo")

	// The read merges the arroz in so the booking can offer them.
	read := arrozGet(t, s, menuID)
	readItems := itemsOf(t, read)
	for _, want := range []string{"Arroz a banda", "Arroz meloso de pulpo"} {
		if !keySet(readItems)[arrozNameKey(want)] {
			t.Fatalf("the booking should still be offered %q, got %v", want, readItems)
		}
	}

	// The backoffice sends that very block back.
	arrozSave(t, s, menuID, read)

	stored := itemsOf(t, storedPrincipales(t, s, menuID))
	assertNoArroz(t, "after one save", stored)
	want := []string{"Meloso de pescado", "Carrillada al vino tinto"}
	if got := keySet(stored); !got[arrozNameKey(want[0])] || !got[arrozNameKey(want[1])] || len(stored) != 2 {
		t.Fatalf("the save dropped or altered the human principals: %v", stored)
	}
	if _, leaked := storedPrincipales(t, s, menuID)[PrincipalesArrozDerivadosKey]; leaked {
		t.Fatal("the read-only derived key was persisted")
	}

	// Reopening shows the same list: nothing was lost, nothing was duplicated.
	reopened := itemsOf(t, arrozGet(t, s, menuID))
	if len(reopened) != 4 {
		t.Fatalf("the reopened menu should offer 2 stored + 2 derived, got %v", reopened)
	}
}

// TestLaLecturaMarcaLosArrocesDerivados pins the read-side contract: the merged
// arroz come back under their own key, so a client can tell them apart from the
// ones a human typed without having to guess from the name.
func TestLaLecturaMarcaLosArrocesDerivados(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []any{"Meloso de pescado"},
	})
	seedArrozSection(t, s, menuID, "arroces", "Arroz a banda")

	read := arrozGet(t, s, menuID)
	derived := keySet(anySliceToStringList(read[PrincipalesArrozDerivadosKey]))
	if !derived[arrozNameKey("Arroz a banda")] {
		t.Errorf("the read should mark the derived arroz, got %v", read[PrincipalesArrozDerivadosKey])
	}
	if derived[arrozNameKey("Meloso de pescado")] {
		t.Error("a human principal must not be reported as derived")
	}
}

// TestGuardarDosVecesNoHaceCrecerLosItems is the test that kills the real bug:
// saving repeatedly must not grow principales.items.
func TestGuardarDosVecesNoHaceCrecerLosItems(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []any{"Meloso de pescado"},
	})
	seedArrozSection(t, s, menuID, "arroces", "Arroz a banda")
	seedArrozCatalogDish(t, s, "Arroz seco de carrillada", "ARROZ", "Arroces")

	var previous []string
	for round := 1; round <= 3; round++ {
		read := arrozGet(t, s, menuID)
		arrozSave(t, s, menuID, read)
		stored := itemsOf(t, storedPrincipales(t, s, menuID))
		assertNoArroz(t, fmt.Sprintf("save %d", round), stored)
		if round > 1 && len(stored) != len(previous) {
			t.Fatalf("save %d changed the stored item count: %d -> %d (%v)",
				round, len(previous), len(stored), stored)
		}
		previous = stored
	}
	if len(previous) != 1 {
		t.Fatalf("the human principal should be the only stored item, got %v", previous)
	}
}

// TestGuardarSinArrocesDerivadosNoCambiaNada covers the production data as it is
// today: no arroz section and no arroz in the catalog, so the derived set is empty
// and a save must persist exactly what the client sent, every time.
func TestGuardarSinArrocesDerivadosNoCambiaNada(t *testing.T) {
	s := arrozTestServer(t)
	original := []any{"Meloso de secreto con ajetes", "Meloso de pescado"}
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              original,
	})

	previous := itemsOf(t, storedPrincipales(t, s, menuID))
	for round := 1; round <= 2; round++ {
		read := arrozGet(t, s, menuID)
		if got := itemsOf(t, read); len(got) != len(original) {
			t.Fatalf("the read should not invent items with an empty derived set: %v", got)
		}
		arrozSave(t, s, menuID, read)
		stored := itemsOf(t, storedPrincipales(t, s, menuID))
		if len(stored) != len(previous) {
			t.Fatalf("save %d changed the stored item count: %d -> %d (%v)",
				round, len(previous), len(stored), stored)
		}
		previous = stored
	}
	if len(previous) != 2 {
		t.Fatalf("the stored principals changed: %v", previous)
	}
}

// TestElBookingSigueOfreciendoLosArrocesSinGuardarlos is the guarantee that the
// fix does not cost the booking its arroz: they are derived, so they are offered
// on every read and stored in none.
func TestElBookingSigueOfreciendoLosArrocesSinGuardarlos(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []any{"Meloso de pescado"},
	})
	seedArrozSection(t, s, menuID, "arroces", "Arroz a banda")
	seedArrozCatalogDish(t, s, "Arroz del senyoret", "ARROZ", "")

	offered := keySet(itemsOf(t, arrozGet(t, s, menuID)))
	for _, want := range []string{"Arroz a banda", "Arroz del senyoret"} {
		if !offered[arrozNameKey(want)] {
			t.Errorf("the booking lost %q: not offered any more", want)
		}
	}
	assertNoArroz(t, "the stored row", itemsOf(t, storedPrincipales(t, s, menuID)))
}

// TestUnPrincipalQueNoEsDerivadoSeConserva guards the edge case of stripping the
// derived set: a principal the user typed by hand is only removed when the read
// would derive that same name. An arroz-shaped title that nothing derives must
// survive a save untouched.
func TestUnPrincipalQueNoEsDerivadoSeConserva(t *testing.T) {
	s := arrozTestServer(t)
	typed := []any{"Meloso de pescado", "Arroz de la casa de la abuela"}
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              typed,
	})

	arrozSave(t, s, menuID, map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              typed,
	})

	stored := itemsOf(t, storedPrincipales(t, s, menuID))
	if len(stored) != 2 || !keySet(stored)[arrozNameKey("Arroz de la casa de la abuela")] {
		t.Fatalf("a principal nothing derives must survive a save, got %v", stored)
	}
}

// TestElLegacyPhpTambienDescontamina covers the older
// /api/admin/menuDeGruposBackend/updateMenu.php entry point, which writes the same
// column and used the same unsanitised payload.
func TestElLegacyPhpTambienDescontamina(t *testing.T) {
	s := arrozTestServer(t)
	menuID := seedArrozMenu(t, s, "Menú Arrocero", map[string]any{
		"titulo_principales": "Principal a elegir",
		"items":              []any{"Meloso de pescado"},
	})
	seedArrozCatalogDish(t, s, "Arroz a banda", "ARROZ", "Arroces")

	// A client that read the menu and sent the block back untouched, derived key included.
	payload := fmt.Sprintf(
		`{"id":%d,"menu_title":"Menú Arrocero","price":45,"active":true,"principales":{`+
			`"titulo_principales":"Principal a elegir",`+
			`"items":["Meloso de pescado","Arroz a banda"],`+
			`%q:["Arroz a banda"]}}`,
		menuID, PrincipalesArrozDerivadosKey,
	)
	rec := httptest.NewRecorder()
	req := categoryReq(http.MethodPut, "/updateMenu.php", payload, arrozTestRestaurant,
		map[string]string{"id": strconv.Itoa(menuID)})
	// The legacy PHP entry points authenticate with the plain restaurant context
	// rather than a backoffice session.
	req = req.WithContext(context.WithValue(req.Context(), restaurantIDKey, arrozTestRestaurant))
	s.handleUpdateGroupMenu(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy update: status=%d body=%s", rec.Code, rec.Body.String())
	}

	stored := storedPrincipales(t, s, menuID)
	assertNoArroz(t, "the legacy php save", itemsOf(t, stored))
	if _, leaked := stored[PrincipalesArrozDerivadosKey]; leaked {
		t.Fatal("the legacy save persisted the read-only derived key")
	}
}
