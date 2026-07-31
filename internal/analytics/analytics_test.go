package analytics

import (
	"testing"
	"time"
)

func TestNormalizeCustomerIdentityUsesFirstAvailableStableKey(t *testing.T) {
	cases := []struct {
		name string
		in   CustomerIdentityInput
		want string
	}{
		{"email", CustomerIdentityInput{Email: "  Ada@Example.COM "}, "email:ada@example.com"},
		{"phone", CustomerIdentityInput{Phone: "+34 (600) 123-456"}, "phone:34600123456"},
		{"tax id", CustomerIdentityInput{TaxID: " b-123.456 "}, "tax:B123456"},
		{"name", CustomerIdentityInput{Name: "  Ada   Lovelace "}, "name:ada lovelace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeCustomerIdentity(tc.in); got != tc.want {
				t.Fatalf("NormalizeCustomerIdentity() = %q, want %q", got, tc.want)
			}
		})
	}
	if got := NormalizeCustomerIdentity(CustomerIdentityInput{}); got != "" {
		t.Fatalf("empty identity = %q, want empty", got)
	}
}

func TestAggregateFactsSeparatesSourcesFiltersStatusAndRefunds(t *testing.T) {
	date := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	facts := []DailyFact{
		{TenantID: 1, Date: date, Source: "INVOICE", Status: "enviada", GrossCents: 1000, CustomerID: 1},
		{TenantID: 1, Date: date, Source: "POS", Status: "PAID", GrossCents: 2500, RefundCents: 500, CustomerID: 1},
		{TenantID: 1, Date: date, Source: "POS", Status: "OPEN", GrossCents: 900},
		{TenantID: 1, Date: date, Source: "POS", Status: "VOIDED", GrossCents: 900},
		{TenantID: 2, Date: date, Source: "POS", Status: "PAID", GrossCents: 9000},
	}

	got := AggregateDailyFacts(facts, 1, date, date)
	if got.InvoicedRevenueCents != 1000 || got.POSRevenueCents != 2000 || got.POSRefundCents != 500 {
		t.Fatalf("revenue totals = %+v", got)
	}
	if got.IdentifiedPeople != 1 {
		t.Fatalf("identified people = %d, want 1", got.IdentifiedPeople)
	}
}

func TestCostFallbackOrderAndUnknownCoverage(t *testing.T) {
	cases := []struct {
		name string
		in   CostCandidates
		want float64
		ok   bool
	}{
		{"movement total", CostCandidates{MovementTotal: Float64Value(12)}, 12, true},
		{"movement unit", CostCandidates{MovementUnit: Float64Value(2), Quantity: 3}, 6, true},
		{"effective price", CostCandidates{EffectivePrice: Float64Value(4), Quantity: 3}, 12, true},
		{"average level", CostCandidates{AverageLevel: Float64Value(5), Quantity: 3}, 15, true},
		{"unknown", CostCandidates{Quantity: 3}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveCost(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ResolveCost() = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestAggregateWasteIncludesRecipeProductionAndUnknownCost(t *testing.T) {
	date := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	got := AggregateDailyFacts([]DailyFact{
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "WASTE", WasteReason: "SPOILAGE", Quantity: 2, CostCents: 300, CostKnown: true},
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "RECIPE_WASTE", WasteReason: "RECIPE", Quantity: 1, CostCents: 0, CostKnown: false},
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "PRODUCTION_VARIANCE", WasteReason: "OVERPRODUCTION", Quantity: 3, CostCents: 0, CostKnown: false},
	}, 1, date, date)
	if got.WasteQuantity != 6 || got.WasteKnownCostCents != 300 || got.WasteUnknownQuantity != 4 {
		t.Fatalf("waste = %+v", got)
	}
	if got.WasteBreakdown["SPOILAGE"].Quantity != 2 || got.WasteBreakdown["RECIPE"].Quantity != 1 {
		t.Fatalf("waste breakdown = %+v", got.WasteBreakdown)
	}
}

func TestAggregatePurchasesReportsKnownCostCoverageAndReturnsOffsetCOGS(t *testing.T) {
	date := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	got := AggregateDailyFacts([]DailyFact{
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "PURCHASE", Quantity: 10, CostCents: 1200, CostKnown: true},
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "PURCHASE", Quantity: 5, CostKnown: false},
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "SALE", Quantity: 8, CostCents: 800, CostKnown: true},
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "RETURN", Quantity: 2, CostCents: 200, CostKnown: true},
	}, 1, date, date)
	if got.StockPurchaseKnownCostCents != 1200 || got.StockPurchaseQuantity != 15 || got.StockPurchaseKnownQuantity != 10 || got.StockPurchaseUnknownQuantity != 5 {
		t.Fatalf("purchase aggregate = %+v", got)
	}
	if got.COGSQuantity != 6 || got.COGSKnownQuantity != 6 || got.COGSKnownCostCents != 600 {
		t.Fatalf("return-adjusted cogs = %+v", got)
	}
}

func TestPeriodsIncludeZeroPeriodsAndPreviousComparison(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	periods, err := BuildPeriods(from, to, GranularityMonth)
	if err != nil || len(periods) != 3 {
		t.Fatalf("periods = %+v, err=%v", periods, err)
	}
	if !periods[1].Start.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("second period = %+v", periods[1])
	}
	comparison := PreviousComparisonRange(from, to)
	if !comparison.Start.Equal(time.Date(2025, 10, 3, 0, 0, 0, 0, time.UTC)) || !comparison.End.Equal(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestComparisonDeltaUsesPreviousPeriodAndNDCostLabel(t *testing.T) {
	current := rollupAccumulator{DailyAggregate: DailyAggregate{InvoicedRevenueCents: 1500, COGSQuantity: 10, COGSKnownQuantity: 5}}
	previous := rollupAccumulator{DailyAggregate: DailyAggregate{InvoicedRevenueCents: 1000}}
	delta := comparisonDelta(current, previous)
	if delta["invoicedRevenueEUR"] != 50 {
		t.Fatalf("delta = %+v", delta)
	}
	summary := summaryFromAggregate(current)
	if summary.CostOfGoodsEUR == nil || summary.CostOfGoodsLabel != "N/D" {
		t.Fatalf("summary cost = %+v", summary)
	}
}

func TestAggregateFactsTenantIsolationAndCostCoverage(t *testing.T) {
	date := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	got := AggregateDailyFacts([]DailyFact{
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "SALE", Quantity: 4, CostCents: 100, CostKnown: true},
		{TenantID: 1, Date: date, Source: "STOCK", FactType: "SALE", Quantity: 6, CostKnown: false},
		{TenantID: 2, Date: date, Source: "STOCK", FactType: "SALE", Quantity: 100, CostCents: 99999, CostKnown: true},
	}, 1, date, date)
	if got.COGSQuantity != 10 || got.COGSKnownQuantity != 4 || got.COGSKnownCostCents != 100 {
		t.Fatalf("tenant aggregate = %+v", got)
	}
	if got.CostCoveragePercent != 40 {
		t.Fatalf("coverage = %v, want 40", got.CostCoveragePercent)
	}
}
