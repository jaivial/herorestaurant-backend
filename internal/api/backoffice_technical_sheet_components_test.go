package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

// seedIngredient creates a raw stock item plus a recipe-usable unit and returns
// both ids.
func seedIngredient(t *testing.T, s *Server, name, baseUnit, dimension string, factor float64, allergens string) (itemID, unitID int64) {
	t.Helper()
	allergenExpr := "NULL"
	if allergens != "" {
		allergenExpr = allergens
	}
	res, err := s.db.Exec(`INSERT INTO stock_items (restaurant_id,name,kind,base_dimension,base_unit,allergens_json)
		VALUES (1,?,'RAW',?,?,`+allergenExpr+`)`, name, dimension, baseUnit)
	if err != nil {
		t.Fatal(err)
	}
	itemID, _ = res.LastInsertId()
	unitRes, err := s.db.Exec(`INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,can_recipe,is_default_display)
		VALUES (1,?,?,?,?,1,1)`, itemID, baseUnit, baseUnit, factor)
	if err != nil {
		t.Fatal(err)
	}
	unitID, _ = unitRes.LastInsertId()
	return itemID, unitID
}

func addComponent(t *testing.T, s *Server, sheetID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetComponentCreate(rec, sheetReq("POST", "/x", body,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	return rec
}

// Entered quantity is converted to the item's base unit once, on write, so cost
// and production never have to guess which unit a row was typed in.
func TestAddComponentConvertsToBaseUnit(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Masa")
	// 1 kg unit on a gram-based item: factor 1000.
	itemID, _ := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")
	kgRes, err := s.db.Exec(`INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,can_recipe)
		VALUES (1,?,'kg','kg',1000,1)`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	kgUnitID, _ := kgRes.LastInsertId()

	rec := addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(kgUnitID, 10)+`,"quantity":0.5}`)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var qtyBase float64
	if err := s.db.QueryRow(`SELECT qty_base FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, sheetID).
		Scan(&qtyBase); err != nil {
		t.Fatal(err)
	}
	if qtyBase != 500 {
		t.Fatalf("qty_base=%v want 500 (0.5 kg in grams)", qtyBase)
	}
}

func TestAddComponentRejectsUnitFromAnotherItem(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Mezcla")
	itemA, _ := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")
	_, unitB := seedIngredient(t, s, "Leche", "ml", "VOLUME", 1, "")

	// Using another item's unit would silently produce a wrong qty_base.
	rec := addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemA, 10)+
		`,"unitId":`+strconv.FormatInt(unitB, 10)+`,"quantity":100}`)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

func TestAddComponentRejectsNonPositiveQuantity(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Cero")
	itemID, unitID := seedIngredient(t, s, "Sal", "g", "MASS", 1, "")
	for _, qty := range []string{"0", "-5"} {
		rec := addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
			`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":`+qty+`}`)
		if rec.Code != 400 {
			t.Fatalf("quantity %s: status %d want 400", qty, rec.Code)
		}
	}
}

// A sheet must not consume the item it produces.
func TestAddComponentRejectsSelfOutput(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Bucle")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&outputItemID)
	var unitID int64
	s.db.QueryRow(`SELECT id FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, outputItemID).Scan(&unitID)

	rec := addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(outputItemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":1}`)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// Transitive cycle: A uses B, then B is asked to use A. The existing bulk save
// only rejected direct self-reference, so this is the case that matters.
func TestAddComponentRejectsTransitiveCycle(t *testing.T) {
	s := sheetsTestServer(t)
	sheetA := createSheet(t, s, "Salsa A")
	sheetB := createSheet(t, s, "Salsa B")

	var outA, outB int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetA).Scan(&outA)
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetB).Scan(&outB)
	var unitA, unitB int64
	s.db.QueryRow(`SELECT id FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, outA).Scan(&unitA)
	s.db.QueryRow(`SELECT id FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, outB).Scan(&unitB)

	// A consumes B's output via sub-recipe B.
	rec := addComponent(t, s, sheetA, `{"stockItemId":`+strconv.FormatInt(outB, 10)+
		`,"unitId":`+strconv.FormatInt(unitB, 10)+`,"quantity":1,"subRecipeId":`+strconv.FormatInt(sheetB, 10)+`}`)
	if rec.Code != 200 {
		t.Fatalf("A<-B status %d body %s", rec.Code, rec.Body.String())
	}
	// B consuming A's output would close the loop.
	rec = addComponent(t, s, sheetB, `{"stockItemId":`+strconv.FormatInt(outA, 10)+
		`,"unitId":`+strconv.FormatInt(unitA, 10)+`,"quantity":1,"subRecipeId":`+strconv.FormatInt(sheetA, 10)+`}`)
	if rec.Code != 400 {
		t.Fatalf("B<-A status %d want 400 (cycle) body %s", rec.Code, rec.Body.String())
	}
}

func TestAddComponentRejectsSubRecipeThatDoesNotProduceTheItem(t *testing.T) {
	s := sheetsTestServer(t)
	sheetA := createSheet(t, s, "Plato")
	sheetB := createSheet(t, s, "Guarnicion")
	itemID, unitID := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")

	rec := addComponent(t, s, sheetA, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":10,"subRecipeId":`+strconv.FormatInt(sheetB, 10)+`}`)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// Adding an ingredient changes what the dish contains, so the cached derived
// allergen set must be recomputed immediately.
func TestAddComponentRefreshesDerivedAllergens(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Con leche")
	itemID, unitID := seedIngredient(t, s, "Nata", "ml", "VOLUME", 1, "JSON_ARRAY('Leche')")

	if rec := addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":200}`); rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var derivedJSON string
	if err := s.db.QueryRow(`SELECT derived_allergens_json FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).
		Scan(&derivedJSON); err != nil {
		t.Fatal(err)
	}
	var derived []string
	json.Unmarshal([]byte(derivedJSON), &derived)
	if len(derived) != 1 || derived[0] != "Leche" {
		t.Fatalf("derived=%v want [Leche]", derived)
	}
}

func TestDeleteComponentRefreshesDerivedAllergens(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Quitar leche")
	itemID, unitID := seedIngredient(t, s, "Nata", "ml", "VOLUME", 1, "JSON_ARRAY('Leche')")
	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":200}`)

	var componentID int64
	s.db.QueryRow(`SELECT id FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, sheetID).Scan(&componentID)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetComponentDelete(rec, sheetReq("DELETE", "/x", "", map[string]string{
		"id": strconv.FormatInt(sheetID, 10), "componentId": strconv.FormatInt(componentID, 10),
	}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var derivedJSON string
	s.db.QueryRow(`SELECT derived_allergens_json FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&derivedJSON)
	var derived []string
	json.Unmarshal([]byte(derivedJSON), &derived)
	if len(derived) != 0 {
		t.Fatalf("derived=%v want empty after removing the only milk ingredient", derived)
	}
}

func TestPatchComponentRecalculatesBaseQuantity(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ajuste")
	itemID, unitID := seedIngredient(t, s, "Azucar", "g", "MASS", 1, "")
	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":100}`)
	var componentID int64
	s.db.QueryRow(`SELECT id FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, sheetID).Scan(&componentID)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetComponentPatch(rec, sheetReq("PATCH", "/x", `{"quantity":250,"wastePct":10}`,
		map[string]string{"id": strconv.FormatInt(sheetID, 10), "componentId": strconv.FormatInt(componentID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var qtyBase, waste float64
	s.db.QueryRow(`SELECT qty_base,waste_pct FROM stock_recipe_components WHERE restaurant_id=1 AND id=?`, componentID).
		Scan(&qtyBase, &waste)
	if qtyBase != 250 || waste != 10 {
		t.Fatalf("qty_base=%v waste=%v want 250/10", qtyBase, waste)
	}
}

func TestComponentEndpointsAreTenantScoped(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ajena")
	itemID, unitID := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")

	req := sheetReq("POST", "/x", `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":10}`,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)})
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetComponentCreate(rec, req)
	if rec.Code != 404 && rec.Code != 400 {
		t.Fatalf("status %d want 404/400 for a foreign tenant", rec.Code)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_components WHERE recipe_id=?`, sheetID).Scan(&n)
	if n != 0 {
		t.Fatalf("foreign tenant wrote %d components", n)
	}
}

// The ingredient cards show a picture, so the list has to carry the stock item's
// image URL. Without it every card fell back to a placeholder even when the
// product had a photo.
func TestComponentsListReturnsTheStockItemImage(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ficha")
	itemID, unitID := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")

	if _, err := s.db.Exec(
		`UPDATE stock_items SET image_url=? WHERE restaurant_id=1 AND id=?`,
		"https://cdn.example/harina.webp", itemID); err != nil {
		t.Fatal(err)
	}

	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"quantity":500,"unitId":`+strconv.FormatInt(unitID, 10)+`}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetComponentsList(rec, sheetReq("GET", "/x", "",
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Components []struct {
			Name     string `json:"name"`
			ImageURL string `json:"imageUrl"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Components) != 1 {
		t.Fatalf("components %d want 1", len(out.Components))
	}
	if out.Components[0].ImageURL != "https://cdn.example/harina.webp" {
		t.Fatalf("imageUrl %q not returned: %s", out.Components[0].ImageURL, rec.Body.String())
	}
}
