package api

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestSheetCostWorkedExampleFromTheBrief(t *testing.T) {
	// 1 kg of flour at 10 EUR -> 0.01 EUR/g. A recipe using 500 g costs 5.00.
	lines := []sheetCostLine{
		{QtyBase: 500, UnitCostBase: 0.01},
	}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1})
	if math.Abs(cost.IngredientCost-5) > 1e-9 {
		t.Fatalf("ingredientCost=%v want 5.00", cost.IngredientCost)
	}
	if !cost.CostComplete {
		t.Fatal("cost should be complete when every line has a price")
	}
}

// The single most dangerous failure mode: a missing purchase price silently
// treated as zero would make a dish look infinitely profitable.
func TestSheetCostWithAMissingPriceIsNotZero(t *testing.T) {
	lines := []sheetCostLine{
		{Name: "Harina", QtyBase: 500, UnitCostBase: 0.01},
		{Name: "Azafran", QtyBase: 2, PriceMissing: true},
	}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1})
	if cost.CostComplete {
		t.Fatal("costComplete must be false when a price is missing")
	}
	if len(cost.MissingPrices) != 1 || cost.MissingPrices[0] != "Azafran" {
		t.Fatalf("missingPrices=%v want [Azafran]", cost.MissingPrices)
	}
	// The known part is still reported, so the user sees a floor, not a lie.
	if math.Abs(cost.IngredientCost-5) > 1e-9 {
		t.Fatalf("ingredientCost=%v want the known 5.00", cost.IngredientCost)
	}
}

func TestSheetCostAppliesComponentWaste(t *testing.T) {
	// 10% waste means you must buy 1/(1-0.10) of what the recipe uses.
	lines := []sheetCostLine{{QtyBase: 900, UnitCostBase: 0.01, WastePct: 10}}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1})
	want := 900 * 0.01 / 0.9
	if math.Abs(cost.IngredientCost-want) > 1e-9 {
		t.Fatalf("ingredientCost=%v want %v", cost.IngredientCost, want)
	}
}

func TestSheetCostDividesByPortions(t *testing.T) {
	lines := []sheetCostLine{{QtyBase: 1000, UnitCostBase: 0.01}}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 4})
	if math.Abs(cost.CostPerPortion-2.5) > 1e-9 {
		t.Fatalf("costPerPortion=%v want 2.50", cost.CostPerPortion)
	}
}

// Labour must stay a separate line: blending it into ingredient cost destroys
// comparability with ingredient-only food-cost benchmarks.
func TestSheetCostKeepsLabourSeparateButInTheTotal(t *testing.T) {
	lines := []sheetCostLine{{QtyBase: 1000, UnitCostBase: 0.01}}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1, LabourCost: 3, DirectVariableCost: 1})
	if math.Abs(cost.IngredientCost-10) > 1e-9 {
		t.Fatalf("ingredientCost=%v must exclude labour", cost.IngredientCost)
	}
	if math.Abs(cost.TotalCost-14) > 1e-9 {
		t.Fatalf("totalCost=%v want 14 (10 + 3 labour + 1 direct)", cost.TotalCost)
	}
}

func TestSheetCostFoodCostPctUsesNetPriceAndIngredientsOnly(t *testing.T) {
	lines := []sheetCostLine{{QtyBase: 1000, UnitCostBase: 0.01}} // 10.00
	cost := computeSheetCost(lines, sheetCostContext{
		Portions: 1, GrossPrice: 33, VatRate: 10, LabourCost: 5,
	})
	// net = 33 / 1.10 = 30.00 -> food cost 10/30 = 33.33%
	if math.Abs(cost.NetPrice-30) > 1e-9 {
		t.Fatalf("netPrice=%v want 30", cost.NetPrice)
	}
	if math.Abs(cost.FoodCostPct-33.333333333333336) > 1e-6 {
		t.Fatalf("foodCostPct=%v want ~33.33", cost.FoodCostPct)
	}
}

// An incomplete cost must not be dressed up with a confident-looking zone.
func TestSheetCostZoneIsSuppressedWhenIncomplete(t *testing.T) {
	lines := []sheetCostLine{
		{Name: "Sin precio", QtyBase: 10, PriceMissing: true},
	}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1, GrossPrice: 20, VatRate: 10})
	if cost.Zone != "" {
		t.Fatalf("zone=%q want empty while the cost is incomplete", cost.Zone)
	}
}

func TestSheetCostZoneUsesResolvedBands(t *testing.T) {
	lines := []sheetCostLine{{QtyBase: 1000, UnitCostBase: 0.01}} // 10.00
	// net 30 -> 33.33% food cost. Default bands put 25-35 in GREEN.
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1, GrossPrice: 33, VatRate: 10})
	if cost.Zone != "GREEN" {
		t.Fatalf("zone=%q want GREEN", cost.Zone)
	}
}

func TestSheetCostWithNoPriceGivesNoPercentages(t *testing.T) {
	lines := []sheetCostLine{{QtyBase: 100, UnitCostBase: 0.02}}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 1})
	if cost.FoodCostPct != 0 || cost.Zone != "" {
		t.Fatalf("without a selling price there is no food-cost %% or zone: %+v", cost)
	}
}

func TestSheetCostHandlesZeroPortionsSafely(t *testing.T) {
	lines := []sheetCostLine{{QtyBase: 100, UnitCostBase: 0.01}}
	cost := computeSheetCost(lines, sheetCostContext{Portions: 0})
	if math.IsInf(cost.CostPerPortion, 0) || math.IsNaN(cost.CostPerPortion) {
		t.Fatalf("costPerPortion=%v must stay finite", cost.CostPerPortion)
	}
}

func priceItem(t *testing.T, s *Server, itemID int64, unitCostBase float64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO stock_item_prices (restaurant_id,stock_item_id,unit_cost_base,effective_at)
		VALUES (1,?,?,NOW())`, itemID, unitCostBase); err != nil {
		t.Fatal(err)
	}
}

func sheetCostOf(t *testing.T, s *Server, sheetID int64) sheetCost {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetCost(rec, sheetReq("GET", "/x", "",
		map[string]string{"id": strconv.FormatInt(sheetID, 10)}))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Cost sheetCost `json:"cost"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Cost
}

func TestSheetCostEndpointUsesLatestPurchasePrice(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Pan")
	itemID, unitID := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")
	priceItem(t, s, itemID, 0.005) // superseded
	priceItem(t, s, itemID, 0.01)  // latest
	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(itemID, 10)+
		`,"unitId":`+strconv.FormatInt(unitID, 10)+`,"quantity":500}`)

	cost := sheetCostOf(t, s, sheetID)
	if math.Abs(cost.IngredientCost-5) > 1e-9 {
		t.Fatalf("ingredientCost=%v want 5.00 from the latest price", cost.IngredientCost)
	}
	if !cost.CostComplete {
		t.Fatalf("cost should be complete: %+v", cost.MissingPrices)
	}
}

func TestSheetCostEndpointReportsUnpricedIngredient(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Con azafran")
	priced, pricedUnit := seedIngredient(t, s, "Harina", "g", "MASS", 1, "")
	priceItem(t, s, priced, 0.01)
	unpriced, unpricedUnit := seedIngredient(t, s, "Azafran", "g", "MASS", 1, "")

	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(priced, 10)+
		`,"unitId":`+strconv.FormatInt(pricedUnit, 10)+`,"quantity":500}`)
	addComponent(t, s, sheetID, `{"stockItemId":`+strconv.FormatInt(unpriced, 10)+
		`,"unitId":`+strconv.FormatInt(unpricedUnit, 10)+`,"quantity":2}`)

	cost := sheetCostOf(t, s, sheetID)
	if cost.CostComplete {
		t.Fatal("an ingredient without a purchase price must not be costed as zero")
	}
	if len(cost.MissingPrices) != 1 || cost.MissingPrices[0] != "Azafran" {
		t.Fatalf("missingPrices=%v", cost.MissingPrices)
	}
	if cost.Zone != "" {
		t.Fatalf("zone=%q must be withheld while the cost is incomplete", cost.Zone)
	}
}
