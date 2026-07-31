package main

import (
	"fmt"
	"strings"
)

// Every catalogue product becomes a stock item so it can be counted. None of
// them get a technical sheet here: no sheets exist yet, so they are all RAW.
// Claiming otherwise would put a recipe in the data that nobody wrote.

// stockItemNameLimit mirrors stock_items.name varchar(180). Catalogue names can
// be much longer; without truncating, the insert fails and the product is left
// unlinked with only a log line to show for it.
const stockItemNameLimit = 180

// truncateName cuts on rune boundaries so a multi-byte character is never split
// into invalid UTF-8.
func truncateName(name string) string {
	runes := []rune(name)
	if len(runes) <= stockItemNameLimit {
		return name
	}
	return string(runes[:stockItemNameLimit])
}

// stockItemSpec is the row the backfill would create for one product.
type stockItemSpec struct {
	SKU             string
	Name            string
	Kind            string
	BaseDimension   string
	BaseUnit        string
	DeductionSource string
}

// stockSKU is the idempotency key. It is qualified by source because the three
// catalogue tables have independent id sequences: plato 12 and vino 12 are
// different products, and a bare id would merge them into one stock item.
func stockSKU(source string, id int64) string {
	return fmt.Sprintf("catalog:%s:%d", strings.ToLower(strings.TrimSpace(source)), id)
}

func newStockItemSpec(source string, id int64, name string) stockItemSpec {
	return stockItemSpec{
		SKU:  stockSKU(source, id),
		Name: truncateName(strings.TrimSpace(name)),
		// A product with no recipe is raw by definition.
		Kind: "RAW",
		// Catalogue products are sold as countable units ("2 paellas"), not by
		// weight or volume.
		BaseDimension: "COUNT",
		BaseUnit:      "ud",
		// Deducting at production would require a recipe these do not have, so
		// the only meaningful moment is the sale.
		DeductionSource: "SALE",
	}
}

// planProduct decides whether a product can be backfilled. A blank name is
// reported as skipped rather than turned into an unusable nameless item.
func planProduct(source string, id int64, name string) (stockItemSpec, bool) {
	if strings.TrimSpace(name) == "" || id <= 0 {
		return stockItemSpec{}, false
	}
	return newStockItemSpec(source, id, name), true
}

// summary is what the operator reads before deciding to run --apply, so the
// outcomes are counted separately rather than lumped into one total.
type summary struct {
	Created       int
	AlreadyLinked int
	Skipped       int
	Failed        int
}

func (s *summary) recordCreated()       { s.Created++ }
func (s *summary) recordAlreadyLinked() { s.AlreadyLinked++ }
func (s *summary) recordSkipped()       { s.Skipped++ }
func (s *summary) recordFailed()        { s.Failed++ }

func (s summary) String() string {
	return fmt.Sprintf("created=%d already_linked=%d skipped=%d failed=%d",
		s.Created, s.AlreadyLinked, s.Skipped, s.Failed)
}
