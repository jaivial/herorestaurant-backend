package api

import (
	"database/sql"
	"testing"
)

// seedVinoCatalogue inserts a wine the way the handlers now do: a row in
// group_menu_wines_v2 plus its legacy_wine_map entry, and it returns the legacy
// num because that is what every handler, payload and test compares against.
//
// legacyNum <= 0 asks for the next free num for the restaurant, which is what
// insertVinoV2 allocates.
func seedVinoCatalogue(t *testing.T, db *sql.DB, restaurantID int, legacyNum int, tipo, nombre string, precio float64) int64 {
	t.Helper()
	if legacyNum <= 0 {
		if err := db.QueryRow(`SELECT COALESCE(MAX(legacy_num),0)+1 FROM legacy_wine_map WHERE restaurant_id=?`, restaurantID).Scan(&legacyNum); err != nil {
			t.Fatalf("next legacy num: %v", err)
		}
	}
	res, err := db.Exec(
		`INSERT INTO group_menu_wines_v2 (restaurant_id,tipo,nombre,precio,active,position) VALUES (?,?,?,?,1,?)`,
		restaurantID, tipo, nombre, precio, legacyNum)
	if err != nil {
		t.Fatalf("insert vino: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("vino id: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO legacy_wine_map (restaurant_id,legacy_num,v2_wine_id) VALUES (?,?,?)`,
		restaurantID, legacyNum, id); err != nil {
		t.Fatalf("map vino: %v", err)
	}
	return id
}
