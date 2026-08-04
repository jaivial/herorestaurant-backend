package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func bulkPreview(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaBulkLinkPreview(rec, sheetReq("POST", "/x", body, nil))
	if rec.Code != 200 {
		t.Fatalf("preview status %d body %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func bulkApply(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaBulkLinkApply(rec, sheetReq("POST", "/x", body, nil))
	return rec
}

// The preview is what makes a bulk change safe: the user sees exactly what
// would happen before anything is written.
func TestBulkPreviewDoesNotChangeAnything(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	sheetID := createSheet(t, s, "Ficha paella")

	body := `{"links":[{"itemId":` + strconv.FormatInt(itemID, 10) +
		`,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `}]}`
	out := bulkPreview(t, s, body)

	if out["success"] != true {
		t.Fatalf("preview failed: %v", out)
	}
	var productionType string
	var linked int64
	s.db.QueryRow(`SELECT production_type,COALESCE(stock_recipe_id,0) FROM comida_items WHERE id=?`, itemID).
		Scan(&productionType, &linked)
	if productionType != "RAW" || linked != 0 {
		t.Fatalf("preview mutated the product: type=%s recipe=%d", productionType, linked)
	}
}

func TestBulkPreviewReportsInvalidRowsWithoutFailingTheWholeRequest(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	sheetID := createSheet(t, s, "Ficha")

	body := `{"links":[
		{"itemId":` + strconv.FormatInt(itemID, 10) + `,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `},
		{"itemId":999999,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `}
	]}`
	out := bulkPreview(t, s, body)

	rows, _ := out["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected both rows described, got %v", out["rows"])
	}
	// The user needs to see which line is wrong, not just that something is.
	invalid := 0
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["valid"] == false {
			invalid++
			if row["message"] == "" || row["message"] == nil {
				t.Fatal("an invalid row must explain itself")
			}
		}
	}
	if invalid != 1 {
		t.Fatalf("expected exactly one invalid row, got %d", invalid)
	}
}

// A partially applied bulk change would leave the menu in a state the user
// never reviewed, so the whole batch is one transaction.
func TestBulkApplyIsAllOrNothing(t *testing.T) {
	s := sheetsTestServer(t)
	first := seedComidaItem(t, s, "Plato A")
	second := seedComidaItem(t, s, "Plato B")
	sheetID := createSheet(t, s, "Ficha")

	body := `{"idempotencyKey":"bulk-1","links":[
		{"itemId":` + strconv.FormatInt(first, 10) + `,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `},
		{"itemId":999999,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `}
	]}`
	if rec := bulkApply(t, s, body); rec.Code != 400 {
		t.Fatalf("status %d want 400 when a row is invalid", rec.Code)
	}

	for _, id := range []int64{first, second} {
		var linked int64
		s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM comida_items WHERE id=?`, id).Scan(&linked)
		if linked != 0 {
			t.Fatalf("product %d was linked despite the batch failing", id)
		}
	}
}

func TestBulkApplyLinksEveryValidRow(t *testing.T) {
	s := sheetsTestServer(t)
	first := seedComidaItem(t, s, "Plato A")
	second := seedComidaItem(t, s, "Plato B")
	sheetID := createSheet(t, s, "Ficha")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE id=?`, sheetID).Scan(&outputItemID)

	body := `{"idempotencyKey":"bulk-ok","links":[
		{"itemId":` + strconv.FormatInt(first, 10) + `,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `},
		{"itemId":` + strconv.FormatInt(second, 10) + `,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `}
	]}`
	if rec := bulkApply(t, s, body); rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	for _, id := range []int64{first, second} {
		var productionType string
		var recipe, item int64
		s.db.QueryRow(`SELECT production_type,COALESCE(stock_recipe_id,0),COALESCE(stock_item_id,0)
			FROM comida_items WHERE id=?`, id).Scan(&productionType, &recipe, &item)
		if productionType != "MANUFACTURED" || recipe != sheetID || item != outputItemID {
			t.Fatalf("product %d: type=%s recipe=%d item=%d", id, productionType, recipe, item)
		}
	}
}

// Re-sending the same wizard submission (a retry, a double click) must not
// apply the change twice.
func TestBulkApplyIsIdempotent(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Plato")
	sheetID := createSheet(t, s, "Ficha")
	body := `{"idempotencyKey":"bulk-retry","links":[{"itemId":` + strconv.FormatInt(itemID, 10) +
		`,"stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `}]}`

	first := bulkApply(t, s, body)
	second := bulkApply(t, s, body)
	if first.Code != 200 || second.Code != 200 {
		t.Fatalf("statuses %d/%d", first.Code, second.Code)
	}
	var out struct {
		Applied int  `json:"applied"`
		Reused  bool `json:"reused"`
	}
	json.Unmarshal(second.Body.Bytes(), &out)
	if !out.Reused {
		t.Fatal("the second identical submission must be recognised as a retry")
	}
}

func TestBulkApplyRejectsAForeignSheet(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Plato")
	sheetID := createSheet(t, s, "Ficha")

	req := sheetReq("POST", "/x", `{"idempotencyKey":"x","links":[{"itemId":`+
		strconv.FormatInt(itemID, 10)+`,"stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}]}`, nil)
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOComidaBulkLinkApply(rec, req)
	if rec.Code == 200 {
		t.Fatal("a foreign tenant must not link these products")
	}
	var linked int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM comida_items WHERE id=?`, itemID).Scan(&linked)
	if linked != 0 {
		t.Fatal("cross-tenant link leaked")
	}
}

func TestBulkApplyRejectsAnEmptyBatch(t *testing.T) {
	s := sheetsTestServer(t)
	if rec := bulkApply(t, s, `{"idempotencyKey":"empty","links":[]}`); rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// The UI creates a sheet automatically when a product is switched to Preparado.
// React can run that effect twice (StrictMode, remounts, a double click), so
// the server must be the one that guarantees one sheet per product: a
// client-side guard cannot survive a remount, and the result is orphan sheets
// plus a confusing error for the user.
func TestEnsureSheetForProductIsIdempotent(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Arroz negro")

	first := httptest.NewRecorder()
	s.handleBOTechnicalSheetEnsureForProduct(first, sheetReq("POST", "/x",
		`{"itemId":`+strconv.FormatInt(itemID, 10)+`,"name":"Arroz negro","source":"comida"}`, nil))
	if first.Code != 200 {
		t.Fatalf("first status %d body %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	s.handleBOTechnicalSheetEnsureForProduct(second, sheetReq("POST", "/x",
		`{"itemId":`+strconv.FormatInt(itemID, 10)+`,"name":"Arroz negro","source":"comida"}`, nil))
	if second.Code != 200 {
		t.Fatalf("second status %d body %s", second.Code, second.Body.String())
	}

	var a, b struct {
		SheetID int64 `json:"sheetId"`
	}
	json.Unmarshal(first.Body.Bytes(), &a)
	json.Unmarshal(second.Body.Bytes(), &b)
	if a.SheetID != b.SheetID {
		t.Fatalf("two calls produced sheets %d and %d; the second must reuse the first", a.SheetID, b.SheetID)
	}

	var sheets int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=1`).Scan(&sheets)
	if sheets != 1 {
		t.Fatalf("%d sheets created for one product", sheets)
	}

	// The product must end up linked, otherwise the sheet is an orphan.
	var linked int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM comida_items WHERE id=?`, itemID).Scan(&linked)
	if linked != a.SheetID {
		t.Fatalf("product links to %d, want %d", linked, a.SheetID)
	}
}

func TestEnsureSheetReusesAnExistingLink(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	existing := createSheet(t, s, "Ficha existente")
	patchProduct(t, s, itemID, `{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(existing, 10)+`}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetEnsureForProduct(rec, sheetReq("POST", "/x",
		`{"itemId":`+strconv.FormatInt(itemID, 10)+`,"name":"Paella","source":"comida"}`, nil))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		SheetID int64 `json:"sheetId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.SheetID != existing {
		t.Fatalf("returned sheet %d, want the already linked %d", out.SheetID, existing)
	}
}

// Reverting to "Materia prima" cleared the product's link but left the draft
// sheet behind. Those orphans accumulate in the stock catalogue with no way to
// reach them, so an untouched draft is removed with its link.
func TestRevertingToRawDiscardsAnUntouchedDraftSheet(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Arroz negro")

	ensure := httptest.NewRecorder()
	s.handleBOTechnicalSheetEnsureForProduct(ensure, sheetReq("POST", "/x",
		`{"itemId":`+strconv.FormatInt(itemID, 10)+`,"name":"Arroz negro","source":"comida"}`, nil))
	if ensure.Code != 200 {
		t.Fatalf("ensure status %d body %s", ensure.Code, ensure.Body.String())
	}

	patchProduct(t, s, itemID, `{"productionType":"RAW"}`)

	var sheets int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=1`).Scan(&sheets)
	if sheets != 0 {
		t.Fatalf("%d draft sheets left behind after reverting to raw", sheets)
	}
}

// A sheet someone has actually worked on is NOT discarded: losing typed
// ingredients because of a mis-click would be far worse than an orphan row.
func TestRevertingToRawKeepsASheetThatHasIngredients(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")

	ensure := httptest.NewRecorder()
	s.handleBOTechnicalSheetEnsureForProduct(ensure, sheetReq("POST", "/x",
		`{"itemId":`+strconv.FormatInt(itemID, 10)+`,"name":"Paella","source":"comida"}`, nil))
	var created struct {
		SheetID int64 `json:"sheetId"`
	}
	json.Unmarshal(ensure.Body.Bytes(), &created)

	ingredient, unit := seedIngredient(t, s, "Arroz", "g", "MASS", 1, "")
	addComponent(t, s, created.SheetID, `{"stockItemId":`+strconv.FormatInt(ingredient, 10)+
		`,"unitId":`+strconv.FormatInt(unit, 10)+`,"quantity":100}`)

	patchProduct(t, s, itemID, `{"productionType":"RAW"}`)

	var sheets int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipes WHERE restaurant_id=1 AND id=?`, created.SheetID).Scan(&sheets)
	if sheets != 1 {
		t.Fatal("a sheet with ingredients must survive; the user's work is not disposable")
	}
}
