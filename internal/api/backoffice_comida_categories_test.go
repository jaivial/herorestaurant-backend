package api

import (
	"os"
	"strings"
	"testing"
)

func ptrString(s string) *string { return &s }
func ptrBool(b bool) *bool       { return &b }

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

func TestComidaCategoryGlobalSentinelIsEmptyStringNotNull(t *testing.T) {
	// MySQL considers NULLs distinct inside a UNIQUE index, so a nullable
	// food_type would let duplicate global categories through.
	if comidaCategoryGlobalType != "" {
		t.Fatalf("global sentinel is %q, want the empty string", comidaCategoryGlobalType)
	}
}

func TestResolveComidaCategoryScopeNeverReturnsTheScopeTheCallerRuledOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   comidaUnifiedCategoryWriteRequest
		want  string
		valid bool
	}{
		{
			name:  "no scope at all means global",
			req:   comidaUnifiedCategoryWriteRequest{},
			want:  comidaCategoryGlobalType,
			valid: true,
		},
		{
			name:  "foodType alone",
			req:   comidaUnifiedCategoryWriteRequest{FoodType: ptrString("cafes")},
			want:  "cafes",
			valid: true,
		},
		{
			name:  "global true wins over a contradictory foodType",
			req:   comidaUnifiedCategoryWriteRequest{FoodType: ptrString("cafes"), Global: ptrBool(true)},
			want:  comidaCategoryGlobalType,
			valid: true,
		},
		{
			name:  "global false with a real foodType",
			req:   comidaUnifiedCategoryWriteRequest{FoodType: ptrString("vinos"), Global: ptrBool(false)},
			want:  "vinos",
			valid: true,
		},
		{
			// The regression this guards: an empty foodType normalises to the
			// global sentinel, so "not global" used to produce a global category.
			name:  "global false with an empty foodType is rejected",
			req:   comidaUnifiedCategoryWriteRequest{FoodType: ptrString(""), Global: ptrBool(false)},
			valid: false,
		},
		{
			name:  "global false with an explicit global foodType is rejected",
			req:   comidaUnifiedCategoryWriteRequest{FoodType: ptrString("global"), Global: ptrBool(false)},
			valid: false,
		},
		{
			name:  "global false with no foodType is rejected",
			req:   comidaUnifiedCategoryWriteRequest{Global: ptrBool(false)},
			valid: false,
		},
		{
			name:  "unknown foodType is rejected",
			req:   comidaUnifiedCategoryWriteRequest{FoodType: ptrString("entrantes")},
			valid: false,
		},
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

func TestComidaCategoryUsageIsConstrainedToTheScopesTheCategoryApplim(t *testing.T) {
	// A cafes category must not be reported as in use by a plato that happens to
	// share its name, or the seeded base plato categories would make several
	// names permanently undeletable.
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
		// POSTRES has no category column at all, so a postres category is never
		// in use and must not query a column that does not exist.
		{"postres", nil, false},
	} {
		got := comidaCategoryItemSourceTypes(tc.foodType)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
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
	if name != "Café con leche" {
		t.Fatalf("name=%q want trimmed original", name)
	}
	if slug != "cafe-con-leche" {
		t.Fatalf("slug=%q want %q", slug, "cafe-con-leche")
	}

	if _, _, err := validateComidaCategoryName("   "); err == nil {
		t.Fatal("an empty name must be rejected")
	}

	// VARCHAR(120) under strict mode raises a 1406, which would surface as a 500.
	long := strings.Repeat("á", comidaCategoryNameMaxLen+1)
	if _, _, err := validateComidaCategoryName(long); err == nil {
		t.Fatal("an over-long name must be rejected before it reaches MySQL")
	}
	// Counted in runes, not bytes: 120 accented characters still fit.
	if _, _, err := validateComidaCategoryName(strings.Repeat("á", comidaCategoryNameMaxLen)); err != nil {
		t.Fatalf("a %d-rune name must be accepted: %v", comidaCategoryNameMaxLen, err)
	}
}

func TestComidaCategoryKeysDistinguishCollidingIdsAcrossTables(t *testing.T) {
	// Legacy and unified ids are independent AUTO_INCREMENT sequences, so id 7 can
	// mean two different categories. The key is what a client uses to tell them
	// apart.
	unified := comidaCategoryKey(comidaCategoryOriginUnified, "platos", 7)
	legacy := comidaCategoryKey(comidaCategoryOriginLegacy, "platos", 7)
	if unified == legacy {
		t.Fatalf("unified and legacy id 7 produced the same key %q", unified)
	}
	if !strings.HasPrefix(unified, comidaCategoryOriginUnified+":") {
		t.Fatalf("unified key %q does not name its origin", unified)
	}
	if !strings.HasPrefix(legacy, comidaCategoryOriginLegacy+":") {
		t.Fatalf("legacy key %q does not name its origin", legacy)
	}
	// A global unified row and a type-scoped one are different rows with
	// different ids, so their keys differ by id alone.
	if comidaCategoryKey(comidaCategoryOriginUnified, comidaCategoryGlobalType, 7) != unified {
		t.Fatal("the unified key must not depend on the scope, only on the id")
	}
}

func TestDedupeComidaCategoriesPrefersWhatTheClientCanActuallyManage(t *testing.T) {
	entry := func(name, origin, foodType string, id int) comidaUnifiedCategoryResponse {
		return comidaUnifiedCategoryResponse{
			ID:       id,
			Key:      comidaCategoryKey(origin, foodType, id),
			Name:     name,
			Slug:     slugifyCategoryName(name),
			FoodType: foodType,
			Scope:    comidaCategoryScope(foodType),
			IsGlobal: foodType == comidaCategoryGlobalType,
			Origin:   origin,
			Editable: origin == comidaCategoryOriginUnified,
			Active:   true,
		}
	}

	in := []comidaUnifiedCategoryResponse{
		entry("Zumos", comidaCategoryOriginLegacy, "bebidas", 3),
		entry("Entrantes", comidaCategoryOriginLegacy, "platos", 1),
		entry("Entrantes", comidaCategoryOriginUnified, comidaCategoryGlobalType, 9),
		entry("Entrantes", comidaCategoryOriginUnified, "platos", 4),
	}

	out := dedupeComidaCategories(in)
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2 after collapsing the three Entrantes", len(out))
	}
	// Sorted by name, so Entrantes comes first.
	if out[0].Name != "Entrantes" || out[1].Name != "Zumos" {
		t.Fatalf("result is not sorted by name: %q, %q", out[0].Name, out[1].Name)
	}
	winner := out[0]
	if !winner.Editable {
		t.Fatal("an editable duplicate must win over a legacy one")
	}
	if winner.IsGlobal {
		t.Fatal("the type-scoped duplicate is more specific and must win over the global one")
	}
	if winner.ID != 4 {
		t.Fatalf("winner id=%d want 4 (the type-scoped unified row)", winner.ID)
	}
}

func TestDedupeComidaCategoriesKeepsALegacyEntryWhenItIsTheOnlyOne(t *testing.T) {
	only := comidaUnifiedCategoryResponse{
		Name:     "Arroz",
		Slug:     "arroz",
		FoodType: "platos",
		Origin:   comidaCategoryOriginLegacy,
	}
	out := dedupeComidaCategories([]comidaUnifiedCategoryResponse{only})
	if len(out) != 1 || out[0].Name != "Arroz" {
		t.Fatalf("a lone legacy entry must survive dedup, got %+v", out)
	}
}

func TestComidaCategoryLegacyTablesCoverOnlyThePreExistingTypes(t *testing.T) {
	// Only platos and bebidas ever had a categories table. Adding another entry
	// here without an actual table would make the list endpoint query a table
	// that does not exist, and the name is interpolated into the SQL.
	if len(comidaCategoryLegacyTables) != 2 {
		t.Fatalf("got %d legacy tables, want 2", len(comidaCategoryLegacyTables))
	}
	for foodType, table := range map[string]string{
		"platos":  "comida_plato_categories",
		"bebidas": "comida_bebida_categories",
	} {
		if comidaCategoryLegacyTables[foodType] != table {
			t.Fatalf("legacy table for %q is %q, want %q", foodType, comidaCategoryLegacyTables[foodType], table)
		}
	}
	for _, table := range comidaCategoryLegacyTables {
		if strings.ContainsAny(table, " `;'\"()") {
			t.Fatalf("legacy table name %q contains characters that could break the query", table)
		}
	}
}

func TestComidaCategoriesMigrationDeclaresTheUniquenessGuarantees(t *testing.T) {
	raw, err := os.ReadFile("../db/migrations/095_comida_categories.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `comida_categories`", // idempotent, no down-migration exists
		"`food_type` VARCHAR(16) NOT NULL DEFAULT ''",    // sentinel, never NULL
		"uniq_comida_categories_restaurant_type_slug",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration is missing %q", want)
		}
	}
	if !strings.Contains(sql, "`restaurant_id`, `food_type`, `slug`") {
		t.Fatal("the unique key must span restaurant, food type and slug")
	}
}
