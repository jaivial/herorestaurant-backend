package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func addStep(t *testing.T, s *Server, sheetID int64, body string) int64 {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepCreate(rec, sheetReq("POST", "/x", body,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		StepID int64 `json:"stepId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.StepID
}

func stepNumbers(t *testing.T, s *Server, sheetID int64) []int {
	t.Helper()
	rows, err := s.db.Query(`SELECT step_no FROM stock_recipe_steps
		WHERE restaurant_id=1 AND recipe_id=? ORDER BY step_no`, sheetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func TestStepsAreNumberedSequentiallyFromOne(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	addStep(t, s, sheetID, `{"title":"Sofreir","description":"Sofreir la cebolla"}`)
	addStep(t, s, sheetID, `{"title":"Hervir","description":"Anadir agua"}`)

	if got := stepNumbers(t, s, sheetID); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("step numbers %v want [1 2]", got)
	}
}

// A cook reads steps by number, so deleting step 2 of 3 must leave 1,2 - not a
// hole at 2 or a duplicate.
func TestDeletingAStepRenumbersTheRest(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	addStep(t, s, sheetID, `{"description":"Uno"}`)
	second := addStep(t, s, sheetID, `{"description":"Dos"}`)
	addStep(t, s, sheetID, `{"description":"Tres"}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepDelete(rec, sheetReq("DELETE", "/x", "", map[string]string{
		"id": strconv.FormatInt(sheetID, 10), "stepId": strconv.FormatInt(second, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	got := stepNumbers(t, s, sheetID)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("step numbers %v want [1 2] with no gap", got)
	}
	var remaining string
	s.db.QueryRow(`SELECT description FROM stock_recipe_steps
		WHERE restaurant_id=1 AND recipe_id=? AND step_no=2`, sheetID).Scan(&remaining)
	if remaining != "Tres" {
		t.Fatalf("step 2 is %q want Tres", remaining)
	}
}

func TestReorderStepsAppliesTheGivenSequence(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	a := addStep(t, s, sheetID, `{"description":"A"}`)
	b := addStep(t, s, sheetID, `{"description":"B"}`)
	c := addStep(t, s, sheetID, `{"description":"C"}`)

	rec := httptest.NewRecorder()
	body := `{"stepIds":[` + strconv.FormatInt(c, 10) + `,` + strconv.FormatInt(a, 10) + `,` + strconv.FormatInt(b, 10) + `]}`
	s.handleBOTechnicalSheetStepsReorder(rec, sheetReq("PUT", "/x", body,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var first string
	s.db.QueryRow(`SELECT description FROM stock_recipe_steps
		WHERE restaurant_id=1 AND recipe_id=? AND step_no=1`, sheetID).Scan(&first)
	if first != "C" {
		t.Fatalf("step 1 is %q want C", first)
	}
}

// A reorder naming only some steps would silently drop the others from the
// numbering, so it must be rejected outright.
func TestReorderRejectsAnIncompleteStepList(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	a := addStep(t, s, sheetID, `{"description":"A"}`)
	addStep(t, s, sheetID, `{"description":"B"}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepsReorder(rec, sheetReq("PUT", "/x",
		`{"stepIds":[`+strconv.FormatInt(a, 10)+`]}`,
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
	if got := stepNumbers(t, s, sheetID); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("numbering %v must be untouched after a rejected reorder", got)
	}
}

func TestReorderRejectsStepsFromAnotherSheet(t *testing.T) {
	s := sheetsTestServer(t)
	sheetA := createSheet(t, s, "A")
	sheetB := createSheet(t, s, "B")
	ownStep := addStep(t, s, sheetA, `{"description":"A1"}`)
	foreign := addStep(t, s, sheetB, `{"description":"B1"}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepsReorder(rec, sheetReq("PUT", "/x",
		`{"stepIds":[`+strconv.FormatInt(foreign, 10)+`,`+strconv.FormatInt(ownStep, 10)+`]}`,
		map[string]string{"id": strconv.FormatInt(sheetA, 10)}))
	if rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

func TestPatchStepUpdatesText(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	stepID := addStep(t, s, sheetID, `{"title":"Viejo","description":"Texto viejo"}`)

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepPatch(rec, sheetReq("PATCH", "/x", `{"title":"Nuevo","description":"Texto nuevo"}`,
		map[string]string{"id": strconv.FormatInt(sheetID, 10), "stepId": strconv.FormatInt(stepID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var title, description string
	s.db.QueryRow(`SELECT title,description FROM stock_recipe_steps WHERE restaurant_id=1 AND id=?`, stepID).
		Scan(&title, &description)
	if title != "Nuevo" || description != "Texto nuevo" {
		t.Fatalf("title=%q description=%q", title, description)
	}
}

func TestStepEndpointsAreTenantScoped(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ajena")
	req := sheetReq("POST", "/x", `{"description":"intruso"}`, map[string]string{"id": strconv.FormatInt(sheetID, 10)})
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepCreate(rec, req)
	if rec.Code == 200 {
		t.Fatal("a foreign tenant must not add steps")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_steps WHERE recipe_id=?`, sheetID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d steps leaked", n)
	}
}
