package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func seedPlatoCategory(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id,name,slug,source,active) VALUES (1,?,?,'PLATO',1)`,
		name, strings.ToLower(name))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func putScope(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOStockMarginScopePut(rec, sheetReq("PUT", "/x", body, nil))
	return rec
}

const fourValidBands = `"bands":[{"zone":"PURPLE","max":25},{"zone":"GREEN","min":25,"max":35},{"zone":"AMBER","min":35,"max":40},{"zone":"RED","min":40}]`

// A scope pointing at a category that does not exist can never resolve, so it
// is dead configuration the user will never understand. It must be refused at
// write time rather than silently stored.
func TestScopeRejectsAnUnknownComidaCategory(t *testing.T) {
	s := sheetsTestServer(t)
	rec := putScope(t, s, `{"scopeKind":"COMIDA_CATEGORY","scopeKey":"platos:999999","label":"Fantasma",`+fourValidBands+`}`)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400, body %s", rec.Code, rec.Body.String())
	}
}

func TestScopeAcceptsARealComidaCategory(t *testing.T) {
	s := sheetsTestServer(t)
	categoryID := seedPlatoCategory(t, s, "Entrantes")
	key := marginScopeKeyForCategory("platos", categoryID)

	rec := putScope(t, s, `{"scopeKind":"COMIDA_CATEGORY","scopeKey":"`+key+`","label":"Entrantes",`+fourValidBands+`}`)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

// The two category tables have colliding ids, so an unqualified key is
// ambiguous and must not be accepted.
func TestScopeRejectsAnUnqualifiedCategoryKey(t *testing.T) {
	s := sheetsTestServer(t)
	categoryID := seedPlatoCategory(t, s, "Entrantes")
	bare := int64ToString(categoryID)

	rec := putScope(t, s, `{"scopeKind":"COMIDA_CATEGORY","scopeKey":"`+bare+`","label":"Ambigua",`+fourValidBands+`}`)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400 for an unqualified category key", rec.Code)
	}
}

func TestScopeRejectsAnUnknownFoodType(t *testing.T) {
	s := sheetsTestServer(t)
	rec := putScope(t, s, `{"scopeKind":"COMIDA_TYPE","scopeKey":"inventado","label":"Inventado",`+fourValidBands+`}`)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// The targets endpoint answers "what food cost should this dish aim for",
// following the same inheritance chain as the bands.
func TestTargetResolvesFromTheMostSpecificScope(t *testing.T) {
	s := sheetsTestServer(t)
	categoryID := seedPlatoCategory(t, s, "Entrantes")
	key := marginScopeKeyForCategory("platos", categoryID)

	putScope(t, s, `{"scopeKind":"GLOBAL","scopeKey":"*","label":"Global","targetFoodCostPct":32,`+fourValidBands+`}`)
	putScope(t, s, `{"scopeKind":"COMIDA_CATEGORY","scopeKey":"`+key+`","label":"Entrantes","targetFoodCostPct":28,`+fourValidBands+`}`)

	rec := httptest.NewRecorder()
	s.handleBOStockMarginTargets(rec, sheetReq("GET",
		"/x?scopeKind=COMIDA_CATEGORY&foodType=platos&categoryId="+int64ToString(categoryID), "", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		TargetFoodCostPct *float64 `json:"targetFoodCostPct"`
		Source            string   `json:"source"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.TargetFoodCostPct == nil || *out.TargetFoodCostPct != 28 {
		t.Fatalf("target=%v want the category's 28, not the global 32", out.TargetFoodCostPct)
	}
	if out.Source != "COMIDA_CATEGORY" {
		t.Fatalf("source=%q want COMIDA_CATEGORY", out.Source)
	}
}

func TestTargetFallsBackToGlobal(t *testing.T) {
	s := sheetsTestServer(t)
	categoryID := seedPlatoCategory(t, s, "Postres")
	putScope(t, s, `{"scopeKind":"GLOBAL","scopeKey":"*","label":"Global","targetFoodCostPct":32,`+fourValidBands+`}`)

	rec := httptest.NewRecorder()
	s.handleBOStockMarginTargets(rec, sheetReq("GET",
		"/x?scopeKind=COMIDA_CATEGORY&foodType=platos&categoryId="+int64ToString(categoryID), "", nil))
	var out struct {
		TargetFoodCostPct *float64 `json:"targetFoodCostPct"`
		Source            string   `json:"source"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.TargetFoodCostPct == nil || *out.TargetFoodCostPct != 32 {
		t.Fatalf("target=%v want the global 32", out.TargetFoodCostPct)
	}
	if out.Source != "GLOBAL" {
		t.Fatalf("source=%q want GLOBAL", out.Source)
	}
}

// No configured target is a real answer: the UI must show "not set" rather than
// invent a number.
func TestTargetIsNullWhenNothingIsConfigured(t *testing.T) {
	s := sheetsTestServer(t)
	rec := httptest.NewRecorder()
	s.handleBOStockMarginTargets(rec, sheetReq("GET", "/x?scopeKind=GLOBAL", "", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var out struct {
		TargetFoodCostPct *float64 `json:"targetFoodCostPct"`
		Source            string   `json:"source"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.TargetFoodCostPct != nil {
		t.Fatalf("target=%v want null when no scope defines one", *out.TargetFoodCostPct)
	}
	if out.Source != "" {
		t.Fatalf("source=%q want empty", out.Source)
	}
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
