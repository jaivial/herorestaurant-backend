package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

// The comida list is what the "Añadir/Editar" modal hydrates from. If it does
// not carry production_type and the stock links, the modal cannot show whether
// a dish is elaborated, and any edit would silently drop the link.
func TestComidaListExposesProductionTypeAndStockLinks(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Paella")
	sheetID := createSheet(t, s, "Ficha paella")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&outputItemID)

	if rec := patchProduct(t, s, itemID,
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`}`); rec.Code != 200 {
		t.Fatalf("link failed: %s", rec.Body.String())
	}

	rec := httptest.NewRecorder()
	s.handleBOComidaList(rec, sheetReq("GET", "/x?pageSize=50", "", map[string]string{"tipo": "platos"}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Items []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
			StockItemID    *int64 `json:"stock_item_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, item := range body.Items {
		if int64(item.Num) != itemID {
			continue
		}
		found = true
		if item.ProductionType != "MANUFACTURED" {
			t.Fatalf("production_type=%q want MANUFACTURED", item.ProductionType)
		}
		if item.StockRecipeID == nil || *item.StockRecipeID != sheetID {
			t.Fatalf("stock_recipe_id=%v want %d", item.StockRecipeID, sheetID)
		}
		// Without the item link the POS knows the recipe but not what to deduct.
		if item.StockItemID == nil || *item.StockItemID != outputItemID {
			t.Fatalf("stock_item_id=%v want %d", item.StockItemID, outputItemID)
		}
	}
	if !found {
		t.Fatalf("seeded item %d missing from list; body=%s", itemID, rec.Body.String())
	}
}

// An unlinked product must report RAW rather than an empty string, so the UI
// has a definite value to render instead of guessing.
func TestComidaListReportsRawForUnlinkedProducts(t *testing.T) {
	s := sheetsTestServer(t)
	itemID := seedComidaItem(t, s, "Refresco")

	rec := httptest.NewRecorder()
	s.handleBOComidaList(rec, sheetReq("GET", "/x?pageSize=50", "", map[string]string{"tipo": "platos"}))

	var body struct {
		Items []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)

	for _, item := range body.Items {
		if int64(item.Num) == itemID {
			if item.ProductionType != "RAW" {
				t.Fatalf("production_type=%q want RAW", item.ProductionType)
			}
			if item.StockRecipeID != nil {
				t.Fatalf("stock_recipe_id=%v want null", *item.StockRecipeID)
			}
			return
		}
	}
	t.Fatalf("seeded item %d missing from the list", itemID)
}

func seedVino(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO VINOS (restaurant_id,tipo,nombre,precio,active) VALUES (1,'TINTO',?,12.5,1)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// Wine is a separate table with its own editor, so every comida change has to
// be mirrored for VINOS or the wine screen silently loses the feature.
func TestVinoListExposesProductionTypeAndStockLinks(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Rioja de prueba")
	sheetID := createSheet(t, s, "Ficha sangria")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&outputItemID)

	if _, err := s.db.Exec(
		`UPDATE VINOS SET production_type='MANUFACTURED', stock_recipe_id=?, stock_item_id=?
		  WHERE restaurant_id=1 AND num=?`, sheetID, outputItemID, vinoID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.handleBOComidaList(rec, sheetReq("GET", "/x?pageSize=50", "", map[string]string{"tipo": "vinos"}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
			StockItemID    *int64 `json:"stock_item_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Items {
		if int64(item.Num) != vinoID {
			continue
		}
		if item.ProductionType != "MANUFACTURED" {
			t.Fatalf("production_type=%q want MANUFACTURED", item.ProductionType)
		}
		if item.StockRecipeID == nil || *item.StockRecipeID != sheetID {
			t.Fatalf("stock_recipe_id=%v want %d", item.StockRecipeID, sheetID)
		}
		if item.StockItemID == nil || *item.StockItemID != outputItemID {
			t.Fatalf("stock_item_id=%v want %d", item.StockItemID, outputItemID)
		}
		return
	}
	t.Fatalf("seeded wine %d missing from the list; body=%s", vinoID, rec.Body.String())
}

func TestVinoDefaultsToRaw(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Albarino")

	rec := httptest.NewRecorder()
	s.handleBOComidaList(rec, sheetReq("GET", "/x?pageSize=50", "", map[string]string{"tipo": "vinos"}))
	var body struct {
		Items []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	for _, item := range body.Items {
		if int64(item.Num) == vinoID {
			if item.ProductionType != "RAW" {
				t.Fatalf("production_type=%q want RAW; a bottle is bought, not produced", item.ProductionType)
			}
			return
		}
	}
	t.Fatalf("seeded wine %d missing from the list", vinoID)
}

// The link endpoint must accept wine as well, otherwise the wine UI would have
// controls that cannot save.
func TestProductionTypePatchWorksForVinos(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Sangria de la casa")
	sheetID := createSheet(t, s, "Ficha sangria")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&outputItemID)

	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"vinos"}`,
		map[string]string{"id": strconv.FormatInt(vinoID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var productionType string
	var recipe, item int64
	s.db.QueryRow(`SELECT production_type,COALESCE(stock_recipe_id,0),COALESCE(stock_item_id,0)
		FROM VINOS WHERE restaurant_id=1 AND num=?`, vinoID).Scan(&productionType, &recipe, &item)
	if productionType != "MANUFACTURED" || recipe != sheetID || item != outputItemID {
		t.Fatalf("wine row: type=%s recipe=%d item=%d", productionType, recipe, item)
	}
}

func TestRevertingAVinoToRawClearsItsLink(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Sangria")
	sheetID := createSheet(t, s, "Ficha")

	patch := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x", body,
			map[string]string{"id": strconv.FormatInt(vinoID, 10)}))
		return rec
	}
	patch(`{"productionType":"MANUFACTURED","stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `,"source":"vinos"}`)
	if rec := patch(`{"productionType":"RAW","source":"vinos"}`); rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var linked int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM VINOS WHERE restaurant_id=1 AND num=?`, vinoID).Scan(&linked)
	if linked != 0 {
		t.Fatalf("stock_recipe_id=%d must be cleared when the wine goes back to RAW", linked)
	}
}

func seedPostre(t *testing.T, s *Server, description string) int64 {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO POSTRES (restaurant_id,DESCRIPCION,active) VALUES (1,?,1)`, description)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// Postres were the only catalogue type with no stock link at all. They are a
// third separate table, so nothing about them is covered by the comida or wine
// tests.
func TestPostreListExposesProductionTypeAndStockLinks(t *testing.T) {
	s := sheetsTestServer(t)
	postreID := seedPostre(t, s, "Tarta de queso")
	sheetID := createSheet(t, s, "Ficha tarta")
	var outputItemID int64
	s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, sheetID).Scan(&outputItemID)

	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"postres"}`,
		map[string]string{"id": strconv.FormatInt(postreID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("patch status %d body %s", rec.Code, rec.Body.String())
	}

	list := httptest.NewRecorder()
	s.handleBOComidaList(list, sheetReq("GET", "/x?pageSize=100", "", map[string]string{"tipo": "postres"}))
	if list.Code != 200 {
		t.Fatalf("list status %d body %s", list.Code, list.Body.String())
	}
	var body struct {
		Items []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
			StockItemID    *int64 `json:"stock_item_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Items {
		if int64(item.Num) != postreID {
			continue
		}
		if item.ProductionType != "MANUFACTURED" {
			t.Fatalf("production_type=%q want MANUFACTURED", item.ProductionType)
		}
		if item.StockRecipeID == nil || *item.StockRecipeID != sheetID {
			t.Fatalf("stock_recipe_id=%v want %d", item.StockRecipeID, sheetID)
		}
		if item.StockItemID == nil || *item.StockItemID != outputItemID {
			t.Fatalf("stock_item_id=%v want %d", item.StockItemID, outputItemID)
		}
		return
	}
	t.Fatalf("seeded postre %d missing from the list; body=%s", postreID, list.Body.String())
}

func TestPostreDefaultsToRaw(t *testing.T) {
	s := sheetsTestServer(t)
	postreID := seedPostre(t, s, "Flan")

	rec := httptest.NewRecorder()
	s.handleBOComidaList(rec, sheetReq("GET", "/x?pageSize=100", "", map[string]string{"tipo": "postres"}))
	var body struct {
		Items []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	for _, item := range body.Items {
		if int64(item.Num) == postreID {
			if item.ProductionType != "RAW" {
				t.Fatalf("production_type=%q want RAW", item.ProductionType)
			}
			return
		}
	}
	t.Fatalf("seeded postre %d missing from the list", postreID)
}

func TestRevertingAPostreToRawClearsItsLink(t *testing.T) {
	s := sheetsTestServer(t)
	postreID := seedPostre(t, s, "Brownie")
	sheetID := createSheet(t, s, "Ficha brownie")

	patch := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x", body,
			map[string]string{"id": strconv.FormatInt(postreID, 10)}))
		return rec
	}
	patch(`{"productionType":"MANUFACTURED","stockRecipeId":` + strconv.FormatInt(sheetID, 10) + `,"source":"postres"}`)
	if rec := patch(`{"productionType":"RAW","source":"postres"}`); rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var linked int64
	s.db.QueryRow(`SELECT COALESCE(stock_recipe_id,0) FROM POSTRES WHERE restaurant_id=1 AND NUM=?`, postreID).Scan(&linked)
	if linked != 0 {
		t.Fatalf("stock_recipe_id=%d must be cleared", linked)
	}
}

// Same rule as comida and wine: deleting a sheet a dessert depends on would
// leave that dessert unable to deduct stock.
func TestSheetUsedByAPostreCannotBeDeleted(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ficha en uso")
	postreID := seedPostre(t, s, "Tiramisu")

	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"postres"}`,
		map[string]string{"id": strconv.FormatInt(postreID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("link status %d body %s", rec.Code, rec.Body.String())
	}

	del := httptest.NewRecorder()
	s.handleBOTechnicalSheetDelete(del, sheetReq("DELETE", "/x", "",
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if del.Code != 409 {
		t.Fatalf("status %d want 409 while a postre uses the sheet", del.Code)
	}
}

// The detail endpoint is what the wine editor hydrates from after a reload. If
// it omits production_type, a saved "Preparado" wine silently reappears as
// "Materia prima" even though the database says otherwise.
func TestVinoDetailExposesProductionTypeAndStockLinks(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Sangria de la casa")
	sheetID := createSheet(t, s, "Ficha sangria")

	rec := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(rec, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"vinos"}`,
		map[string]string{"id": strconv.FormatInt(vinoID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("patch status %d body %s", rec.Code, rec.Body.String())
	}

	get := httptest.NewRecorder()
	s.handleBOComidaGet(get, sheetReq("GET", "/x", "", map[string]string{
		"tipo": "vinos", "id": strconv.FormatInt(vinoID, 10),
	}))
	if get.Code != 200 {
		t.Fatalf("get status %d body %s", get.Code, get.Body.String())
	}

	var body struct {
		Vino struct {
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
		} `json:"vino"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Vino.ProductionType != "MANUFACTURED" {
		t.Fatalf("production_type=%q want MANUFACTURED; the editor would show the wrong value after a reload",
			body.Vino.ProductionType)
	}
	if body.Vino.StockRecipeID == nil || *body.Vino.StockRecipeID != sheetID {
		t.Fatalf("stock_recipe_id=%v want %d", body.Vino.StockRecipeID, sheetID)
	}
}

// /api/admin/vinos/{id} is the endpoint the wine detail PAGE loads from, which
// is a different handler from /comida/vinos/{id}. Fixing only the latter left
// the page hydrating without production_type, so a saved "Preparado" wine came
// back as "Materia prima" after a reload.
func TestBOVinoGetExposesProductionTypeAndStockLinks(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Sangria de la casa")
	sheetID := createSheet(t, s, "Ficha sangria")

	patch := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(patch, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"vinos"}`,
		map[string]string{"id": strconv.FormatInt(vinoID, 10)}))
	if patch.Code != 200 {
		t.Fatalf("patch status %d body %s", patch.Code, patch.Body.String())
	}

	rec := httptest.NewRecorder()
	s.handleBOVinoGet(rec, sheetReq("GET", "/x", "", map[string]string{
		"id": strconv.FormatInt(vinoID, 10),
	}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Vino    struct {
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
		} `json:"vino"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success {
		t.Fatalf("request failed: %s", rec.Body.String())
	}
	if body.Vino.ProductionType != "MANUFACTURED" {
		t.Fatalf("production_type=%q want MANUFACTURED", body.Vino.ProductionType)
	}
	if body.Vino.StockRecipeID == nil || *body.Vino.StockRecipeID != sheetID {
		t.Fatalf("stock_recipe_id=%v want %d", body.Vino.StockRecipeID, sheetID)
	}
}

// The wine detail PAGE loads through vinos.get(), which in the client is
// implemented as a LIST call filtered by id. So the list handler is the one the
// page really depends on, and it must carry the stock link too.
func TestBOVinosListExposesProductionTypeAndStockLinks(t *testing.T) {
	s := sheetsTestServer(t)
	vinoID := seedVino(t, s, "Sangria de la casa")
	sheetID := createSheet(t, s, "Ficha sangria")

	patch := httptest.NewRecorder()
	s.handleBOComidaProductionTypePatch(patch, sheetReq("PATCH", "/x",
		`{"productionType":"MANUFACTURED","stockRecipeId":`+strconv.FormatInt(sheetID, 10)+`,"source":"vinos"}`,
		map[string]string{"id": strconv.FormatInt(vinoID, 10)}))
	if patch.Code != 200 {
		t.Fatalf("patch status %d body %s", patch.Code, patch.Body.String())
	}

	rec := httptest.NewRecorder()
	s.handleBOVinosList(rec, sheetReq("GET", "/x?limit=500", "", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Vinos []struct {
			Num            int    `json:"num"`
			ProductionType string `json:"production_type"`
			StockRecipeID  *int64 `json:"stock_recipe_id"`
		} `json:"vinos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, wine := range body.Vinos {
		if int64(wine.Num) != vinoID {
			continue
		}
		if wine.ProductionType != "MANUFACTURED" {
			t.Fatalf("production_type=%q want MANUFACTURED", wine.ProductionType)
		}
		if wine.StockRecipeID == nil || *wine.StockRecipeID != sheetID {
			t.Fatalf("stock_recipe_id=%v want %d", wine.StockRecipeID, sheetID)
		}
		return
	}
	t.Fatalf("seeded wine %d missing from the list", vinoID)
}
