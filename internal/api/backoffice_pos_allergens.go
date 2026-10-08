package api

import (
	"encoding/json"
	"strings"
)

// The allergens the EU requires a restaurant to declare, plus the two the
// Spanish regulation adds in practice. The POS only ever displays these, so
// accepting a free-text string is how "does this contain nuts?" ends up
// answered with "unknown" instead of an answer.
//
// Matches the Spanish wording used by the rest of the catalogue
// (menu_dishes_catalog, stock_items), so an import does not have to translate.
var posKnownAllergens = map[string]struct{}{
	"gluten": {}, "crustaceos": {}, "huevos": {}, "pescado": {},
	"cacahuetes": {}, "soja": {}, "lacteos": {}, "frutos_secos": {},
	"apio": {}, "mostaza": {}, "sesamo": {}, "sulfitos": {},
	"altramuz": {}, "moluscos": {}, "moluscos_blasteados": {},
}

// The display spelling, so the till shows "Cacahuetes" rather than whatever
// case the catalogue happened to use.
var posAllergenLabels = map[string]string{
	"gluten": "Gluten", "crustaceos": "Crustáceos", "huevos": "Huevos",
	"pescado": "Pescado", "cacahuetes": "Cacahuetes", "soja": "Soja",
	"lacteos": "Lácteos", "frutos_secos": "Frutos secos", "apio": "Apio",
	"mostaza": "Mostaza", "sesamo": "Sésamo", "sulfitos": "Sulfitos",
	"altramuz": "Altramuz", "moluscos": "Moluscos", "moluscos_blasteados": "Moluscos blasteados",
}

// normaliseAllergenKey folds the accents and case away so "Lácteos", "lacteos"
// and "LACTEOS" are the same allergen.
func normaliseAllergenKey(value string) string {
	replacer := strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", " ", "_", "-", "_")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(value)))
}

// Normalises and validates an allergen list for storage. Returns false when an
// entry is not one of the declarable allergens: a product must never end up
// carrying an allergen nobody recognises, because the till renders the list to
// the guest as-is and "a doubt" is not a legal declaration.
//
// Duplicates collapse and order follows the regulatory list, so the same
// product does not end up with two spellings depending on who edited it.
func normalisePOSAllergens(values []string) ([]string, bool) {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		key := normaliseAllergenKey(value)
		if key == "" {
			continue
		}
		if _, ok := posKnownAllergens[key]; !ok {
			return nil, false
		}
		seen[key] = true
	}
	order := []string{"gluten", "crustaceos", "huevos", "pescado", "cacahuetes", "soja", "lacteos", "frutos_secos", "apio", "mostaza", "sesamo", "sulfitos", "altramuz", "moluscos", "moluscos_blasteados"}
	out := make([]string, 0, len(seen))
	for _, key := range order {
		if seen[key] {
			out = append(out, posAllergenLabels[key])
		}
	}
	return out, true
}

// encodePOSAllergens stores the list, or NULL when the product has none.
func encodePOSAllergens(values []string) any {
	if len(values) == 0 {
		return nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	return string(encoded)
}

// decodePOSAllergens reads a stored list. A product written before this column
// existed, or hand-edited into invalid JSON, reads as "no allergens recorded"
// rather than taking the whole product list down.
func decodePOSAllergens(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	clean, _ := normalisePOSAllergens(out)
	if clean == nil {
		return []string{}
	}
	return clean
}
