package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func duplicateSheet(t *testing.T, s *Server, sheetID int64, body string) (int64, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetDuplicate(rec, sheetReq("POST", "/x", body,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	var out struct {
		SheetID int64 `json:"sheetId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.SheetID, rec
}

// Decision #5: linking a sheet to a second product duplicates it. Editing the
// copy must never reach back into the original.
func TestDuplicateCopiesComponentsAndSteps(t *testing.T) {
	s := sheetsTestServer(t)
	original := createSheet(t, s, "Salsa base")
	itemID, unitID := seedIngredient(t, s, "Tomate", "g", "MASS", 1, "")
	addComponent(t, s, original, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":300,"wastePct":5}`)
	addStep(t, s, original, `{"title":"Cocer","description":"Cocer 20 min"}`)
	addStep(t, s, original, `{"title":"Triturar","description":"Triturar fino"}`)

	copyID, rec := duplicateSheet(t, s, original, `{"name":"Salsa copia"}`)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if copyID == original {
		t.Fatal("duplicate must create a new sheet")
	}

	var components, steps int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, copyID).Scan(&components)
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_steps WHERE restaurant_id=1 AND recipe_id=?`, copyID).Scan(&steps)
	if components != 1 || steps != 2 {
		t.Fatalf("copy has %d components and %d steps, want 1 and 2", components, steps)
	}
	var qtyBase, waste float64
	s.db.QueryRow(`SELECT qty_base,waste_pct FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, copyID).
		Scan(&qtyBase, &waste)
	if qtyBase != 300 || waste != 5 {
		t.Fatalf("copied component qty=%v waste=%v want 300/5", qtyBase, waste)
	}
}

// The whole point of copying instead of sharing: the two sheets must be
// independent afterwards.
func TestEditingACopyDoesNotAffectTheOriginal(t *testing.T) {
	s := sheetsTestServer(t)
	original := createSheet(t, s, "Original")
	itemID, unitID := seedIngredient(t, s, "Tomate", "g", "MASS", 1, "")
	addComponent(t, s, original, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":300}`)

	copyID, _ := duplicateSheet(t, s, original, `{"name":"Copia"}`)

	var copyComponentID int64
	s.db.QueryRow(`SELECT id FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, copyID).Scan(&copyComponentID)
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetComponentPatch(rec, sheetReq("PATCH", "/x", `{"quantity":900}`,
		map[string]string{"id": strconv.FormatInt(copyID, 10), "componentId": strconv.FormatInt(copyComponentID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	var originalQty float64
	s.db.QueryRow(`SELECT qty_base FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, original).Scan(&originalQty)
	if originalQty != 300 {
		t.Fatalf("original qty changed to %v; the copy is not independent", originalQty)
	}
}

// The copy produces its own semi-finished item, otherwise both sheets would
// write production into the same stock line.
func TestDuplicateCreatesItsOwnOutputItem(t *testing.T) {
	s := sheetsTestServer(t)
	original := createSheet(t, s, "Base")
	copyID, _ := duplicateSheet(t, s, original, `{"name":"Copia"}`)

	var originalOutput, copyOutput int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, original).Scan(&originalOutput)
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, copyID).Scan(&copyOutput)
	if originalOutput == copyOutput || copyOutput == 0 {
		t.Fatalf("copy output=%d original output=%d; they must differ", copyOutput, originalOutput)
	}
	var units int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, copyOutput).Scan(&units)
	if units == 0 {
		t.Fatal("the copied output item has no base unit, so it could never be used in a recipe")
	}
}

// A copy starts as a draft: it is not a published sheet until its owner says so.
func TestDuplicateStartsAsDraft(t *testing.T) {
	s := sheetsTestServer(t)
	original := createSheet(t, s, "Base")
	copyID, _ := duplicateSheet(t, s, original, `{"name":"Copia"}`)

	var status string
	s.db.QueryRow(`SELECT status FROM stock_recipes WHERE restaurant_id=1 AND id=?`, copyID).Scan(&status)
	if status != "DRAFT" {
		t.Fatalf("status=%q want DRAFT", status)
	}
	var copiedFrom int64
	s.db.QueryRow(`SELECT COALESCE(copied_from_recipe_id,0) FROM stock_recipes WHERE restaurant_id=1 AND id=?`, copyID).Scan(&copiedFrom)
	if copiedFrom != original {
		t.Fatalf("copied_from_recipe_id=%d want %d for traceability", copiedFrom, original)
	}
}

func TestDuplicateRejectsAForeignSheet(t *testing.T) {
	s := sheetsTestServer(t)
	original := createSheet(t, s, "Ajena")
	req := sheetReq("POST", "/x", `{"name":"Robada"}`, map[string]string{"id": strconv.FormatInt(original, 10)})
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetDuplicate(rec, req)
	if rec.Code == 200 {
		t.Fatal("a foreign tenant must not duplicate this sheet")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE copied_from_recipe_id=?`, original).Scan(&n)
	if n != 0 {
		t.Fatalf("%d copies leaked", n)
	}
}
