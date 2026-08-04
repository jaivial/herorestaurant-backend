package main

import "testing"

func TestSummaryIssuesExcludesHealthyOpenVisits(t *testing.T) {
	got := (summary{OpenVisits: 3, OldVisits: 1, CoverDifferences: 2, StockDifferences: 4}).issues()
	if got != 7 {
		t.Fatalf("issues=%d", got)
	}
}

// A margin scope whose key no longer points at a real category stops matching
// silently: the dish quietly falls back to the global bands and nobody is told.
// The audit surfaces it so someone can fix or delete the scope.
func TestUnresolvableScopeCountsAsAnIssue(t *testing.T) {
	base := summary{}
	if base.issues() != 0 {
		t.Fatalf("empty summary reports %d issues", base.issues())
	}
	withOrphan := summary{UnresolvableMarginScopes: 1}
	if withOrphan.issues() != 1 {
		t.Fatalf("an unresolvable scope must be reported, got %d", withOrphan.issues())
	}
}

// Open visits are normal during service, so they are informational only and
// must never raise the issue count on their own.
func TestOpenVisitsAreNotAnIssueByThemselves(t *testing.T) {
	if (summary{OpenVisits: 5}).issues() != 0 {
		t.Fatal("open visits are normal mid-service and must not trigger an alert")
	}
}
