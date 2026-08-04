package api

import "testing"

func TestStockBaseUnitForDimension(t *testing.T) {
	tests := map[string]string{"MASS": "g", "VOLUME": "ml", "COUNT": "ud"}
	for dimension, want := range tests {
		got, ok := stockBaseUnitForDimension(dimension)
		if !ok || got != want {
			t.Fatalf("dimension %s: got %q, %v; want %q, true", dimension, got, ok, want)
		}
	}
	if _, ok := stockBaseUnitForDimension("PRICE"); ok {
		t.Fatal("invalid dimension accepted")
	}
}

func TestNormalizeStockMovementQuantity(t *testing.T) {
	got, err := normalizeStockMovementQuantity("WASTE", 2.5, 1000)
	if err != nil || got != -2500 {
		t.Fatalf("got %v, %v; want -2500, nil", got, err)
	}
	got, err = normalizeStockMovementQuantity("PURCHASE", 3, 12)
	if err != nil || got != 36 {
		t.Fatalf("got %v, %v; want 36, nil", got, err)
	}
}
