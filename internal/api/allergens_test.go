package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCanonicalAllergensAreTheFourteenEURegulatedOnes(t *testing.T) {
	if len(canonicalAllergens) != 14 {
		t.Fatalf("got %d allergens, want the 14 EU FIC Annex II allergens", len(canonicalAllergens))
	}
	seen := map[string]bool{}
	for _, key := range canonicalAllergens {
		if seen[key] {
			t.Fatalf("duplicate allergen %q", key)
		}
		seen[key] = true
	}
}

func TestNormalizeAllergenAcceptsAliasesAndCasing(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Gluten", "Gluten"},
		{"gluten", "Gluten"},
		{"  GLUTEN  ", "Gluten"},
		{"Crustáceos", "Crustaceos"}, // accented input
		{"crustaceos", "Crustaceos"},
		{"Frutos de cáscara", "Frutos de cascara"},
		{"frutos secos", "Frutos de cascara"}, // common colloquial alias
		{"Sésamo", "Sesamo"},
		{"leche", "Leche"},
		{"", ""},
		{"unicornio", ""}, // unknown must not silently become a real allergen
	} {
		if got := normalizeAllergen(tc.in); got != tc.want {
			t.Fatalf("normalizeAllergen(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeAllergenListDedupesAndOrders(t *testing.T) {
	got := normalizeAllergenList([]string{"leche", "Gluten", "LECHE", "unicornio", "  ", "Soja"})
	want := []string{"Gluten", "Soja", "Leche"} // canonical order, deduped, unknown dropped
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// The backend and frontend allergen lists must never drift: a dish showing a
// different allergen set in the kitchen than on the menu is a safety incident.
func TestBackendAllergenListMatchesFrontend(t *testing.T) {
	raw, err := os.ReadFile("../../../backoffice/ui/widgets/allergens/allergens.ts")
	if err != nil {
		t.Skipf("frontend allergens.ts not readable: %v", err)
	}
	re := regexp.MustCompile(`\{\s*key:\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatal("could not parse any allergen keys from allergens.ts")
	}
	frontend := make([]string, 0, len(matches))
	for _, m := range matches {
		frontend = append(frontend, m[1])
	}
	if len(frontend) != len(canonicalAllergens) {
		t.Fatalf("frontend has %d allergens, backend has %d: %v vs %v",
			len(frontend), len(canonicalAllergens), frontend, canonicalAllergens)
	}
	for i := range frontend {
		if frontend[i] != canonicalAllergens[i] {
			t.Fatalf("allergen %d differs: frontend %q backend %q", i, frontend[i], canonicalAllergens[i])
		}
	}
}

func TestResolveSheetAllergensNeverDropsADerivedAllergen(t *testing.T) {
	derived := []string{"Gluten", "Leche"}
	// A user tries to disable a derived allergen. Food-safety: must be ignored.
	manual := manualAllergens{Added: []string{"Soja"}, Disabled: []string{"Gluten", "Leche"}}
	got := resolveSheetAllergens(derived, manual)
	joined := strings.Join(got, ",")
	for _, must := range []string{"Gluten", "Leche", "Soja"} {
		if !strings.Contains(joined, must) {
			t.Fatalf("resolved %v must contain %q", got, must)
		}
	}
}

func TestResolveSheetAllergensAppliesManualLayer(t *testing.T) {
	// Manual add appears; manual disable of a NON-derived allergen is honoured.
	got := resolveSheetAllergens(
		[]string{"Gluten"},
		manualAllergens{Added: []string{"Soja", "Apio"}, Disabled: []string{"Apio"}},
	)
	want := map[string]bool{"Gluten": true, "Soja": true}
	if len(got) != 2 {
		t.Fatalf("got %v want exactly Gluten+Soja", got)
	}
	for _, key := range got {
		if !want[key] {
			t.Fatalf("unexpected allergen %q in %v", key, got)
		}
	}
}

func TestResolveSheetAllergensNormalizesManualInput(t *testing.T) {
	got := resolveSheetAllergens(nil, manualAllergens{Added: []string{"  crustáceos ", "unicornio"}})
	if len(got) != 1 || got[0] != "Crustaceos" {
		t.Fatalf("got %v want [Crustaceos]", got)
	}
}

func TestResolveSheetAllergensIsDeterministic(t *testing.T) {
	a := resolveSheetAllergens([]string{"Leche", "Gluten"}, manualAllergens{Added: []string{"Soja"}})
	b := resolveSheetAllergens([]string{"Gluten", "Leche"}, manualAllergens{Added: []string{"Soja"}})
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("order not deterministic: %v vs %v", a, b)
	}
}

// --- recursive derivation over the component tree ---

func TestDeriveAllergensWalksNestedSubRecipes(t *testing.T) {
	tree := map[int64][]allergenTreeNode{
		1: {{ItemID: 10, ItemName: "Harina"}, {ItemID: 11, SubRecipeID: ptrInt64(2), ItemName: "Bechamel"}},
		2: {{ItemID: 12, ItemName: "Leche entera"}},
	}
	items := map[int64][]string{
		10: {"Gluten"},
		12: {"Leche"},
	}
	derived, contributors, err := deriveAllergensFromTree(1, tree, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(derived) != 2 || derived[0] != "Gluten" || derived[1] != "Leche" {
		t.Fatalf("got %v want [Gluten Leche]", derived)
	}
	// Every derived allergen must name the ingredient that caused it, so the UI
	// can explain a lock the user cannot override.
	if got := contributors["Leche"]; len(got) != 1 || got[0] != "Leche entera" {
		t.Fatalf("contributors[Leche]=%v want [Leche entera]", got)
	}
	if got := contributors["Gluten"]; len(got) != 1 || got[0] != "Harina" {
		t.Fatalf("contributors[Gluten]=%v want [Harina]", got)
	}
}

func TestDeriveAllergensStopsOnCycle(t *testing.T) {
	// A cycle is reachable through the API, and an unguarded walk would hang a
	// request thread rather than fail.
	tree := map[int64][]allergenTreeNode{
		1: {{ItemID: 10, SubRecipeID: ptrInt64(2), ItemName: "A"}},
		2: {{ItemID: 11, SubRecipeID: ptrInt64(1), ItemName: "B"}},
	}
	if _, _, err := deriveAllergensFromTree(1, tree, map[int64][]string{}); err == nil {
		t.Fatal("expected a cycle error")
	}
}

func TestDeriveAllergensEnforcesDepthCap(t *testing.T) {
	tree := map[int64][]allergenTreeNode{}
	for i := int64(1); i <= 20; i++ {
		next := i + 1
		tree[i] = []allergenTreeNode{{ItemID: 100 + i, SubRecipeID: &next, ItemName: "n"}}
	}
	if _, _, err := deriveAllergensFromTree(1, tree, map[int64][]string{}); err == nil {
		t.Fatal("expected a depth-cap error")
	}
}

func TestDeriveAllergensDedupesContributors(t *testing.T) {
	tree := map[int64][]allergenTreeNode{
		1: {{ItemID: 10, ItemName: "Harina"}, {ItemID: 10, ItemName: "Harina"}},
	}
	_, contributors, err := deriveAllergensFromTree(1, tree, map[int64][]string{10: {"Gluten"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := contributors["Gluten"]; len(got) != 1 {
		t.Fatalf("contributors[Gluten]=%v want one entry", got)
	}
}

func TestDeriveAllergensIgnoresUnknownItemAllergens(t *testing.T) {
	tree := map[int64][]allergenTreeNode{1: {{ItemID: 10, ItemName: "Cosa"}}}
	derived, _, err := deriveAllergensFromTree(1, tree, map[int64][]string{10: {"unicornio", "  "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(derived) != 0 {
		t.Fatalf("got %v want none", derived)
	}
}

func ptrInt64(v int64) *int64 { return &v }
