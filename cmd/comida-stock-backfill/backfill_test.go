package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The SKU is the idempotency key: re-running the backfill must find the same
// row rather than create a second stock item for the same product.
func TestSkuIsStableAndDistinctPerSource(t *testing.T) {
	seen := map[string]string{}
	for _, c := range []struct {
		source string
		id     int64
	}{
		{"platos", 12}, {"bebidas", 12}, {"cafes", 12}, {"vinos", 12}, {"postres", 12},
	} {
		sku := stockSKU(c.source, c.id)
		if sku == "" {
			t.Fatalf("%s:%d produced an empty sku", c.source, c.id)
		}
		// platos 12 and vinos 12 are DIFFERENT products: the id spaces overlap
		// across tables, so an unqualified sku would merge two products into one
		// stock item.
		if prev, clash := seen[sku]; clash {
			t.Fatalf("sku %q collides between %s and %s", sku, prev, c.source)
		}
		seen[sku] = c.source
	}

	if stockSKU("platos", 12) != stockSKU("platos", 12) {
		t.Fatal("sku must be stable across runs")
	}
}

func TestSkuFitsTheColumn(t *testing.T) {
	// stock_items.sku is varchar(64); a truncated sku would break the unique
	// key that makes this backfill idempotent.
	if got := len(stockSKU("postres", 9223372036854775807)); got > 64 {
		t.Fatalf("sku length %d exceeds the column width", got)
	}
}

// Every backfilled product is a countable unit with no recipe behind it yet.
func TestBackfilledItemsAreRawCountableUnits(t *testing.T) {
	spec := newStockItemSpec("platos", 1, "Paella")
	if spec.Kind != "RAW" {
		t.Fatalf("kind=%q; without a technical sheet a product is raw", spec.Kind)
	}
	if spec.BaseUnit != "ud" || spec.BaseDimension != "COUNT" {
		t.Fatalf("unit=%q dimension=%q want ud/COUNT", spec.BaseUnit, spec.BaseDimension)
	}
	// Deducting at production would require a recipe, which these do not have.
	if spec.DeductionSource != "SALE" {
		t.Fatalf("deductionSource=%q want SALE", spec.DeductionSource)
	}
}

func TestProductWithoutANameIsSkippedRatherThanNamedBlank(t *testing.T) {
	// A nameless stock item is unusable in every picker and report, so it is
	// reported as skipped instead of created with an empty name.
	if _, ok := planProduct("platos", 5, "   "); ok {
		t.Fatal("a blank name must not produce a stock item")
	}
	if _, ok := planProduct("platos", 5, "Paella"); !ok {
		t.Fatal("a named product must be planned")
	}
}

// The summary is what the operator reads to decide whether to run --apply.
func TestSummaryCountsAreSeparate(t *testing.T) {
	s := summary{}
	s.recordCreated()
	s.recordCreated()
	s.recordAlreadyLinked()
	s.recordSkipped()
	if s.Created != 2 || s.AlreadyLinked != 1 || s.Skipped != 1 {
		t.Fatalf("created=%d linked=%d skipped=%d", s.Created, s.AlreadyLinked, s.Skipped)
	}
}

// stock_items.name is varchar(180) but catalogue names can be far longer.
// Without truncation the insert fails and the product is silently left
// unlinked, which is exactly what happened on the first real run.
func TestLongProductNameIsTruncatedToFitTheColumn(t *testing.T) {
	long := strings.Repeat("á", 255) // multi-byte on purpose
	spec, ok := planProduct("platos", 13, long)
	if !ok {
		t.Fatal("a long name is still a valid product")
	}
	if runes := []rune(spec.Name); len(runes) > stockItemNameLimit {
		t.Fatalf("name kept %d characters, want at most %d", len(runes), stockItemNameLimit)
	}
	// Truncating mid-rune would corrupt the text; the result must stay valid.
	if !utf8.ValidString(spec.Name) {
		t.Fatal("truncation produced invalid UTF-8")
	}
}

func TestShortNamesAreLeftAlone(t *testing.T) {
	spec, _ := planProduct("platos", 1, "Paella de marisco")
	if spec.Name != "Paella de marisco" {
		t.Fatalf("name=%q was altered unnecessarily", spec.Name)
	}
}
