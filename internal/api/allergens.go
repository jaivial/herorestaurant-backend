package api

import (
	"errors"
	"strings"
	"unicode"
)

// The 14 allergens that EU FIC Regulation 1169/2011 Annex II requires to be
// declared. This list is mirrored in backoffice/ui/widgets/allergens/allergens.ts
// and TestBackendAllergenListMatchesFrontend pins the two together: a dish that
// shows one allergen set in the kitchen and another on the menu is a safety
// incident, not a cosmetic bug.
//
// Keys are unaccented on purpose so they survive any collation or transport.
var canonicalAllergens = []string{
	"Gluten",
	"Crustaceos",
	"Huevos",
	"Pescado",
	"Cacahuetes",
	"Soja",
	"Leche",
	"Frutos de cascara",
	"Apio",
	"Mostaza",
	"Sesamo",
	"Sulfitos",
	"Altramuces",
	"Moluscos",
}

// Aliases map what humans and suppliers actually write onto canonical keys.
// Accents are stripped before lookup, so only genuinely different wordings need
// an entry here.
var allergenAliases = map[string]string{
	"frutos secos":        "Frutos de cascara",
	"frutos de cascara":   "Frutos de cascara",
	"nueces":              "Frutos de cascara",
	"cacahuete":           "Cacahuetes",
	"mani":                "Cacahuetes",
	"lacteos":             "Leche",
	"lactosa":             "Leche",
	"huevo":               "Huevos",
	"marisco":             "Crustaceos",
	"crustaceo":           "Crustaceos",
	"molusco":             "Moluscos",
	"sulfito":             "Sulfitos",
	"dioxido de azufre":   "Sulfitos",
	"anhidrido sulfuroso": "Sulfitos",
	"ajonjoli":            "Sesamo",
	"soya":                "Soja",
	"altramuz":            "Altramuces",
	"trigo":               "Gluten",
	"cereales":            "Gluten",
}

// stripAccents folds accented Latin characters onto their base letter so
// "Crustáceos" and "Crustaceos" are the same allergen.
func stripAccents(s string) string {
	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
		"é", "e", "è", "e", "ë", "e", "ê", "e",
		"í", "i", "ì", "i", "ï", "i", "î", "i",
		"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
		"ú", "u", "ù", "u", "ü", "u", "û", "u",
		"ñ", "n", "ç", "c",
	)
	return replacer.Replace(s)
}

// normalizeAllergen maps free-form input onto a canonical key, or "" when the
// value is not a recognised allergen. Returning "" rather than passing the
// input through is deliberate: an unrecognised string must never be persisted
// as if it were a declared allergen.
func normalizeAllergen(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	folded := stripAccents(strings.ToLower(trimmed))
	folded = strings.Join(strings.FieldsFunc(folded, func(r rune) bool {
		return unicode.IsSpace(r)
	}), " ")
	for _, key := range canonicalAllergens {
		if stripAccents(strings.ToLower(key)) == folded {
			return key
		}
	}
	if alias, ok := allergenAliases[folded]; ok {
		return alias
	}
	return ""
}

// normalizeAllergenList normalizes, drops unknowns, dedupes and returns the
// result in canonical order so persisted JSON and API responses are stable.
func normalizeAllergenList(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	for _, value := range raw {
		if key := normalizeAllergen(value); key != "" {
			seen[key] = true
		}
	}
	out := make([]string, 0, len(seen))
	for _, key := range canonicalAllergens {
		if seen[key] {
			out = append(out, key)
		}
	}
	return out
}

// manualAllergens is the user-editable layer stored in
// stock_recipes.manual_allergens_json.
type manualAllergens struct {
	Added    []string `json:"added"`
	Disabled []string `json:"disabled"`
}

// resolveSheetAllergens combines the derived (read-only) set with the manual
// layer:
//
//	final = derived ∪ (added \ derived) \ (disabled \ derived)
//
// A derived allergen can never be removed. The ingredient tree says the dish
// contains it, so honouring a "disable" would let the UI hide a real allergen.
// This is enforced here, in the server, not only in the interface.
func resolveSheetAllergens(derived []string, manual manualAllergens) []string {
	derivedSet := make(map[string]bool)
	for _, key := range normalizeAllergenList(derived) {
		derivedSet[key] = true
	}
	final := make(map[string]bool, len(derivedSet))
	for key := range derivedSet {
		final[key] = true
	}
	for _, key := range normalizeAllergenList(manual.Added) {
		final[key] = true
	}
	for _, key := range normalizeAllergenList(manual.Disabled) {
		if derivedSet[key] {
			continue // derived allergens are not disableable
		}
		delete(final, key)
	}
	out := make([]string, 0, len(final))
	for _, key := range canonicalAllergens {
		if final[key] {
			out = append(out, key)
		}
	}
	return out
}

// maxAllergenDepth caps sub-recipe nesting. Cycles are already rejected by the
// visited set; the cap additionally protects against a legitimately absurd
// nesting chain turning one request into a very deep walk.
const maxAllergenDepth = 12

// allergenTreeNode is one component row, reduced to what derivation needs.
type allergenTreeNode struct {
	ItemID      int64
	ItemName    string
	SubRecipeID *int64
}

// deriveAllergensFromTree walks a recipe's components, following sub-recipes,
// and returns the union of the allergens declared on every reachable stock item
// plus, for each allergen, the ingredients that contributed it.
//
// It is deliberately pure: the caller loads the tree once and this function
// stays unit-testable without a database.
func deriveAllergensFromTree(
	rootRecipeID int64,
	tree map[int64][]allergenTreeNode,
	itemAllergens map[int64][]string,
) (derived []string, contributors map[string][]string, err error) {
	found := map[string]bool{}
	contributors = map[string][]string{}
	seenContributor := map[string]bool{}
	visiting := map[int64]bool{}

	var walk func(recipeID int64, depth int) error
	walk = func(recipeID int64, depth int) error {
		if depth > maxAllergenDepth {
			return errors.New("recipe nesting too deep")
		}
		if visiting[recipeID] {
			return errors.New("recipe cycle detected")
		}
		visiting[recipeID] = true
		defer delete(visiting, recipeID)

		for _, node := range tree[recipeID] {
			for _, raw := range itemAllergens[node.ItemID] {
				key := normalizeAllergen(raw)
				if key == "" {
					continue
				}
				found[key] = true
				if name := strings.TrimSpace(node.ItemName); name != "" {
					dedupe := key + "\x00" + name
					if !seenContributor[dedupe] {
						seenContributor[dedupe] = true
						contributors[key] = append(contributors[key], name)
					}
				}
			}
			if node.SubRecipeID != nil {
				if err := walk(*node.SubRecipeID, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(rootRecipeID, 0); err != nil {
		return nil, nil, err
	}
	for _, key := range canonicalAllergens {
		if found[key] {
			derived = append(derived, key)
		}
	}
	return derived, contributors, nil
}
