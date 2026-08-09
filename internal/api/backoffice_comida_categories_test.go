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

// A refused create must leave the existing category exactly as it was, which for a
// deactivated one means staying off: an operator who switched a category off does not
// expect someone else's failed create to switch it back on.
//
// What guarantees this is comidaCategorySlugTaken counting inactive rows, so the
// refusal happens before the INSERT. The INSERT's own duplicate handling is not
// reachable from here; the only path that reaches it is the concurrent create tracked
// in the cross-scope race issue.
func TestARefusedCreateDoesNotDisturbTheCategoryItCollidedWith(t *testing.T) {
	s := sheetsTestServer(t)
	existing := createCategory(t, s, 1, `{"name":"Vermuts","foodType":"vinos"}`)
	if code, body := patchCategory(t, s, 1, existing.ID, `{"active":false}`); code != http.StatusOK {
		t.Fatalf("deactivate: status=%d body=%s", code, body)
	}

	// Same slug, different spelling: an upsert would have rewritten the name.
	if code, body := createCategoryStatus(t, s, 1, `{"name":"VERMUTS","foodType":"vinos"}`); code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", code, body)
	}

	var name string
	var active int
	var rows int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM comida_categories WHERE restaurant_id=1 AND slug='vermuts'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows carry the slug, want 1", rows)
	}
	if err := s.db.QueryRow(
		`SELECT name, active FROM comida_categories WHERE restaurant_id=1 AND id=?`, existing.ID).Scan(&name, &active); err != nil {
		t.Fatal(err)
	}
	if name != "Vermuts" {
		t.Fatalf("the refused create renamed the existing category to %q", name)
	}
	if active != 0 {
		t.Fatal("the refused create switched a deactivated category back on")
	}
}

func TestCreateRejectsANameTheLegacyTableAlreadyOwns(t *testing.T) {
	s := sheetsTestServer(t)
	// A custom row, not a seeded one: the seeds are covered separately and are
	// refused even before they exist.
	if _, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, 'De la huerta', 'de-la-huerta', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}

	if code, body := createCategoryStatus(t, s, 1, `{"name":"De la huerta","foodType":"platos"}`); code != http.StatusConflict {
		t.Fatalf("a name the legacy table owns: status=%d body=%s", code, body)
	}
	// A global category is offered next to the platos ones, so it collides too.
	if code, body := createCategoryStatus(t, s, 1, `{"name":"De la huerta","global":true}`); code != http.StatusConflict {
		t.Fatalf("a global colliding with a legacy platos row: status=%d body=%s", code, body)
	}
	// Cafes has no legacy table and does not read the platos one.
	createCategory(t, s, 1, `{"name":"De la huerta","foodType":"cafes"}`)
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

// The unique index only spans one scope, so this collision is caught by the
// handler's own overlap check and by nothing else.
func TestRenamingRejectsANameHeldInAnOverlappingScope(t *testing.T) {
	s := sheetsTestServer(t)
	createCategory(t, s, 1, `{"name":"Temporada","global":true}`)
	espumosos := createCategory(t, s, 1, `{"name":"Espumosos","foodType":"vinos"}`)

	// A vinos category renamed onto a global name: both would show in the vinos
	// picker, and renaming either afterwards would rewrite the other's products.
	if code, body := patchCategory(t, s, 1, espumosos.ID, `{"name":"Temporada"}`); code != http.StatusConflict {
		t.Fatalf("renaming onto a global name: status=%d body=%s", code, body)
	}

	// And the same collision in the other direction.
	global, ok := categoryNamed(listCategories(t, s, 1, ""), "Temporada")
	if !ok {
		t.Fatal("the global category is missing from the management listing")
	}
	if code, body := patchCategory(t, s, 1, global.ID, `{"name":"Espumosos"}`); code != http.StatusConflict {
		t.Fatalf("renaming a global onto a type-scoped name: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=vinos"), "Espumosos"); !ok {
		t.Fatal("the type-scoped category lost its name to a rejected rename")
	}
}

// Re-scoping is what turns a harmless pair of same-named categories into an
// ambiguous one, so it is checked on the destination scope, not the current one.
func TestRescopingRejectsANameHeldInTheDestinationScope(t *testing.T) {
	s := sheetsTestServer(t)
	createCategory(t, s, 1, `{"name":"Especiales","foodType":"vinos"}`)
	cafes := createCategory(t, s, 1, `{"name":"Especiales","foodType":"cafes"}`)

	if code, body := patchCategory(t, s, 1, cafes.ID, `{"foodType":"vinos"}`); code != http.StatusConflict {
		t.Fatalf("moving onto an occupied name: status=%d body=%s", code, body)
	}
	if _, ok := categoryNamed(listCategories(t, s, 1, "?foodType=cafes"), "Especiales"); !ok {
		t.Fatal("the category left its scope on a rejected move")
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

// The base names are seeded lazily, on the first listing, so on a fresh restaurant
// they are absent from the table and a lookup would report them free. Taking one
// would hand the operator a unified category that the next listing seeds a legacy
// row alongside, and renaming it afterwards would hijack the seed.
func TestCreateRejectsASeededBaseNameBeforeItHasBeenSeeded(t *testing.T) {
	s := sheetsTestServer(t)

	var seeded int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM comida_plato_categories WHERE restaurant_id=1`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != 0 {
		t.Fatalf("the base categories are already seeded (%d rows); this test needs a fresh tenant", seeded)
	}

	for _, base := range basePlatoCategories {
		if code, body := createCategoryStatus(t, s, 1, `{"name":"`+base.Name+`","foodType":"platos"}`); code != http.StatusConflict {
			t.Fatalf("base plato name %q: status=%d body=%s", base.Name, code, body)
		}
	}
	for _, base := range baseBebidaCategories {
		if code, body := createCategoryStatus(t, s, 1, `{"name":"`+base.Name+`","foodType":"bebidas"}`); code != http.StatusConflict {
			t.Fatalf("base bebida name %q: status=%d body=%s", base.Name, code, body)
		}
	}
	// A global category collides with every type, so the base names are out too.
	if code, body := createCategoryStatus(t, s, 1, `{"name":"`+basePlatoCategories[0].Name+`","global":true}`); code != http.StatusConflict {
		t.Fatalf("a global taking a base plato name: status=%d body=%s", code, body)
	}
	// A type with no legacy table of its own is unaffected.
	createCategory(t, s, 1, `{"name":"`+basePlatoCategories[0].Name+`","foodType":"vinos"}`)
}

// The seeded rows belong to the old carta screen, and ensureBase*Categories puts them
// back on the next listing. Renaming one would let the seed return and leave the
// renamed row behind as a second, uneditable entry; deleting one would take a base
// category out of the carta.
//
// Creating such a category is refused now, so the row is inserted the way one that
// predates that check would look.
func TestWritesNeverTouchASeededBaseCategory(t *testing.T) {
	s := sheetsTestServer(t)
	// Reading the listing seeds the base rows with source 'base'.
	listCategories(t, s, 1, "?foodType=platos")
	seed := basePlatoCategories[0]

	res, err := s.db.Exec(
		`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (1, 'platos', ?, ?, 1)`,
		seed.Name, seed.Slug)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	if code, body := patchCategory(t, s, 1, int(id), `{"name":"Renombrada"}`); code != http.StatusOK {
		t.Fatalf("rename: status=%d body=%s", code, body)
	}
	var name string
	var source string
	if err := s.db.QueryRow(
		`SELECT name, COALESCE(source,'custom') FROM comida_plato_categories WHERE restaurant_id=1 AND slug=?`, seed.Slug).Scan(&name, &source); err != nil {
		t.Fatalf("the seeded row is gone: %v", err)
	}
	if name != seed.Name || source != "base" {
		t.Fatalf("the rename hijacked the seeded row: name=%q source=%q", name, source)
	}

	if code, body := deleteCategory(t, s, 1, int(id)); code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", code, body)
	}
	var survivors int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM comida_plato_categories WHERE restaurant_id=1 AND slug=? AND COALESCE(source,'custom')='base'`, seed.Slug).Scan(&survivors); err != nil {
		t.Fatal(err)
	}
	if survivors != 1 {
		t.Fatalf("deleting a category took the seeded %q with it", seed.Name)
	}
}

// The base names are reserved, but a category that already carries one predates the
// reservation: it exists in restaurants running the version that let it through.
// Reserving its own name against it would freeze the row, answering 409 to every
// reactivation and every move for good.
func TestACategoryAlreadyHoldingABaseNameIsNotFrozenByTheReservation(t *testing.T) {
	s := sheetsTestServer(t)
	seed := basePlatoCategories[0]

	// Inserted in platos, the scope the reservation covers, and the way a row created
	// before the reservation existed would look.
	res, err := s.db.Exec(
		`INSERT INTO comida_categories (restaurant_id, food_type, name, slug, active) VALUES (1, 'platos', ?, ?, 0)`,
		seed.Name, seed.Slug)
	if err != nil {
		t.Fatal(err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	id := int(id64)

	if code, body := patchCategory(t, s, 1, id, `{"active":true}`); code != http.StatusOK {
		t.Fatalf("reactivating a category that holds a base name: status=%d body=%s", code, body)
	}
	if code, body := patchCategory(t, s, 1, id, `{"global":true}`); code != http.StatusOK {
		t.Fatalf("moving a category that holds a base name: status=%d body=%s", code, body)
	}

	// Renaming away from the base name is allowed, and afterwards the name is
	// reserved again: the exemption is only for the row that already carries it.
	if code, body := patchCategory(t, s, 1, id, `{"name":"Otra","foodType":"platos"}`); code != http.StatusOK {
		t.Fatalf("renaming away from a base name: status=%d body=%s", code, body)
	}
	if code, body := patchCategory(t, s, 1, id, `{"name":"`+seed.Name+`"}`); code != http.StatusConflict {
		t.Fatalf("renaming back onto a base name: status=%d body=%s", code, body)
	}
}

// A deactivated category keeps its row, so nothing in this catalogue can take its
// name while it is off. The old carta screen can, though, writing straight to the
// legacy table — and turning the category back on would then put two entries with
// the same name in the same picker.
func TestReactivatingRevalidatesTheNameAgainstTheLegacyTable(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Sugerencias","foodType":"platos"}`)
	if code, body := patchCategory(t, s, 1, cat.ID, `{"active":false}`); code != http.StatusOK {
		t.Fatalf("deactivate: status=%d body=%s", code, body)
	}

	// The old carta screen claims the name while the category is switched off.
	if _, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Sugerencias', 'sugerencias', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}

	if code, body := patchCategory(t, s, 1, cat.ID, `{"active":true}`); code != http.StatusConflict {
		t.Fatalf("reactivating onto a name the legacy table took: status=%d body=%s", code, body)
	}
	got := listCategories(t, s, 1, "?foodType=platos")
	if c, ok := categoryNamed(got, "Sugerencias"); !ok || c.Origin != comidaCategoryOriginLegacy {
		t.Fatal("the rejected reactivation still switched the category back on")
	}
}

// A legacy row is only this category's twin if it carries the same name as well as
// the same slug and is active. Anything else belongs to the old carta screen, and
// rewriting or deleting it would be collateral damage on data this endpoint does not
// own.
//
// Each discriminator gets its own case, because a row that fails two of them at once
// proves nothing about either.
func TestWritesLeaveALegacyRowThatIsNotTheirTwinAlone(t *testing.T) {
	// The legacy row is written after the category, because an active row sharing the
	// slug is exactly what the create pre-check refuses. That ordering is the real
	// one anyway: the old carta screen is what makes a row diverge.
	for _, tc := range []struct {
		name      string
		legacyRow string
	}{
		{
			// handleComidaPlatoCategoriesCreate takes a client-supplied slug that
			// need not match its own name, so the carta can land on this slug.
			name:      "the name diverged",
			legacyRow: `('De la casa', 'fuera-de-carta', 'custom', 1)`,
		},
		{
			name:      "the row is inactive",
			legacyRow: `('Fuera de carta', 'fuera-de-carta', 'custom', 0)`,
		},
	} {
		// setup leaves a category and, sharing its slug, a legacy row that is not its
		// twin. Each write gets a fresh one: a rename moves the category off the
		// shared slug, after which a delete would not even look at the row.
		setup := func(t *testing.T) (*Server, comidaUnifiedCategoryResponse) {
			t.Helper()
			s := sheetsTestServer(t)
			cat := createCategory(t, s, 1, `{"name":"Fuera de carta","foodType":"platos"}`)
			if _, err := s.db.Exec(
				`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, ` + tc.legacyRow[1:],
			); err != nil {
				t.Fatal(err)
			}
			return s, cat
		}
		// assertUntouched re-reads the legacy row and fails if anything moved.
		assertUntouched := func(t *testing.T, s *Server) {
			t.Helper()
			var name, source string
			var active int
			if err := s.db.QueryRow(
				`SELECT name, active, COALESCE(source,'custom') FROM comida_plato_categories WHERE restaurant_id=1 AND slug='fuera-de-carta'`,
			).Scan(&name, &active, &source); err != nil {
				t.Fatalf("the unrelated legacy row is gone: %v", err)
			}
			if got := `('` + name + `', 'fuera-de-carta', '` + source + `', ` + strconv.Itoa(active) + `)`; got != tc.legacyRow {
				t.Fatalf("the unrelated legacy row was rewritten: %s, want %s", got, tc.legacyRow)
			}
		}

		t.Run(tc.name+"/rename", func(t *testing.T) {
			s, cat := setup(t)
			if code, body := patchCategory(t, s, 1, cat.ID, `{"name":"En carta"}`); code != http.StatusOK {
				t.Fatalf("rename: status=%d body=%s", code, body)
			}
			assertUntouched(t, s)
		})

		t.Run(tc.name+"/delete", func(t *testing.T) {
			s, cat := setup(t)
			if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusOK {
				t.Fatalf("delete: status=%d body=%s", code, body)
			}
			assertUntouched(t, s)
		})
	}
}

// stock_margin_scopes.scope_key names the legacy category as plain text, with no
// foreign key, so deleting the twin leaves a target nothing will ever clean up — and
// ids are reused, so the next category to land on that id inherits it.
func TestDeletingACategoryTakesItsMarginScopeWithIt(t *testing.T) {
	s := sheetsTestServer(t)
	cat := createCategory(t, s, 1, `{"name":"Guisos","foodType":"platos"}`)

	// Saving a product against the category is what materialises the legacy twin.
	if _, err := s.db.Exec(
		`INSERT INTO comida_plato_categories (restaurant_id, name, slug, source, active) VALUES (1, 'Guisos', 'guisos', 'custom', 1)`,
	); err != nil {
		t.Fatal(err)
	}
	var twinID int64
	if err := s.db.QueryRow(
		`SELECT id FROM comida_plato_categories WHERE restaurant_id=1 AND slug='guisos'`).Scan(&twinID); err != nil {
		t.Fatal(err)
	}

	scopeKey := marginScopeKeyForCategory("platos", twinID)
	res, err := s.db.Exec(
		`INSERT INTO stock_margin_scopes (restaurant_id, scope_kind, scope_key, label, target_food_cost_pct) VALUES (1, 'COMIDA_CATEGORY', ?, 'Guisos', 32.00)`,
		scopeKey)
	if err != nil {
		t.Fatal(err)
	}
	scopeID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO stock_margin_scope_bands (restaurant_id, scope_id, zone, min_food_cost_pct, max_food_cost_pct) VALUES (1, ?, 'GREEN', 20.00, 35.00)`,
		scopeID); err != nil {
		t.Fatal(err)
	}

	if code, body := deleteCategory(t, s, 1, cat.ID); code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", code, body)
	}

	var scopes int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM stock_margin_scopes WHERE restaurant_id=1 AND scope_key=?`, scopeKey).Scan(&scopes); err != nil {
		t.Fatal(err)
	}
	if scopes != 0 {
		t.Fatal("the margin scope outlived the category it was configured for")
	}
	// The bands cascade from the scope row.
	var bands int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM stock_margin_scope_bands WHERE restaurant_id=1 AND scope_id=?`, scopeID).Scan(&bands); err != nil {
		t.Fatal(err)
	}
	if bands != 0 {
		t.Fatalf("%d bands outlived their scope", bands)
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
