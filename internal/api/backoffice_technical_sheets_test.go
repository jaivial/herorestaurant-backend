package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/config"
)

func sheetsTestServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("MIGRATIONS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MIGRATIONS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Child rows are removed before their parents: a foreign key would silently
	// block the parent delete and leak rows into the next test.
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM comida_bulk_link_batches WHERE restaurant_id=1`,
			`DELETE FROM VINOS WHERE restaurant_id=1`,
			`DELETE FROM POSTRES WHERE restaurant_id=1`,
			`DELETE FROM comida_items WHERE restaurant_id=1`,
			`DELETE FROM stock_margin_scope_bands WHERE restaurant_id=1`,
			`DELETE FROM stock_margin_scopes WHERE restaurant_id=1`,
			`DELETE FROM comida_plato_categories WHERE restaurant_id=1`,
			`DELETE FROM stock_recipe_steps WHERE restaurant_id=1`,
			`DELETE FROM stock_recipe_components WHERE restaurant_id=1`,
			`DELETE FROM stock_recipes WHERE restaurant_id=1`,
			`DELETE FROM stock_item_prices WHERE restaurant_id=1`,
			`DELETE FROM stock_item_units WHERE restaurant_id=1`,
			`DELETE FROM stock_items WHERE restaurant_id=1`,
		} {
			if _, err := db.Exec(statement); err != nil {
				t.Errorf("cleanup %q: %v", statement, err)
			}
		}
		db.Close()
	})
	if _, err := db.Exec(`INSERT IGNORE INTO restaurants(id) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	return NewServer(db, config.Config{})
}

func sheetReq(method, path, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	for k, v := range params {
		routeCtx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(withBOAuth(ctx, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
}

func createSheet(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetCreate(rec, sheetReq("POST", "/comida/technical-sheets", `{"name":"`+name+`","portions":4}`, nil))
	if rec.Code != 200 {
		t.Fatalf("create status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		SheetID int64 `json:"sheetId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.SheetID == 0 {
		t.Fatalf("no sheetId in %s", rec.Body.String())
	}
	return out.SheetID
}

// D4: creating a sheet must also create its output stock item and that item's
// base unit, in one transaction. A sheet without an output item can never be
// produced, and an orphan item pollutes the catalogue.
func TestCreateSheetAlsoCreatesOutputItemAndUnit(t *testing.T) {
	s := sheetsTestServer(t)
	id := createSheet(t, s, "Bechamel")

	var outputItemID int64
	var status string
	var portions int
	if err := s.db.QueryRow(`SELECT output_item_id,status,portions FROM stock_recipes WHERE restaurant_id=1 AND id=?`, id).
		Scan(&outputItemID, &status, &portions); err != nil {
		t.Fatal(err)
	}
	if outputItemID == 0 {
		t.Fatal("sheet has no output item")
	}
	if status != "DRAFT" {
		t.Fatalf("status=%s want DRAFT", status)
	}
	if portions != 4 {
		t.Fatalf("portions=%d want 4", portions)
	}

	var kind, baseUnit, deduction string
	if err := s.db.QueryRow(`SELECT kind,base_unit,deduction_source FROM stock_items WHERE restaurant_id=1 AND id=?`, outputItemID).
		Scan(&kind, &baseUnit, &deduction); err != nil {
		t.Fatal(err)
	}
	if kind != "SEMI_FINISHED" || baseUnit != "ud" || deduction != "SALE" {
		t.Fatalf("item kind=%s base=%s deduction=%s", kind, baseUnit, deduction)
	}

	var units int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, outputItemID).Scan(&units); err != nil {
		t.Fatal(err)
	}
	if units != 1 {
		t.Fatalf("got %d units want 1", units)
	}

	// The default path must keep producing the historical ud/1 unit, not just
	// any single row: the defaulting rule is exactly what this PR touched.
	var code, label string
	var factor float64
	if err := s.db.QueryRow(`SELECT code,label,factor_to_base FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, outputItemID).Scan(&code, &label, &factor); err != nil {
		t.Fatal(err)
	}
	if code != "ud" || label != "ud" || factor != 1 {
		t.Fatalf("unit code=%s label=%s factor=%v want ud/ud/1", code, label, factor)
	}
}

func TestCreateSheetRejectsEmptyName(t *testing.T) {
	s := sheetsTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetCreate(rec, sheetReq("POST", "/comida/technical-sheets", `{"name":"   "}`, nil))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_items WHERE restaurant_id=1`).Scan(&n)
	if n != 0 {
		t.Fatalf("a rejected create leaked %d stock items", n)
	}
}

// Stock creation lets the user pick the dimension and display unit the sheet's
// output article should use; the create must apply them instead of always
// forcing COUNT/ud.
func TestCreateSheetHonoursCustomOutputUnit(t *testing.T) {
	s := sheetsTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetCreate(rec, sheetReq("POST", "/comida/technical-sheets",
		`{"name":"Pure de patata","portions":4,"baseDimension":"MASS","displayUnitCode":"kg","displayUnitLabel":"kg","displayUnitFactor":1000}`, nil))
	if rec.Code != 200 {
		t.Fatalf("create status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		SheetID int64 `json:"sheetId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var outputItemID int64
	if err := s.db.QueryRow(`SELECT output_item_id FROM stock_recipes WHERE restaurant_id=1 AND id=?`, out.SheetID).Scan(&outputItemID); err != nil {
		t.Fatal(err)
	}
	var dimension, baseUnit string
	if err := s.db.QueryRow(`SELECT base_dimension,base_unit FROM stock_items WHERE restaurant_id=1 AND id=?`, outputItemID).Scan(&dimension, &baseUnit); err != nil {
		t.Fatal(err)
	}
	if dimension != "MASS" || baseUnit != "g" {
		t.Fatalf("output item dimension=%s base=%s want MASS/g", dimension, baseUnit)
	}
	var code, label string
	var factor float64
	if err := s.db.QueryRow(`SELECT code,label,factor_to_base FROM stock_item_units WHERE restaurant_id=1 AND stock_item_id=?`, outputItemID).Scan(&code, &label, &factor); err != nil {
		t.Fatal(err)
	}
	if code != "kg" || label != "kg" || factor != 1000 {
		t.Fatalf("unit code=%s label=%s factor=%v want kg/kg/1000", code, label, factor)
	}
}

func TestCreateSheetRejectsInvalidBaseDimension(t *testing.T) {
	s := sheetsTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetCreate(rec, sheetReq("POST", "/comida/technical-sheets",
		`{"name":"X","baseDimension":"BOGUS"}`, nil))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// An explicit factor of zero or negative is a client bug, not an omission:
// reject it instead of silently storing factor 1.
func TestCreateSheetRejectsInvalidDisplayUnitFactor(t *testing.T) {
	s := sheetsTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetCreate(rec, sheetReq("POST", "/comida/technical-sheets",
		`{"name":"X","displayUnitFactor":0}`, nil))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_items WHERE restaurant_id=1`).Scan(&n)
	if n != 0 {
		t.Fatalf("a rejected create leaked %d stock items", n)
	}
}

// Publishing a sheet with no ingredients would put a costless, allergen-free
// dish on the menu.
func TestPublishRequiresAtLeastOneComponent(t *testing.T) {
	s := sheetsTestServer(t)
	id := createSheet(t, s, "Vacia")

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetPublish(rec, sheetReq("POST", "/x", "", map[string]string{"id": strconv.FormatInt(id, 10)}))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400 body %s", rec.Code, rec.Body.String())
	}
	var status string
	s.db.QueryRow(`SELECT status FROM stock_recipes WHERE restaurant_id=1 AND id=?`, id).Scan(&status)
	if status != "DRAFT" {
		t.Fatalf("status=%s want DRAFT", status)
	}
}

func TestGetSheetIsTenantScoped(t *testing.T) {
	s := sheetsTestServer(t)
	id := createSheet(t, s, "Privada")

	req := httptest.NewRequest("GET", "/x", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", strconv.FormatInt(id, 10))
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	// Another tenant must not be able to read this sheet.
	req = req.WithContext(withBOAuth(ctx, boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetGet(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status %d want 404 for a foreign tenant", rec.Code)
	}
}

func TestPatchSheetUpdatesEditableFields(t *testing.T) {
	s := sheetsTestServer(t)
	id := createSheet(t, s, "Original")

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetPatch(rec, sheetReq("PATCH", "/x",
		`{"name":"Renombrada","portions":8,"prepTimeMin":25,"wastePct":5}`,
		map[string]string{"id": strconv.FormatInt(id, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var name string
	var portions, prep int
	var waste float64
	if err := s.db.QueryRow(`SELECT name,portions,prep_time_min,waste_pct FROM stock_recipes WHERE restaurant_id=1 AND id=?`, id).
		Scan(&name, &portions, &prep, &waste); err != nil {
		t.Fatal(err)
	}
	if name != "Renombrada" || portions != 8 || prep != 25 || waste != 5 {
		t.Fatalf("got name=%s portions=%d prep=%d waste=%v", name, portions, prep, waste)
	}
}

func TestPatchSheetRejectsInvalidPortions(t *testing.T) {
	s := sheetsTestServer(t)
	id := createSheet(t, s, "Porciones")
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetPatch(rec, sheetReq("PATCH", "/x", `{"portions":0}`,
		map[string]string{"id": strconv.FormatInt(id, 10)}))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// A sheet's allergen endpoint must refuse to disable a derived allergen even if
// the client asks nicely. The UI lock is not the security boundary.
func TestPatchAllergensCannotDisableDerived(t *testing.T) {
	s := sheetsTestServer(t)
	id := createSheet(t, s, "Con gluten")

	// Give the sheet a component whose item declares Gluten.
	res, err := s.db.Exec(`INSERT INTO stock_items (restaurant_id,name,kind,base_dimension,base_unit,allergens_json)
		VALUES (1,'Harina','RAW','MASS','g',JSON_ARRAY('Gluten'))`)
	if err != nil {
		t.Fatal(err)
	}
	itemID, _ := res.LastInsertId()
	unitRes, err := s.db.Exec(`INSERT INTO stock_item_units (restaurant_id,stock_item_id,code,label,factor_to_base,can_recipe)
		VALUES (1,?,'g','g',1,1)`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	unitID, _ := unitRes.LastInsertId()
	if _, err := s.db.Exec(`INSERT INTO stock_recipe_components (restaurant_id,recipe_id,stock_item_id,entered_qty,entered_unit_id,qty_base)
		VALUES (1,?,?,500,?,500)`, id, itemID, unitID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetAllergensPatch(rec, sheetReq("PATCH", "/x",
		`{"added":["Soja"],"disabled":["Gluten"]}`,
		map[string]string{"id": strconv.FormatInt(id, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleBOTechnicalSheetAllergensGet(rec, sheetReq("GET", "/x", "", map[string]string{"id": strconv.FormatInt(id, 10)}))
	var out struct {
		Derived      []string            `json:"derived"`
		Final        []string            `json:"final"`
		Contributors map[string][]string `json:"contributors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v body %s", err, rec.Body.String())
	}
	joined := strings.Join(out.Final, ",")
	if !strings.Contains(joined, "Gluten") {
		t.Fatalf("Gluten was removed despite being derived: %v", out.Final)
	}
	if !strings.Contains(joined, "Soja") {
		t.Fatalf("manual add missing: %v", out.Final)
	}
	if len(out.Contributors["Gluten"]) == 0 {
		t.Fatalf("no contributor reported for Gluten: %+v", out.Contributors)
	}
}

// The PATCH response is what the editor renders after a manual add, so it has to
// carry the same fields as the GET. Returning only the final list left the UI
// with no derived/contributor data and made a just-added allergen vanish.
func TestAllergensPatchReturnsTheSameShapeAsGet(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ficha")

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetAllergensPatch(rec, sheetReq("PATCH", "/x", `{"added":["Soja"]}`,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Derived      *[]string            `json:"derived"`
		ManualAdded  *[]string            `json:"manualAdded"`
		Effective    *[]string            `json:"effective"`
		Contributors *map[string][]string `json:"contributors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v body %s", err, rec.Body.String())
	}
	for name, present := range map[string]bool{
		"derived":      out.Derived != nil,
		"manualAdded":  out.ManualAdded != nil,
		"effective":    out.Effective != nil,
		"contributors": out.Contributors != nil,
	} {
		if !present {
			t.Fatalf("PATCH response is missing %q: %s", name, rec.Body.String())
		}
	}
	if len(*out.Effective) != 1 || (*out.Effective)[0] != "Soja" {
		t.Fatalf("effective %v want [Soja]", *out.Effective)
	}
	if len(*out.ManualAdded) != 1 || (*out.ManualAdded)[0] != "Soja" {
		t.Fatalf("manualAdded %v want [Soja]", *out.ManualAdded)
	}
}

// GET must also expose "effective": the client reads that name, and a response
// that only says "final" left the chip list permanently empty.
func TestAllergensGetExposesEffective(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ficha")

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetAllergensPatch(rec, sheetReq("PATCH", "/x", `{"added":["Leche"]}`,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("patch status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleBOTechnicalSheetAllergensGet(rec, sheetReq("GET", "/x", "",
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	var out struct {
		Effective []string `json:"effective"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Effective) != 1 || out.Effective[0] != "Leche" {
		t.Fatalf("effective %v want [Leche]: %s", out.Effective, rec.Body.String())
	}
}
