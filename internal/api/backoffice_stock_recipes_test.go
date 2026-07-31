package api

import "testing"

func TestStockProductionRequirement(t *testing.T) {
	got, err := stockProductionRequirement(500, 4, 0)
	if err != nil || got != 2000 {
		t.Fatalf("got %v, %v; want 2000, nil", got, err)
	}
	got, err = stockProductionRequirement(100, 2, 20)
	if err != nil || got != 250 {
		t.Fatalf("got %v, %v; want 250, nil", got, err)
	}
	if _, err = stockProductionRequirement(100, 1, 100); err == nil {
		t.Fatal("accepted 100% waste")
	}
}

func TestExpandStockRecipeRequirements(t *testing.T) {
	subID := int64(2)
	recipes := map[int64]stockBOMRecipe{
		1: {OutputQty: 4, Components: []stockBOMComponent{{ItemID: 20, SubRecipeID: &subID, QtyBase: 200}}},
		2: {OutputQty: 1000, Components: []stockBOMComponent{{ItemID: 10, QtyBase: 500}, {ItemID: 11, QtyBase: 100, WastePct: 20}}},
	}
	got, err := expandStockRecipeRequirements(1, 2, recipes)
	if err != nil {
		t.Fatal(err)
	}
	if got[10] != 200 || got[11] != 50 {
		t.Fatalf("got %#v; want flour=200 sauce=50", got)
	}
}

func TestExpandStockRecipeRequirementsRejectsCycle(t *testing.T) {
	one, two := int64(1), int64(2)
	recipes := map[int64]stockBOMRecipe{
		1: {OutputQty: 1, Components: []stockBOMComponent{{ItemID: 2, SubRecipeID: &two, QtyBase: 1}}},
		2: {OutputQty: 1, Components: []stockBOMComponent{{ItemID: 1, SubRecipeID: &one, QtyBase: 1}}},
	}
	if _, err := expandStockRecipeRequirements(1, 1, recipes); err == nil {
		t.Fatal("accepted recipe cycle")
	}
}
