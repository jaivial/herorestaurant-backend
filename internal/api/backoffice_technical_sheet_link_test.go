package api

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func seedComidaItem(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	// source_type stores the tipo slug used by the list endpoint ("platos"),
	// not a singular label; seeding the wrong value hides the row from queries.
	res, err := s.db.Exec(`INSERT INTO comida_items (restaurant_id,source_type,nombre,titulo,active)
		VALUES (1,'platos',?,?,1)`, name, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func patchProduct(t *testing.T, s *Server, itemID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x", body,
		map[string]string{"id": strconv.FormatInt(itemID, 10)}))
	return rec
}

func TestProductStartsRawAndCanBecomeManufactured(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")

	var productionType string
	s.db.QueryRow(`SELECT production_type FROM comida_items WHERE restaurant_id=1 AND id=?`, itemID).Scan(&productionType)
	if productionType != "RAW" {
		t.Fatalf("new product is %q; existing products must default to RAW so nothing changes on upgrade", productionType)
	}

	if rec := patchProduct(t, s, itemID, `{"productionType":"MANUFACTURED"}`); rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	s.db.QueryRow(`SELECT production_type FROM comida_items WHERE restaurant_id=1 AND id=?`, itemID).Scan(&productionType)
	if productionType != "MANUFACTURED" {
		t.Fatalf("production_type=%q want MANUFACTURED", productionType)
	}
}

func TestProductionTypeRejectsUnknownValue(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	if rec := patchProduct(t, s, itemID, `{"productionType":"MAGIC"}`); rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// A manufactured product is linked to exactly one sheet; the link is what makes
// POS deduct the right semi-finished item.
func TestLinkingASheetSetsBothTheRecipeAndTheStockItem(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	sheetID := createSheet(t, s, "Paella ficha")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&outputItemID)

	rec := patchProduct(t, s, itemID,
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var linkedRecipe, linkedItem int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0),COALESCE(stock_item_id,0)
		FROM comida_items WHERE restaurant_id=1 AND id=?`, itemID).Scan(&linkedRecipe, &linkedItem)
	if linkedRecipe != sheetID {
		t.Fatalf("stock_recipe_id=%d want %d", linkedRecipe, sheetID)
	}
	// Without the item link the POS would know the recipe but not what to
	// deduct, so both are written together.
	if linkedItem != outputItemID {
		t.Fatalf("stock_item_id=%d want the sheet output %d", linkedItem, outputItemID)
	}
}

func TestLinkingRejectsASheetFromAnotherTenant(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	sheetID := createSheet(t, s, "Ficha ajena")

	// Act as a different tenant: its own product must not be linkable to a
	// sheet it cannot see.
	req := sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`,
		map[string]string{"id": strconv.FormatInt(itemID, 10)})
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, req)
	if rec.Code == 200 {
		t.Fatal("a sheet from another tenant must not be linkable")
	}
	var linked int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM comida_items WHERE id=?`, itemID).Scan(&linked)
	if linked != 0 {
		t.Fatalf("stock_recipe_id=%d leaked across tenants", linked)
	}
}

// Going back to RAW must clear the link, otherwise the POS would keep deducting
// a recipe the product no longer claims to use.
func TestRevertingToRawClearsTheLink(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	sheetID := createSheet(t, s, "Ficha")
	patchProduct(t, s, itemID, `{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`)

	if rec := patchProduct(t, s, itemID, `{"productionType":"RAW"}`); rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var linkedRecipe int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM comida_items WHERE restaurant_id=1 AND id=?`, itemID).Scan(&linkedRecipe)
	if linkedRecipe != 0 {
		t.Fatalf("stock_recipe_id=%d must be cleared when the product goes back to RAW", linkedRecipe)
	}
}

// Usage drives the "this sheet is used by N products" warning before an edit.
func TestSheetUsageListsLinkedProducts(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Salsa compartida")
	first := seedComidaItem(t, s, "Plato A")
	second := seedComidaItem(t, s, "Plato B")
	patchProduct(t, s, first, `{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`)
	patchProduct(t, s, second, `{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetUsage(rec, sheetReq("GET", "/x", "", map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Products []struct {
			Name string `json:"name"`
		} `json:"products"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Products) != 2 {
		t.Fatalf("usage returned %d products, want 2", len(out.Products))
	}
}

// Deleting a sheet that a product still sells would break that product's stock
// deduction, so it is refused while the link exists.
func TestDeleteIsRefusedWhileAProductUsesTheSheet(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "En uso")
	itemID := seedComidaItem(t, s, "Plato")
	patchProduct(t, s, itemID, `{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetDelete(rec, sheetReq("DELETE", "/x", "", map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 409 {
		t.Fatalf("status %d want 409 while the sheet is in use", rec.Code)
	}
	var stillThere int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&stillThere)
	if stillThere != 1 {
		t.Fatal("the sheet must survive a refused delete")
	}
}

func TestDeleteRemovesAnUnusedSheetAndItsChildren(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Sin uso")
	itemID, unitID := seedIngredient(t, s, "Sal", "g", "MASS", 1, "")
	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":5}`)
	addStep(t, s, sheetID, `{"description":"Paso"}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetDelete(rec, sheetReq("DELETE", "/x", "", map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var recipes, components, steps int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&recipes)
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_components WHERE restaurant_id=1 AND recipe_id=?`, sheetID).Scan(&components)
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_steps WHERE restaurant_id=1 AND recipe_id=?`, sheetID).Scan(&steps)
	if recipes != 0 || components != 0 || steps != 0 {
		t.Fatalf("leftovers: recipes=%d components=%d steps=%d", recipes, components, steps)
	}
}

// A sheet used as an ingredient by another sheet is equally unsafe to delete.
func TestDeleteIsRefusedWhileAnotherSheetUsesItAsSubRecipe(t *testing.T) {
	s := sheetsTestServer(t)
	base := createSheet(t, s, "Salsa base")
	parent := createSheet(t, s, "Plato")
	var baseOutput int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, base).Scan(&baseOutput)
	var baseUnit int64
	s.db.QueryRow(`SELECT id FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, baseOutput).Scan(&baseUnit)
	addComponent(t, s, parent, `{"stockItemId":`+strconv.FormatInt(baseOutput, 10)+
		`,"unitId":`+strconv.FormatInt(baseUnit, 10)+`,"quantity":1,"subRecipeId":`+strconv.FormatInt(base, 10)+`}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetDelete(rec, sheetReq("DELETE", "/x", "", map[string]string{"id": strconv.FormatInt(base, 10)}))
	if rec.Code != 409 {
		t.Fatalf("status %d want 409", rec.Code)
	}
}

// A sheet used by a wine is just as unsafe to delete as one used by a dish:
// deleting it would leave that wine unable to deduct stock.
func TestSheetUsedByAWineCannotBeDeleted(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ficha sangria")
	vinoID := seedVino(t, s, "Sangria")

	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"vinos"}`,
		map[string]string{"id": strconv.FormatInt(vinoID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("link status %d body %s", rec.Code, rec.Body.String())
	}

	del := httptest.NewRecorder()
	s.handleBOTechnicalSheetDelete(del, sheetReq("DELETE", "/x", "",
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if del.Code != 409 {
		t.Fatalf("status %d want 409 while a wine still uses the sheet", del.Code)
	}
	var alive int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&alive)
	if alive != 1 {
		t.Fatal("the sheet must survive a refused delete")
	}
}

// Every catalogue product is linked to its own RAW stock item by the backfill.
// Going back to Materia prima must drop the recipe link but keep that stock
// link: nulling it silently unlinked the product from stock for good, and the
// only way back was re-running the backfill.
func TestRevertingToRawKeepsTheProductsOwnStockItem(t *testing.T) {
	s := sheetsTestServer(t)

	// A catalogue product linked to its own raw stock item, as the backfill leaves it.
	rawItem, _ := seedIngredient(t, s, "Cafe solo", "ud", "COUNT", 1, "")
	// The backfill keys the SKU by the tipo slug in source_type, here "cafes".
	if _, err := s.db.Exec(
		`UPDATE stock_items SET sku='catalog:cafes:900' WHERE restaurant_id=1 AND id=?`, rawItem); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO comida_items
		(id,restaurant_id,source_type,nombre,precio,production_type,stock_item_id)
		VALUES (900,1,'cafes','Cafe solo',2.2,'RAW',?)`, rawItem); err != nil {
		t.Fatal(err)
	}

	sheetID := createSheet(t, s, "Ficha")

	// To Preparado...
	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"comida"}`,
		map[string]string{"id": "900"}))
	if rec.Code != 200 {
		t.Fatalf("to manufactured: status %d body %s", rec.Code, rec.Body.String())
	}

	// ...and back to Materia prima.
	rec = httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"RAW","source":"comida"}`, map[string]string{"id": "900"}))
	if rec.Code != 200 {
		t.Fatalf("to raw: status %d body %s", rec.Code, rec.Body.String())
	}

	var stockItemID, recipeID sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT stock_item_id, stock_recipe_id FROM comida_items WHERE restaurant_id=1 AND id=900`).
		Scan(&stockItemID, &recipeID); err != nil {
		t.Fatal(err)
	}
	if recipeID.Valid {
		t.Fatalf("recipe link survived the revert: %d", recipeID.Int64)
	}
	if !stockItemID.Valid || stockItemID.Int64 != rawItem {
		t.Fatalf("stock item link is %v, want the product's own raw item %d", stockItemID, rawItem)
	}
}

// The browser filters by category, so the list has to expose the category of
// each sheet's output item and be able to narrow by it.
func TestSheetListFiltersByCategoryAndReportsIt(t *testing.T) {
	s := sheetsTestServer(t)

	// The category name is unique per tenant, so the row is removed again rather
	// than leaking into the next run.
	if _, err := s.db.Exec(`DELETE FROM stock_categories WHERE restaurant_id=1 AND name='Salsas'`); err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(
		`INSERT INTO stock_categories (restaurant_id,name,sort_order,is_active) VALUES (1,'Salsas',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	categoryID, _ := res.LastInsertId()
	t.Cleanup(func() {
		s.db.Exec(`UPDATE stock_items SET category_id=NULL WHERE restaurant_id=1 AND category_id=?`, categoryID)
		s.db.Exec(`DELETE FROM stock_categories WHERE restaurant_id=1 AND id=?`, categoryID)
	})

	inCategory := createSheet(t, s, "Salsa brava")
	createSheet(t, s, "Pan de masa madre")

	// The category lives on the sheet's output item.
	if _, err := s.db.Exec(`UPDATE stock_items SET category_id=?
		 WHERE restaurant_id=1 AND id=(SELECT output_item_id FROM stock_recipes WHERE id=?)`,
		categoryID, inCategory); err != nil {
		t.Fatal(err)
	}

	read := func(url string) []map[string]any {
		rec := httptest.NewRecorder()
		s.handleBOTechnicalSheetList(rec, sheetReq("GET", url, "", nil))
		if rec.Code != 200 {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Sheets []map[string]any `json:"sheets"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Sheets
	}

	all := read("/comida/technical-sheets")
	if len(all) != 2 {
		t.Fatalf("unfiltered list returned %d sheets, want 2", len(all))
	}

	filtered := read("/comida/technical-sheets?categoryId=" + strconv.FormatInt(categoryID, 10))
	if len(filtered) != 1 {
		t.Fatalf("category filter returned %d sheets, want 1: %+v", len(filtered), filtered)
	}
	if filtered[0]["name"] != "Salsa brava" {
		t.Fatalf("filtered to the wrong sheet: %+v", filtered[0])
	}
	if filtered[0]["categoryName"] != "Salsas" {
		t.Fatalf("categoryName not reported: %+v", filtered[0])
	}
}

// The product form is filled from the chosen sheet, so the list has to carry the
// values that fill it. Without them the picker would have to fetch every sheet
// one by one just to show a card.
func TestSheetListCarriesTheFieldsThatFillTheProductForm(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Paella")

	if _, err := s.db.Exec(
		`UPDATE stock_recipes SET selling_price_gross=18.50, prep_time_min=35 WHERE restaurant_id=1 AND id=?`,
		sheetID); err != nil {
		t.Fatal(err)
	}
	addStep(t, s, sheetID, `{"title":"Sofreir","description":"Sofreir"}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetList(rec, sheetReq("GET", "/comida/technical-sheets", "", nil))
	var out struct {
		Sheets []struct {
			Name              string   `json:"name"`
			SellingPriceGross *float64 `json:"sellingPriceGross"`
			PrepTimeMin       *int     `json:"prepTimeMin"`
			StepCount         *int     `json:"stepCount"`
			ComponentCount    *int     `json:"componentCount"`
			Allergens         *[]string `json:"allergens"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sheets) != 1 {
		t.Fatalf("sheets %d want 1", len(out.Sheets))
	}
	got := out.Sheets[0]
	if got.SellingPriceGross == nil || *got.SellingPriceGross != 18.5 {
		t.Fatalf("sellingPriceGross %v want 18.5: %s", got.SellingPriceGross, rec.Body.String())
	}
	if got.PrepTimeMin == nil || *got.PrepTimeMin != 35 {
		t.Fatalf("prepTimeMin %v want 35", got.PrepTimeMin)
	}
	if got.StepCount == nil || *got.StepCount != 1 {
		t.Fatalf("stepCount %v want 1", got.StepCount)
	}
	if got.ComponentCount == nil {
		t.Fatal("componentCount missing")
	}
	if got.Allergens == nil {
		t.Fatal("allergens missing: the form fills its allergen grid from this")
	}
}
