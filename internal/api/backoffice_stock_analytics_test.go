package api

import (
	"encoding/json"
	"testing"
)

func TestStockCostMetrics(t *testing.T) {
	net, pct, margin := stockCostMetrics(12.10, 10, 3)
	if net != 11 || pct < 27.27 || pct > 27.28 || margin != 8 {
		t.Fatalf("got net=%v pct=%v margin=%v", net, pct, margin)
	}
}

// Boundaries come from the owner-approved food-cost standard: RED above 40%,
// AMBER 35-40%, GREEN 25-35%, PURPLE below 25%. Each boundary value is pinned
// on both sides, because an off-by-one on a band edge silently moves dishes
// between zones and misreports menu economics.
func TestStockDefaultMarginZoneBoundaries(t *testing.T) {
	cases := []struct {
		pct  float64
		zone string
	}{
		{0, "PURPLE"},
		{24.99, "PURPLE"},
		{25, "GREEN"},
		{30, "GREEN"},
		{34.99, "GREEN"},
		{35, "AMBER"},
		{37.5, "AMBER"},
		{40, "RED"},
		{40.01, "RED"},
		{45, "RED"},
		{100, "RED"},
	}
	for _, tc := range cases {
		if got := stockDefaultMarginZone(tc.pct); got != tc.zone {
			t.Errorf("stockDefaultMarginZone(%v) = %s, want %s", tc.pct, got, tc.zone)
		}
	}
}

func TestStockCustomMarginZone(t *testing.T) {
	bands := []stockMarginBand{{Zone: "GREEN", Min: floatPtr(20), Max: floatPtr(30), IsActive: true}, {Zone: "RED", Min: floatPtr(30), IsActive: true}}
	if got := stockMarginZone(25, bands); got != "GREEN" {
		t.Fatalf("got %s", got)
	}
	if got := stockMarginZone(35, bands); got != "RED" {
		t.Fatalf("got %s", got)
	}
}

func floatPtr(value float64) *float64 { return &value }

func TestStockRecipeIngredientCostNested(t *testing.T) {
	subID := int64(2)
	recipes := map[int64]stockBOMRecipe{
		1: {OutputQty: 4, Components: []stockBOMComponent{{ItemID: 20, SubRecipeID: &subID, QtyBase: 200}}},
		2: {OutputQty: 1000, Components: []stockBOMComponent{{ItemID: 10, QtyBase: 500}}},
	}
	cost, err := stockRecipeIngredientCost(1, recipes, map[int64]float64{10: 0.002})
	if err != nil || cost != 0.05 {
		t.Fatalf("got %v, %v; want 0.05", cost, err)
	}
}

func TestProtectedDishCannotBeRemoved(t *testing.T) {
	result := map[string]any{"recommendations": []any{map[string]any{"type": "REMOVE_DISH", "item": "Paella firma", "suggestedAction": "Remove"}}}
	protectStockSignatureDishRecommendations(result, []map[string]any{{"name": "Paella firma", "isProtected": true}})
	recommendation := result["recommendations"].([]any)[0].(map[string]any)
	if recommendation["type"] != "MONITOR" {
		t.Fatalf("got %#v", recommendation)
	}
}

func TestMiniMaxDocumentContentImage(t *testing.T) {
	content := minimaxDocumentContent("image/png", []byte("png"), "Extract invoice")
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[0]["type"] != "image" || blocks[1]["type"] != "text" {
		t.Fatalf("unexpected blocks: %#v", blocks)
	}
	source := blocks[0]["source"].(map[string]any)
	if source["media_type"] != "image/png" || source["data"] != "cG5n" {
		t.Fatalf("unexpected source: %#v", source)
	}
}

func TestMiniMaxDocumentContentPDF(t *testing.T) {
	content := minimaxDocumentContent("application/pdf", []byte("pdf"), "Extract recipe")
	raw, _ := json.Marshal(content)
	var blocks []map[string]any
	_ = json.Unmarshal(raw, &blocks)
	if blocks[0]["type"] != "document" {
		t.Fatalf("unexpected blocks: %#v", blocks)
	}
}
