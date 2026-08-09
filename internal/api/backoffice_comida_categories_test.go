package api

import (
	"os"
	"strings"
	"testing"
)

func TestNormalizeComidaCategoryFoodTypeAcceptsEveryFoodTypeAndGlobalAliases(t *testing.T) {
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
		{"  PLATOS  ", "platos", true}, // trimmed and lowercased
		{"", comidaCategoryGlobalType, true},
		{"global", comidaCategoryGlobalType, true},
		{"all", comidaCategoryGlobalType, true},
		{"entrantes", "", false}, // a category name is not a food type
		{"menus", "", false},
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
	// MySQL treats NULLs as distinct inside a UNIQUE index, so a nullable
	// food_type would let duplicate global categories through. The sentinel must
	// stay an empty string for uniq_comida_categories_restaurant_type_slug to work.
	if comidaCategoryGlobalType != "" {
		t.Fatalf("global sentinel is %q, want the empty string", comidaCategoryGlobalType)
	}
}

func TestComidaCategoryScopeNamesGlobalRowsExplicitly(t *testing.T) {
	if got := comidaCategoryScope(comidaCategoryGlobalType); got != "global" {
		t.Fatalf("comidaCategoryScope(global sentinel)=%q want %q", got, "global")
	}
	for _, foodType := range comidaCategoryFoodTypes {
		if got := comidaCategoryScope(foodType); got != foodType {
			t.Fatalf("comidaCategoryScope(%q)=%q want %q", foodType, got, foodType)
		}
	}
}

func TestComidaCategoryLegacyTablesCoverOnlyThePreExistingTypes(t *testing.T) {
	// Only platos and bebidas ever had a categories table. Adding another entry
	// here without an actual table would make the list endpoint query a table
	// that does not exist.
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
}

func TestComidaCategoryLegacyTableNamesAreNeverTakenFromInput(t *testing.T) {
	// listLegacyComidaCategories interpolates the table name into the SQL, which
	// is only safe while every name comes from this fixed map.
	for _, table := range comidaCategoryLegacyTables {
		if strings.ContainsAny(table, " `;'\"()") {
			t.Fatalf("legacy table name %q contains characters that could break the query", table)
		}
	}
}

func TestComidaCategoriesMigrationDeclaresTheUniquenessGuarantees(t *testing.T) {
	raw, err := os.ReadFile("../db/migrations/094_comida_categories.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `comida_categories`", // idempotent, no down-migration exists
		"`food_type` VARCHAR(16) NOT NULL DEFAULT ''",    // sentinel, never NULL
		"uniq_comida_categories_restaurant_type_slug",
		"(`restaurant_id`, `food_type`, `slug`)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration is missing %q", want)
		}
	}
}

func TestComidaCategoryRoutesAreDeclaredBeforeTheTipoWildcard(t *testing.T) {
	// chi matches static segments before wildcards, but /comida/categorias and
	// /comida/{tipo} are close enough that ordering is worth pinning down: a
	// reader moving the block below the wildcard should see this fail.
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}
	routes := string(raw)
	categorias := strings.Index(routes, `Get("/comida/categorias"`)
	wildcard := strings.Index(routes, `Get("/comida/{tipo}"`)
	if categorias < 0 {
		t.Fatal("GET /comida/categorias is not registered")
	}
	if wildcard < 0 {
		t.Fatal("GET /comida/{tipo} is not registered")
	}
	if categorias > wildcard {
		t.Fatal("GET /comida/categorias must be registered before GET /comida/{tipo}")
	}
	for _, want := range []string{
		`Post("/comida/categorias"`,
		`Patch("/comida/categorias/{id}"`,
		`Delete("/comida/categorias/{id}"`,
	} {
		if !strings.Contains(routes, want) {
			t.Fatalf("server.go is missing %s", want)
		}
	}
}
