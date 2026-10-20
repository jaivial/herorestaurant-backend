// Package aiusage owns the model price catalogue and the token/cost maths used
// by every LLM call site. Prices are USD per 1M tokens.
//
// Coordination id: ai_usage_pricing_v1
package aiusage

import (
	"math"
	"strings"
	"time"
)

// Tokens is the provider-reported usage of one LLM request.
type Tokens struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

// Rate is a price list expressed in USD per 1M tokens.
type Rate struct {
	InputUSD      float64 `json:"inputUsdPerMillion"`
	OutputUSD     float64 `json:"outputUsdPerMillion"`
	CacheReadUSD  float64 `json:"cacheReadUsdPerMillion"`
	CacheWriteUSD float64 `json:"cacheWriteUsdPerMillion"`
}

// Pricing is one catalogue entry. MiniMax publishes a single flat rate; the
// peak/off-peak fields are kept for providers that bill by time of day.
type Pricing struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Peak     Rate   `json:"peak"`
	OffPeak  Rate   `json:"offPeak"`
	Flat     bool   `json:"flat"`
}

// MiniMax M3.1 Flash is the only model the product calls.
var miniMaxM31Flash = Pricing{
	Provider: "minimax",
	Model:    "MiniMax-M3.1-Flash",
	Peak: Rate{
		InputUSD:      0.15,
		OutputUSD:     0.60,
		CacheReadUSD:  0.015,
		CacheWriteUSD: 0,
	},
	OffPeak: Rate{
		InputUSD:      0.15,
		OutputUSD:     0.60,
		CacheReadUSD:  0.015,
		CacheWriteUSD: 0,
	},
	Flat: true,
}

// MiniMax M3 legacy pricing. It is not selectable any more; the entry exists
// so historical ai_usage_logs rows keep a real cost instead of "sin tarifa".
var miniMaxM3Legacy = Pricing{
	Provider: "minimax",
	Model:    "MiniMax-M3",
	Peak: Rate{
		InputUSD:      0.30,
		OutputUSD:     1.20,
		CacheReadUSD:  0.06,
		CacheWriteUSD: 0,
	},
	OffPeak: Rate{
		InputUSD:      0.30,
		OutputUSD:     1.20,
		CacheReadUSD:  0.06,
		CacheWriteUSD: 0,
	},
	Flat: true,
}

var catalogue = []Pricing{miniMaxM31Flash, miniMaxM3Legacy}

// Catalog returns the known price list.
func Catalog() []Pricing {
	out := make([]Pricing, len(catalogue))
	copy(out, catalogue)
	return out
}

// Normalize maps provider/model aliases to the canonical catalogue identity.
// Canonical provider is returned for display; the caller keeps the original
// model string in the usage row.
func Normalize(provider, model string) (string, string) {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.TrimSpace(model)
	lower := strings.ToLower(m)

	if strings.Contains(lower, "minimax-m3.1-flash") || strings.Contains(lower, "minimax-m3-1-flash") {
		return "minimax", "MiniMax-M3.1-Flash"
	}
	if lower == "minimax-m3" {
		return "minimax", "MiniMax-M3"
	}
	return p, m
}

// Lookup returns the pricing entry for a provider/model pair.
func Lookup(provider, model string) (Pricing, bool) {
	cp, cm := Normalize(provider, model)
	for _, p := range catalogue {
		if strings.EqualFold(p.Provider, cp) && strings.EqualFold(p.Model, cm) {
			return p, true
		}
	}
	return Pricing{}, false
}

// RateAt returns the effective rate for the given instant.
func RateAt(provider, model string, at time.Time) (Rate, bool) {
	p, ok := Lookup(provider, model)
	if !ok {
		return Rate{}, false
	}
	if p.Flat {
		return p.Peak, true
	}
	if IsPeak(at) {
		return p.Peak, true
	}
	return p.OffPeak, true
}

// IsPeak reports whether at falls inside DeepSeek peak hours: Mon-Fri
// 01:00-04:00 and 06:00-10:00 UTC.
func IsPeak(at time.Time) bool {
	utc := at.UTC()
	switch utc.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	hour := utc.Hour()
	return (hour >= 1 && hour < 4) || (hour >= 6 && hour < 10)
}

// Cost returns the USD cost of tokens at the effective rate and whether the
// model has a known price. Unknown models cost 0 and are flagged unpriced.
func Cost(provider, model string, tokens Tokens, at time.Time) (float64, bool) {
	rate, ok := RateAt(provider, model, at)
	if !ok {
		return 0, false
	}
	total := float64(tokens.Input)*rate.InputUSD +
		float64(tokens.Output)*rate.OutputUSD +
		float64(tokens.CacheRead)*rate.CacheReadUSD +
		float64(tokens.CacheWrite)*rate.CacheWriteUSD
	return math.Round(total/1_000_000*1e8) / 1e8, true
}
