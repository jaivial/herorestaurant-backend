package api

import (
	"testing"
	"time"
)

func TestEffectiveHourlyCost(t *testing.T) {
	if got, err := effectiveHourlyCost("MONTHLY", 2000, 160, 30); err != nil || got != 16.25 {
		t.Fatalf("got %v, %v; want 16.25", got, err)
	}
	if got, err := effectiveHourlyCost("HOURLY", 12, 0, 25); err != nil || got != 15 {
		t.Fatalf("got %v, %v; want 15", got, err)
	}
}

func TestCompensationRangesOverlap(t *testing.T) {
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !compensationRangesOverlap(from, &to, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), nil) {
		t.Fatal("inclusive effective dates must overlap")
	}
}

func TestStockRecipeLabourCostNested(t *testing.T) {
	subID := int64(2)
	recipes := map[int64]stockBOMRecipe{
		1: {OutputQty: 4, Components: []stockBOMComponent{{ItemID: 20, SubRecipeID: &subID, QtyBase: 200}}},
		2: {OutputQty: 1000},
	}
	cost, missing, err := stockRecipeLabourCost(1, recipes, map[int64]float64{1: 8, 2: 20}, map[int64][]string{})
	if err != nil || cost != 3 || len(missing) != 0 {
		t.Fatalf("got cost=%v missing=%v err=%v; want 3", cost, missing, err)
	}
}

func TestStockRecipeLabourCostReportsMissingRates(t *testing.T) {
	recipes := map[int64]stockBOMRecipe{1: {OutputQty: 2}}
	cost, missing, err := stockRecipeLabourCost(1, recipes, map[int64]float64{}, map[int64][]string{1: {"Ana"}})
	if err != nil || cost != 0 || len(missing) != 1 || missing[0] != "Ana" {
		t.Fatalf("got cost=%v missing=%v err=%v", cost, missing, err)
	}
}
