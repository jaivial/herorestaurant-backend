package api

import (
	"strings"
	"testing"
)

func ptr(v float64) *float64 { return &v }

func TestStockDefaultMarginBandsShape(t *testing.T) {
	bands := stockDefaultMarginBands()
	if len(bands) != 4 {
		t.Fatalf("want 4 default bands, got %d", len(bands))
	}
	wantZones := []string{"PURPLE", "GREEN", "AMBER", "RED"}
	for i, want := range wantZones {
		if bands[i].Zone != want {
			t.Errorf("band[%d].Zone = %s, want %s", i, bands[i].Zone, want)
		}
	}
	// Defaults must classify every pct consistently with stockDefaultMarginZone.
	cases := []float64{0, 24.99, 25, 30, 34.99, 35, 37.5, 40, 40.01, 45, 100}
	for _, pct := range cases {
		if got := stockMarginZone(pct, bands); got != stockDefaultMarginZone(pct) {
			t.Errorf("defaults disagree with stockDefaultMarginZone at %v: bands=%s single=%s",
				pct, got, stockDefaultMarginZone(pct))
		}
	}
}

func TestValidateMarginScopeBands(t *testing.T) {
	t.Run("accepts a valid contiguous partition", func(t *testing.T) {
		in := []stockMarginScopeBandInput{
			{Zone: "RED", Min: ptr(40)},
			{Zone: "PURPLE", Max: ptr(25)},
			{Zone: "AMBER", Min: ptr(35), Max: ptr(40)},
			{Zone: "GREEN", Min: ptr(25), Max: ptr(35)},
		}
		out, err := validateMarginScopeBands(in)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(out))
		for i, b := range out {
			got[i] = b.Zone
		}
		want := []string{"PURPLE", "GREEN", "AMBER", "RED"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order = %v, want %v", got, want)
			}
		}
	})

	t.Run("rejects a non-contiguous partition", func(t *testing.T) {
		// gap between GREEN max (33) and AMBER min (35)
		in := []stockMarginScopeBandInput{
			{Zone: "PURPLE", Max: ptr(25)},
			{Zone: "GREEN", Min: ptr(25), Max: ptr(33)},
			{Zone: "AMBER", Min: ptr(35), Max: ptr(40)},
			{Zone: "RED", Min: ptr(40)},
		}
		if _, err := validateMarginScopeBands(in); err == nil {
			t.Fatal("non-contiguous partition must be rejected")
		}
	})

	t.Run("rejects fewer than four zones", func(t *testing.T) {
		in := []stockMarginScopeBandInput{
			{Zone: "PURPLE", Max: ptr(25)},
			{Zone: "GREEN", Min: ptr(25), Max: ptr(35)},
		}
		if _, err := validateMarginScopeBands(in); err == nil {
			t.Fatal("missing zones must be rejected")
		}
	})

	t.Run("rejects a duplicate zone", func(t *testing.T) {
		in := []stockMarginScopeBandInput{
			{Zone: "PURPLE", Max: ptr(25)},
			{Zone: "GREEN", Min: ptr(25), Max: ptr(35)},
			{Zone: "AMBER", Min: ptr(35), Max: ptr(40)},
			{Zone: "AMBER", Min: ptr(40), Max: ptr(50)},
		}
		if _, err := validateMarginScopeBands(in); err == nil {
			t.Fatal("duplicate zone must be rejected")
		}
	})

	t.Run("rejects inverted range within a band", func(t *testing.T) {
		in := []stockMarginScopeBandInput{
			{Zone: "PURPLE", Max: ptr(25)},
			{Zone: "GREEN", Min: ptr(35), Max: ptr(25)},
			{Zone: "AMBER", Min: ptr(25), Max: ptr(40)},
			{Zone: "RED", Min: ptr(40)},
		}
		if _, err := validateMarginScopeBands(in); err == nil {
			t.Fatal("inverted range must be rejected")
		}
	})

	t.Run("rejects out-of-range boundary", func(t *testing.T) {
		in := []stockMarginScopeBandInput{
			{Zone: "PURPLE", Max: ptr(25)},
			{Zone: "GREEN", Min: ptr(25), Max: ptr(35)},
			{Zone: "AMBER", Min: ptr(35), Max: ptr(40)},
			{Zone: "RED", Min: ptr(40)},
		}
		in[1].Min = ptr(150)
		if _, err := validateMarginScopeBands(in); err == nil {
			t.Fatal("value above 100 must be rejected")
		}
	})
}

func TestResolveMarginBandsInheritance(t *testing.T) {
	global := []stockMarginBand{{Zone: "GREEN", Min: ptr(20), Max: ptr(30), IsActive: true}}
	catScope := []stockMarginBand{{Zone: "RED", Min: ptr(50), IsActive: true}}
	scopes := map[string][]stockMarginBand{
		"GLOBAL:*":                 global,
		"COMIDA_CATEGORY:platos:3": catScope,
	}

	t.Run("prefers the most specific scope", func(t *testing.T) {
		chain := []stockMarginScopeSpec{
			{ScopeKind: "COMIDA_CATEGORY", ScopeKey: "platos:3"},
			{ScopeKind: "GLOBAL", ScopeKey: "*"},
		}
		got := resolveMarginBands(chain, scopes)
		if len(got) != 1 || got[0].Zone != "RED" {
			t.Fatalf("expected category bands, got %v", got)
		}
	})

	t.Run("falls back to global when specific scope is unconfigured", func(t *testing.T) {
		chain := []stockMarginScopeSpec{
			{ScopeKind: "COMIDA_CATEGORY", ScopeKey: "platos:99"},
			{ScopeKind: "GLOBAL", ScopeKey: "*"},
		}
		got := resolveMarginBands(chain, scopes)
		if len(got) != 1 || got[0].Zone != "GREEN" {
			t.Fatalf("expected global bands, got %v", got)
		}
	})

	t.Run("returns nil when nothing is configured", func(t *testing.T) {
		chain := []stockMarginScopeSpec{
			{ScopeKind: "COMIDA_CATEGORY", ScopeKey: "platos:99"},
		}
		if got := resolveMarginBands(chain, scopes); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("ignores an unresolvable key without erroring", func(t *testing.T) {
		// platos:9999 not in map -> fall through to global
		chain := []stockMarginScopeSpec{
			{ScopeKind: "COMIDA_CATEGORY", ScopeKey: "platos:9999"},
			{ScopeKind: "GLOBAL", ScopeKey: "*"},
		}
		got := resolveMarginBands(chain, scopes)
		if len(got) != 1 || got[0].Zone != "GREEN" {
			t.Fatalf("expected fall-through to global, got %v", got)
		}
	})
}

func TestMarginScopeKeyForCategory(t *testing.T) {
	cases := []struct {
		foodType string
		catID    int64
		want     string
	}{
		{"platos", 3, "platos:3"},
		{"bebidas", 12, "bebidas:12"},
	}
	for _, tc := range cases {
		if got := marginScopeKeyForCategory(tc.foodType, tc.catID); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
	if !strings.HasPrefix(marginScopeKeyForCategory("platos", 5), "platos:") {
		t.Error("key must be type-qualified")
	}
}
