package api

import (
	"testing"
	"time"
)

func TestPOSTicketTotalsUsesIntegerCents(t *testing.T) {
	lines := []posTotalLine{
		{Quantity: 2, UnitPriceCents: 1250, DiscountCents: 100, VATRate: 10},
		{Quantity: 1, UnitPriceCents: 450, VATRate: 21},
	}
	got, err := calculatePOSTotals(lines, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.SubtotalGrossCents != 2950 || got.DiscountCents != 150 || got.TotalGrossCents != 2800 || got.TaxCents != 291 {
		t.Fatalf("unexpected totals: %+v", got)
	}
}

func TestPOSTicketTotalsAppliesSurchargeAfterDiscount(t *testing.T) {
	lines := []posTotalLine{{Quantity: 1, UnitPriceCents: 1000, VATRate: 10}}
	got, err := calculatePOSTotalsWithAdjustments(lines, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got.SurchargeCents != 100 || got.TotalGrossCents != 1100 {
		t.Fatalf("unexpected totals: %+v", got)
	}
	// VAT is recomputed on the surcharged gross, not on the pre-surcharge gross.
	if got.TaxCents != 100 {
		t.Fatalf("unexpected tax: %+v", got)
	}
}

func TestPOSTicketTotalsRejectsNegativeSurcharge(t *testing.T) {
	lines := []posTotalLine{{Quantity: 1, UnitPriceCents: 1000, VATRate: 10}}
	if _, err := calculatePOSTotalsWithAdjustments(lines, 0, -1); err == nil {
		t.Fatal("negative surcharge accepted")
	}
}

func TestPOSTicketTotalsSurchargeAndDiscountCoexist(t *testing.T) {
	lines := []posTotalLine{{Quantity: 1, UnitPriceCents: 2000, VATRate: 10}}
	got, err := calculatePOSTotalsWithAdjustments(lines, 500, 200)
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscountCents != 500 || got.SurchargeCents != 200 || got.TotalGrossCents != 1700 {
		t.Fatalf("unexpected totals: %+v", got)
	}
}

func TestPOSBusinessMomentHonoursCutoffAndService(t *testing.T) {
	at := time.Date(2026, 7, 28, 2, 30, 0, 0, time.UTC)
	periods := []posServicePeriod{{ServiceType: "DINNER", Start: "20:00", End: "04:00"}}
	got, err := resolvePOSBusinessMoment(at, "UTC", "05:00", periods)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceDate != "2026-07-27" || got.ServiceType != "DINNER" {
		t.Fatalf("unexpected business moment: %+v", got)
	}
}

func TestPOSStockPlannedQuantity(t *testing.T) {
	got, err := posStockPlannedQuantity(2.5, 330)
	if err != nil || got != 825 {
		t.Fatalf("got %v, %v; want 825, nil", got, err)
	}
	if _, err := posStockPlannedQuantity(0, 330); err == nil {
		t.Fatal("zero sale quantity accepted")
	}
}

func TestPOSVisitCoversCountOnceAcrossTickets(t *testing.T) {
	visits := []posCoverVisit{{Covers: 4, Status: "CLOSED", Channel: "DINE_IN", PaidTickets: 2}, {Covers: 3, Status: "OPEN", Channel: "DINE_IN", PaidTickets: 1}, {Covers: 0, Status: "CLOSED", Channel: "TAKEAWAY", PaidTickets: 1}}
	if got := aggregatePOSCovers(visits, -1); got != 3 {
		t.Fatalf("got %d; want 3", got)
	}
}

func TestPOSKitchenDeltasEmitOnlyChanges(t *testing.T) {
	got := calculatePOSKitchenDeltas(map[int64]float64{1: 3, 2: 0, 3: 1}, map[int64]float64{1: 1, 2: 2, 3: 1})
	if len(got) != 2 || got[0].LineID != 1 || got[0].Action != "ADD" || got[0].Quantity != 2 || got[1].LineID != 2 || got[1].Action != "VOID" || got[1].Quantity != -2 {
		t.Fatalf("unexpected deltas: %+v", got)
	}
}

func TestPOSKitchenStatusTransitions(t *testing.T) {
	if !validPOSKitchenTransition("PENDING", "ACKNOWLEDGED") || !validPOSKitchenTransition("ACKNOWLEDGED", "READY") || validPOSKitchenTransition("READY", "PENDING") {
		t.Fatal("invalid kitchen transition rules")
	}
}

func TestPOSLiveModesRequireFreshAcceptance(t *testing.T) {
	if !requiresPOSActivationAcceptance("SHADOW", "LIVE") || requiresPOSActivationAcceptance("LIVE", "LIVE") || requiresPOSActivationAcceptance("OFF", "SHADOW") {
		t.Fatal("invalid activation acceptance rules")
	}
}

func TestNormalizeSpanishCustomerTaxID(t *testing.T) {
	tests := map[string]string{
		" 12345678z ": "12345678Z",
		"x2482300w":   "X2482300W",
		"b99286320":   "B99286320",
		"":            "",
	}
	for input, want := range tests {
		got, ok := normalizeSpanishCustomerTaxID(input)
		if !ok || got != want {
			t.Fatalf("normalizeSpanishCustomerTaxID(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	for _, invalid := range []string{"12345678A", "X2482300A", "B99286321", "not-a-tax-id"} {
		if _, ok := normalizeSpanishCustomerTaxID(invalid); ok {
			t.Fatalf("invalid tax ID %q accepted", invalid)
		}
	}
}
