package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func ptrString(s string) *string { return &s }
func ptrBool(b bool) *bool       { return &b }

// categoryReq builds a request already carrying a backoffice session for the given
// restaurant, so the handlers can be driven without going through the router.
func categoryReq(method, path, body string, restaurantID int, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	for k, v := range params {
		routeCtx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(withBOAuth(ctx, boAuth{ActiveRestaurantID: restaurantID, Role: "admin", User: boUser{ID: 7}}))
}

type categoryEnvelope struct {
	Success  bool                          `json:"success"`
	Category comidaUnifiedCategoryResponse `json:"category"`
}

type categoryListEnvelope struct {
	Success    bool                            `json:"success"`
	Categories []comidaUnifiedCategoryResponse `json:"categories"`
}

func createCategory(t *testing.T, s *Server, restaurantID int, body string) comidaUnifiedCategoryResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaCategoryCreate(rec, categoryReq(http.MethodPost, "/comida/categorias", body, restaurantID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("create %s: status=%d body=%s", body, rec.Code, rec.Body.String())
	}
	var out categoryEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Category.ID == 0 {
		t.Fatalf("create %s returned no id: %s", body, rec.Body.String())
	}
	return out.Category
}

func createCategoryStatus(t *testing.T, s *Server, restaurantID int, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaCategoryCreate(rec, categoryReq(http.MethodPost, "/comida/categorias", body, restaurantID, nil))
	return rec.Code, rec.Body.String()
}

func patchCategory(t *testing.T, s *Server, restaurantID, id int, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaCategoryPatch(rec, categoryReq(http.MethodPatch, "/comida/categorias/"+strconv.Itoa(id), body, restaurantID, map[string]string{"id": strconv.Itoa(id)}))
	return rec.Code, rec.Body.String()
}

func deleteCategory(t *testing.T, s *Server, restaurantID, id int) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaCategoryDelete(rec, categoryReq(http.MethodDelete, "/comida/categorias/"+strconv.Itoa(id), "", restaurantID, map[string]string{"id": strconv.Itoa(id)}))
	return rec.Code, rec.Body.String()
}

func listCategories(t *testing.T, s *Server, restaurantID int, query string) []comidaUnifiedCategoryResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOComidaCategoriesList(rec, categoryReq(http.MethodGet, "/comida/categorias"+query, "", restaurantID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list %q: status=%d body=%s", query, rec.Code, rec.Body.String())
	}
	var out categoryListEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Categories
}

func categoryNamed(list []comidaUnifiedCategoryResponse, name string) (comidaUnifiedCategoryResponse, bool) {
	for _, c := range list {
		if c.Name == name {
			return c, true
		}
	}
	return comidaUnifiedCategoryResponse{}, false
}

func insertComidaItem(t *testing.T, s *Server, restaurantID int, sourceType, name, categoria string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO comida_items (restaurant_id, source_type, nombre, categoria, active) VALUES (?, ?, ?, ?, 1)`,
		restaurantID, sourceType, name, categoria,
	); err != nil {
		t.Fatal(err)
	}
}

func itemCategoria(t *testing.T, s *Server, restaurantID int, name string) string {
	t.Helper()
	var got string
	if err := s.db.QueryRow(
		`SELECT COALESCE(categoria,'') FROM comida_items WHERE restaurant_id=? AND nombre=?`,
		restaurantID, name,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

// ---------------------------------------------------------------------------
// Scope resolution
// ---------------------------------------------------------------------------

func TestNormalizeComidaCategoryFoodTypeSpeaksTheSameVocabularyAsTheRestOfComida(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  string
		valid bool
	}{
		{"platos", "platos", true},
		{"bebidas", "bebidas", true},
		{"vinos", "vinos", true},
		{"cafes", "cafes", true},
		{"postres", "postres", true},
		{"  PLATOS  ", "platos", true},
		// Singular and accented forms are what normalizeComidaTipo accepts, so
		// GET /comida/vino and ?foodType=vino must not disagree.
		{"plato", "platos", true},
		{"vino", "vinos", true},
		{"café", "cafes", true},
		{"cafés", "cafes", true},
		{"", comidaCategoryGlobalType, true},
		{"global", comidaCategoryGlobalType, true},
		// "all" used to alias the global sentinel, which made ?foodType=all return
		// the fewest rows instead of the most. It is not a scope.
		{"all", "", false},
		{"entrantes", "", false},
	} {
		got, valid := normalizeComidaCategoryFoodType(tc.in)
		if valid != tc.valid {
			t.Fatalf("normalizeComidaCategoryFoodType(%q) valid=%v want %v", tc.in, valid, tc.valid)
		}
		if valid && got != tc.want {
			t.Fatalf("normalizeComidaCategoryFoodType(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveComidaCategoryScopeNeverReturnsTheScopeTheCallerRuledOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   comidaUnifiedCategoryWriteRequest
		want  string
		valid bool
	}{
		{"no scope means global", comidaUnifiedCategoryWriteRequest{}, comidaCategoryGlobalType, true},
		{"foodType alone", comidaUnifiedCategoryWriteRequest{FoodType: ptrString("cafes")}, "cafes", true},
		{"global true beats a contradictory foodType", comidaUnifiedCategoryWriteRequest{FoodType: ptrString("cafes"), Global: ptrBool(true)}, comidaCategoryGlobalType, true},
		{"global false with a real foodType", comidaUnifiedCategoryWriteRequest{FoodType: ptrString("vinos"), Global: ptrBool(false)}, "vinos", true},
		// The regression this guards: an empty foodType normalises to the global
		// sentinel, so "not global" used to produce a global category.
		{"global false with an empty foodType", comidaUnifiedCategoryWriteRequest{FoodType: ptrString(""), Global: ptrBool(false)}, "", false},
		{"global false with an explicit global foodType", comidaUnifiedCategoryWriteRequest{FoodType: ptrString("global"), Global: ptrBool(false)}, "", false},
		{"global false with no foodType", comidaUnifiedCategoryWriteRequest{Global: ptrBool(false)}, "", false},
		{"unknown foodType", comidaUnifiedCategoryWriteRequest{FoodType: ptrString("entrantes")}, "", false},
	} {
		got, valid := resolveComidaCategoryScope(tc.req)
		if valid != tc.valid {
			t.Fatalf("%s: valid=%v want %v", tc.name, valid, tc.valid)
		}
		if valid && got != tc.want {
			t.Fatalf("%s: scope=%q want %q", tc.name, got, tc.want)
		}
	}
}

func TestComidaCategoryUsageIsConstrainedToTheScopesTheCategoryReaches(t *testing.T) {
	// A cafes category must not be reported as in use by a plato that happens to
	// share its name, or the seeded base plato categories would make several names
	// permanently undeletable.
	for _, tc := range []struct {
		foodType string
		want     []string
		vinos    bool
	}{
		{comidaCategoryGlobalType, []string{"platos", "bebidas", "cafes"}, true},
		{"platos", []string{"platos"}, false},
		{"bebidas", []string{"bebidas"}, false},
		{"cafes", []string{"cafes"}, false},
		// Wines are categorised by VINOS.tipo, not by comida_items.
		{"vinos", nil, true},
		// POSTRES has no category column at all, so a postres category is never in
		// use and must not query a column that does not exist.
		{"postres", nil, false},
	} {
		if got := comidaCategoryItemSourceTypes(tc.foodType); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("comidaCategoryItemSourceTypes(%q)=%v want %v", tc.foodType, got, tc.want)
		}
		if v := comidaCategoryTouchesVinos(tc.foodType); v != tc.vinos {
			t.Fatalf("comidaCategoryTouchesVinos(%q)=%v want %v", tc.foodType, v, tc.vinos)
		}
	}
}

func TestValidateComidaCategoryNameBoundsWhatTheColumnAccepts(t *testing.T) {
	name, slug, err := validateComidaCategoryName("  Café con leche  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Café con leche" || slug != "cafe-con-leche" {
		t.Fatalf("got name=%q slug=%q", name, slug)
	}
	if _, _, err := validateComidaCategoryName("   "); err == nil {
		t.Fatal("an empty name must be rejected")
	}
	// VARCHAR(120) under strict mode raises a 1406, which would surface as a 500.
	if _, _, err := validateComidaCategoryName(strings.Repeat("á", comidaCategoryNameMaxLen+1)); err == nil {
		t.Fatal("an over-long name must be rejected before it reaches MySQL")
	}
	// Counted in runes, not bytes: 120 accented characters still fit.
	if _, _, err := validateComidaCategoryName(strings.Repeat("á", comidaCategoryNameMaxLen)); err != nil {
		t.Fatalf("a %d-rune name must be accepted: %v", comidaCategoryNameMaxLen, err)
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func TestCategoryCreateRoundTripsThroughTheScopedListing(t *testing.T) {
	s := sheetsTestServer(t)

	created := createCategory(t, s, 1, `{"name":"Tapas frías","foodType":"cafes"}`)
	if created.Slug != "tapas-frias" || created.Scope != "cafes" || created.IsGlobal {
		t.Fatalf("unexpected shape: %+v", created)
	}
	if !created.Editable || created.Origin != comidaCategoryOriginUnified {
		t.Fatalf("a category created here must be editable and unified: %+v", created)
	}

	got, ok := categoryNamed(listCategories(t, s, 1, "?foodType=cafes"), "Tapas frías")
	if !ok {
		t.Fatal("the created category is missing from its own scope")
	}
	if got.ID != created.ID || got.Key != created.Key {
		t.Fatalf("listing disagrees with create: %+v vs %+v", got, created)
	}
}

func TestGlobalCategoriesAppearInEveryTypeListing(t *testing.T) {
	s := sheetsTestServer(t)
	createCategory(t, s, 1, `{"name":"Temporada","global":true}`)

	for _, foodType := range comidaCategoryFoodTypes {
		if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType="+foodType), "Temporada"); !ok {
			t.Fatalf("a global category is missing from the %s listing", foodType)
		}
	}
}

func TestCategoriesAreScopedToTheirRestaurant(t *testing.T) {
	s := sheetsTestServer(t)
	mine := createCategory(t, s, 1, `{"name":"Solo mia","foodType":"vinos"}`)

	if _, ok := categoryNamed(listCategories(t, s, 2, "?foodType=vinos"), "Solo mia"); ok {
		t.Fatal("another restaurant can see this category")
	}
	// The same name is free for the other tenant, and creating it must not collide.
	theirs := createCategory(t, s, 2, `{"name":"Solo mia","foodType":"vinos"}`)
	if theirs.ID == mine.ID {
		t.Fatal("the two tenants share a row")
	}

	if code, body := patchCategory(t, s, 2, mine.ID, `{"name":"Robada"}`); code != http.StatusNotFound {
		t.Fatalf("patching another tenant's category: status=%d body=%s", code, body)
	}
	if code, body := deleteCategory(t, s, 2, mine.ID); code != http.StatusNotFound {
		t.Fatalf("deleting another tenant's category: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=vinos"), "Solo mia"); !ok {
		t.Fatal("the original category was touched by the other tenant")
	}
}

func TestCreateRejectsANameAlreadyVisibleInTheSameScope(t *testing.T) {
	s := sheetsTestServer(t)
	createCategory(t, s, 1, `{"name":"Entrantes","foodType":"cafes"}`)

	if code, body := createCategoryStatus(t, s, 1, `{"name":"entrantes","foodType":"cafes"}`); code != http.StatusConflict {
		t.Fatalf("a duplicate slug in the same scope: status=%d body=%s", code, body)
	}
	// A global category is shown next to every type's own, so the pair would be
	// indistinguishable in the picker and neither could be renamed safely.
	if code, body := createCategoryStatus(t, s, 1, `{"name":"Entrantes","global":true}`); code != http.StatusConflict {
		t.Fatalf("a global colliding with a type-scoped one: status=%d body=%s", code, body)
	}
	// The reverse direction has to be refused too.
	createCategory(t, s, 1, `{"name":"Temporada","global":true}`)
	if code, body := createCategoryStatus(t, s, 1, `{"name":"Temporada","foodType":"vinos"}`); code != http.StatusConflict {
		t.Fatalf("a type-scoped colliding with a global: status=%d body=%s", code, body)
	}
	// A different type does not overlap, so this one is legitimate.
	createCategory(t, s, 1, `{"name":"Entrantes","foodType":"vinos"}`)
}

func TestCreateRejectsANameTheLegacyTableAlreadyOwns(t *testing.T) {
	s := sheetsTestServer(t)
	// Reading the platos listing seeds the base legacy categories.
	legacy := listCategories(t, s, 1, "?foodType=platos")
	var seeded string
	for _, c := range legacy {
		if c.Origin == comidaCategoryOriginLegacy {
			seeded = c.Name
			break
		}
	}
	if seeded == "" {
		t.Skip("no legacy platos categories are seeded in this schema")
	}

	if code, body := createCategoryStatus(t, s, 1, `{"name":"`+seeded+`","foodType":"platos"}`); code != http.StatusConflict {
		t.Fatalf("a name the legacy table owns: status=%d body=%s", code, body)
	}
}

func TestCreateRejectsAnInvalidBody(t *testing.T) {
	s := sheetsTestServer(t)
	for _, tc := range []struct{ name, body string }{
		{"empty name", `{"name":"   ","foodType":"vinos"}`},
		{"over-long name", `{"name":"` + strings.Repeat("a", comidaCategoryNameMaxLen+1) + `","foodType":"vinos"}`},
		{"unknown food type", `{"name":"X","foodType":"entrantes"}`},
		{"contradictory scope", `{"name":"X","global":false}`},
		// A typo'd field must not be dropped in silence: this asked for a global
		// category and would otherwise have produced one scoped to nothing.
		{"unknown field", `{"name":"X","globl":true}`},
	} {
		if code, body := createCategoryStatus(t, s, 1, tc.body); code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", tc.name, code, body)
		}
	}
}

func TestRenamingACategoryCarriesItsProductsAlong(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Tapas","foodType":"cafes"}`)
	insertComidaItem(t, s, 1, "cafes", "Cortado", "Tapas")
	// A product of another type with the same category name must be left alone: the
	// category is scoped to cafes and does not speak for platos.
	insertComidaItem(t, s, 1, "platos", "Ensalada", "Tapas")

	if code, body := patchCategory(t, s, 1, cat.ID, `{"name":"Raciones"}`); code != http.StatusOK {
		t.Fatalf("rename: status=%d body=%s", code, body)
	}
	if got := itemCategoria(t, s, 1, "Cortado"); got != "Raciones" {
		t.Fatalf("the cafes product still reads %q", got)
	}
	if got := itemCategoria(t, s, 1, "Ensalada"); got != "Tapas" {
		t.Fatalf("a platos product was rewritten by a cafes rename: %q", got)
	}
	// The renamed category must not leave the old name behind as a second entry.
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=cafes"), "Tapas"); ok {
		t.Fatal("the old name is still listed after the rename")
	}
}

func TestRenamingAPlatosCategoryAlsoRenamesItsLegacyTwin(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Arroces","foodType":"platos"}`)

	// Saving a plato resolves the category against comida_plato_categories and
	// creates the row there, which is how a unified category grows a legacy twin.
	if _, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Arroces', 'arroces', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}
	insertComidaItem(t, s, 1, "platos", "Paella", "Arroces")

	if code, body := patchCategory(t, s, 1, cat.ID, `{"name":"Arroces y fideuás"}`); code != http.StatusOK {
		t.Fatalf("rename: status=%d body=%s", code, body)
	}

	list := listCategories(t, s, 1, "?foodType=platos")
	if _, ok := categoryNamed(list, "Arroces"); ok {
		t.Fatal("the legacy twin kept the old name and came back as a separate entry")
	}
	if _, ok := categoryNamed(list, "Arroces y fideuás"); !ok {
		t.Fatal("the renamed category is missing from the listing")
	}
	if got := itemCategoria(t, s, 1, "Paella"); got != "Arroces y fideuás" {
		t.Fatalf("the product still reads %q", got)
	}
}

func TestRenamingRejectsANameAlreadyVisibleInTheScope(t *testing.T) {
	s := sheetsTestServer(t)
	createCategory(t, s, 1, `{"name":"Blancos","foodType":"vinos"}`)
	tintos := createCategory(t, s, 1, `{"name":"Tintos","foodType":"vinos"}`)

	if code, body := patchCategory(t, s, 1, tintos.ID, `{"name":"blancos"}`); code != http.StatusConflict {
		t.Fatalf("renaming onto an existing name: status=%d body=%s", code, body)
	}
	// The failed rename must not have leaked into the products or the row.
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=vinos"), "Tintos"); !ok {
		t.Fatal("the category lost its name to a rename that was rejected")
	}
}

func TestAnInUseCategoryCannotBeRescopedOrDeactivated(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Infusiones","foodType":"cafes"}`)
	insertComidaItem(t, s, 1, "cafes", "Poleo", "Infusiones")

	// Moving it to another scope would strand the product: the usage check that
	// guards DELETE only looks in the category's current scope.
	if code, body := patchCategory(t, s, 1, cat.ID, `{"foodType":"vinos"}`); code != http.StatusConflict {
		t.Fatalf("re-scoping an in-use category: status=%d body=%s", code, body)
	}
	// Deactivating hides it from every picker while the product still carries it.
	if code, body := patchCategory(t, s, 1, cat.ID, `{"active":false}`); code != http.StatusConflict {
		t.Fatalf("deactivating an in-use category: status=%d body=%s", code, body)
	}
	// Renaming stays available, because it cascades.
	if code, body := patchCategory(t, s, 1, cat.ID, `{"name":"Infusiones y tés"}`); code != http.StatusOK {
		t.Fatalf("renaming an in-use category: status=%d body=%s", code, body)
	}
}

func TestAnUnusedCategoryCanBeRescopedAndDeactivated(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Sin uso","foodType":"cafes"}`)

	if code, body := patchCategory(t, s, 1, cat.ID, `{"foodType":"vinos"}`); code != http.StatusOK {
		t.Fatalf("re-scope: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=cafes"), "Sin uso"); ok {
		t.Fatal("the category is still listed under its old scope")
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=vinos"), "Sin uso"); !ok {
		t.Fatal("the category is missing from its new scope")
	}

	if code, body := patchCategory(t, s, 1, cat.ID, `{"active":false}`); code != http.StatusOK {
		t.Fatalf("deactivate: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=vinos"), "Sin uso"); ok {
		t.Fatal("a deactivated category is still offered by the picker")
	}
	// The config screen still has to see it, or it could never be switched back on.
	if _, ok := categoryNamed(listCategories(t, s, 1, ""), "Sin uso"); !ok {
		t.Fatal("a deactivated category vanished from the management listing")
	}
}

func TestDeleteRefusesWhileProductsStillReferenceTheCategory(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Digestivos","foodType":"cafes"}`)
	insertComidaItem(t, s, 1, "cafes", "Orujo", "Digestivos")

	if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusConflict {
		t.Fatalf("deleting an in-use category: status=%d body=%s", code, body)
	}

	if _, err := s.db.Exec(`DELETE FROM comida_items WHERE restaurant_id=1 AND nombre='Orujo'`); err != nil {
		t.Fatal(err)
	}
	if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusOK {
		t.Fatalf("deleting an unused category: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=cafes"), "Digestivos"); ok {
		t.Fatal("the deleted category is still listed")
	}
}

func TestDeletingACategoryAlsoRemovesItsLegacyTwin(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Arroces","foodType":"platos"}`)
	// Saving a plato materialises the twin, exactly as in the rename test.
	if _, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Arroces', 'arroces', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}

	if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", code, body)
	}

	// The twin is reported with id 0 and editable false, so a survivor would be a
	// catalogue entry no endpoint can ever remove.
	if got, ok := categoryNamed(listCategories(t, s, 1, "?foodType=platos"), "Arroces"); ok {
		t.Fatalf("the legacy twin outlived the delete: %+v", got)
	}
}

func TestDeletingACategoryLeavesSeededBaseCategoriesAlone(t *testing.T) {
	s := sheetsTestServer(t)
	// ensureBasePlatoCategories seeds the base rows on the first listing.
	base := listCategories(t, s, 1, "?foodType=platos")
	if len(base) == 0 {
		t.Skip("no seeded base plato categories in this schema")
	}
	seeded := base[0]

	cat := createCategory(t, s, 1, `{"name":"Arroces","foodType":"platos"}`)
	if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=platos"), seeded.Name); !ok {
		t.Fatalf("deleting a category took the seeded %q with it", seeded.Name)
	}
}

func TestAnInactiveLegacyCategoryDoesNotBlockTheNameItDoesNotShow(t *testing.T) {
	s := sheetsTestServer(t)
	// Nothing in the app lists an inactive legacy row, so refusing the name would
	// point the operator at a duplicate they cannot find.
	if _, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Fuera de carta', 'fuera-de-carta', 'custom', 0)`,
	); err != nil {
		t.Fatal(err)
	}

	if code, body := createCategoryStatus(t, s, 1, `{"name":"Fuera de carta","foodType":"platos"}`); code != http.StatusOK {
		t.Fatalf("an inactive legacy row blocked the name: status=%d body=%s", code, body)
	}
}

func TestDeleteIgnoresAProductOfAnotherTypeWithTheSameName(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Especiales","foodType":"cafes"}`)
	// A plato with a matching name must not pin the cafes category in place.
	insertComidaItem(t, s, 1, "platos", "Solomillo", "Especiales")

	if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
}

func TestALegacyEntryIsListedButNotWritable(t *testing.T) {
	s := sheetsTestServer(t)
	if _, err := s.db.Exec(
		`INSERT INTO comida_bebida_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Refrescos', 'refrescos', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}

	got, ok := categoryNamed(listCategories(t, s, 1, "?foodType=bebidas"), "Refrescos")
	if !ok {
		t.Fatal("the legacy category is missing from the bebidas listing")
	}
	if got.Editable || got.Origin != comidaCategoryOriginLegacy {
		t.Fatalf("a legacy row must not claim to be writable: %+v", got)
	}
	// Legacy ids collide with unified ones, so they are not exposed; a client that
	// tries to write to one addresses a category these endpoints cannot reach.
	if got.ID != 0 {
		t.Fatalf("a legacy row exposed id %d", got.ID)
	}
	if code, body := patchCategory(t, s, 1, 0, `{"name":"Zumos"}`); code != http.StatusNotFound {
		t.Fatalf("patching a legacy entry: status=%d body=%s", code, body)
	}
	if code, body := deleteCategory(t, s, 1, 0); code != http.StatusNotFound {
		t.Fatalf("deleting a legacy entry: status=%d body=%s", code, body)
	}
}

func TestTheManagementListingShowsTheSameCatalogueThePickersDo(t *testing.T) {
	s := sheetsTestServer(t)
	if _, err := s.db.Exec(
		`INSERT INTO comida_bebida_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Refrescos', 'refrescos', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}
	createCategory(t, s, 1, `{"name":"Reservas","foodType":"vinos"}`)

	all := listCategories(t, s, 1, "")
	for _, name := range []string{"Refrescos", "Reservas"} {
		if _, ok := categoryNamed(all, name); !ok {
			t.Fatalf("%q is offered by a picker but missing from the management listing", name)
		}
	}
}

func TestAListingNeverOffersTheSameNameTwice(t *testing.T) {
	s := sheetsTestServer(t)
	// A legacy row and a unified row can carry the same name: the legacy table
	// predates the unique index and nothing spans both.
	if _, err := s.db.Exec(
		`INSERT INTO comida_bebida_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Cervezas', 'cervezas', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (1, 'bebidas', 'Cervezas', 'cervezas', 1)`,
	); err != nil {
		t.Fatal(err)
	}

	list := listCategories(t, s, 1, "?foodType=bebidas")
	seen := 0
	var winner comidaUnifiedCategoryResponse
	for _, c := range list {
		if c.Name == "Cervezas" {
			seen++
			winner = c
		}
	}
	if seen != 1 {
		t.Fatalf("the picker offers %q %d times", "Cervezas", seen)
	}
	if !winner.Editable {
		t.Fatal("the entry the client can act on lost to the one it cannot")
	}
}

func TestAnUnauthenticatedRequestIsRejected(t *testing.T) {
	s := sheetsTestServer(t)
	for _, tc := range []struct {
		name string
		call http.HandlerFunc
	}{
		{"list", s.handleBOComidaCategoriesList},
		{"create", s.handleBOComidaCategoryCreate},
		{"patch", s.handleBOComidaCategoryPatch},
		{"delete", s.handleBOComidaCategoryDelete},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/comida/categorias", strings.NewReader(`{"name":"X"}`))
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", "1")
		tc.call(rec, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a session: status=%d body=%s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

func TestComidaCategoriesTableEnforcesTheUniquenessTheHandlersRelyOn(t *testing.T) {
	s := sheetsTestServer(t)

	var nullable, columnType string
	if err := s.db.QueryRow(`
		SELECT is_nullable, column_type
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = 'comida_categories' AND column_name = 'food_type'
	`).Scan(&nullable, &columnType); err != nil {
		t.Fatal(err)
	}
	// MySQL treats NULLs as distinct inside a UNIQUE index, so a nullable food_type
	// would let duplicate global categories through.
	if nullable != "NO" {
		t.Fatalf("food_type is nullable (%s), so the global sentinel would not be unique", nullable)
	}

	var nameLen, slugLen int
	if err := s.db.QueryRow(`
		SELECT
			MAX(CASE WHEN column_name = 'name' THEN character_maximum_length END),
			MAX(CASE WHEN column_name = 'slug' THEN character_maximum_length END)
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = 'comida_categories'
	`).Scan(&nameLen, &slugLen); err != nil {
		t.Fatal(err)
	}
	if nameLen != comidaCategoryNameMaxLen {
		t.Fatalf("name is VARCHAR(%d) but the handler validates against %d", nameLen, comidaCategoryNameMaxLen)
	}
	if slugLen != comidaCategorySlugMaxLen {
		t.Fatalf("slug is VARCHAR(%d) but the handler assumes %d", slugLen, comidaCategorySlugMaxLen)
	}

	// The index is what stops a second identical category racing in behind the
	// handler's own check.
	if _, err := s.db.Exec(`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (1,'','Global','global-x',1)`); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.Exec(`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (1,'','Otro','global-x',1)`)
	if !isDuplicateKeyErr(err) {
		t.Fatalf("a duplicate slug in the same scope was accepted: %v", err)
	}
	// The same slug under a different food type is a different category.
	if _, err := s.db.Exec(`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (1,'vinos','Global','global-x',1)`); err != nil {
		t.Fatalf("the unique key is not scoped by food_type: %v", err)
	}
	// And so is the same slug under another tenant.
	if _, err := s.db.Exec(`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (2,'','Global','global-x',1)`); err != nil {
		t.Fatalf("the unique key is not scoped by restaurant_id: %v", err)
	}
}

func TestComidaCategoryLegacyTablesExist(t *testing.T) {
	s := sheetsTestServer(t)
	// The table name is interpolated into the queries, so an entry here without a
	// real table would be a runtime error on a path the unique index cannot catch.
	for foodType, table := range comidaCategoryLegacyTables {
		var n int
		if err := s.db.QueryRow(`
			SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?
		`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("legacy table %q for %q does not exist", table, foodType)
		}
		if strings.ContainsAny(table, " `;'\"()") {
			t.Fatalf("legacy table name %q contains characters that could break the query", table)
		}
	}
}
