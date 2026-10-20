package aiusage

import (
	"math"
	"testing"
	"time"
)

func TestCostMiniMaxM31Flash(t *testing.T) {
	tokens := Tokens{Input: 1000, Output: 500, CacheRead: 2000}
	// 1000*0.15 + 500*0.60 + 2000*0.015 = 480 -> 0.00048 USD.
	got, priced := Cost("minimax", "MiniMax-M3.1-Flash", tokens, time.Now())
	if !priced {
		t.Fatal("MiniMax-M3.1-Flash must be priced")
	}
	if math.Abs(got-0.00048) > 1e-12 {
		t.Fatalf("cost = %v, want 0.00048", got)
	}
}

func TestCostMiniMaxM3Legacy(t *testing.T) {
	tokens := Tokens{Input: 1000, Output: 500, CacheRead: 2000}
	// 1000*0.30 + 500*1.20 + 2000*0.06 = 1020 -> 0.00102 USD.
	got, priced := Cost("minimax", "MiniMax-M3", tokens, time.Now())
	if !priced {
		t.Fatal("MiniMax-M3 keeps a legacy price for historical rows")
	}
	if math.Abs(got-0.00102) > 1e-12 {
		t.Fatalf("cost = %v, want 0.00102", got)
	}
}

func TestCostUnknownModelIsUnpriced(t *testing.T) {
	got, priced := Cost("opencode-go", "deepseek-v4.1-flash", Tokens{Input: 1000}, time.Now())
	if priced || got != 0 {
		t.Fatalf("unknown model = (%v, %v), want (0, false)", got, priced)
	}
}

func TestMiniMaxFlatRateDoesNotChangeWithPeak(t *testing.T) {
	peak := time.Date(2026, time.October, 20, 2, 0, 0, 0, time.UTC) // Tuesday 02:00 UTC
	offPeak := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC)
	tokens := Tokens{Input: 1_000_000, Output: 1_000_000, CacheRead: 1_000_000}
	atPeak, ok := Cost("minimax", "MiniMax-M3.1-Flash", tokens, peak)
	if !ok {
		t.Fatal("peak lookup failed")
	}
	atOffPeak, ok := Cost("minimax", "MiniMax-M3.1-Flash", tokens, offPeak)
	if !ok {
		t.Fatal("off-peak lookup failed")
	}
	if atPeak != atOffPeak {
		t.Fatalf("flat rate changed: peak=%v off-peak=%v", atPeak, atOffPeak)
	}
}

func TestNormalizeAliases(t *testing.T) {
	cases := []struct{ model, provider, want string }{
		{"MiniMax-M3.1-Flash", "minimax", "MiniMax-M3.1-Flash"},
		{"minimax-m3.1-flash", "minimax", "MiniMax-M3.1-Flash"},
		{"minimax-m3-1-flash", "minimax", "MiniMax-M3.1-Flash"},
		{"MiniMax-M3", "minimax", "MiniMax-M3"},
	}
	for _, c := range cases {
		_, got := Normalize(c.provider, c.model)
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}
