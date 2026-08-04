package api

import (
	"testing"
	"time"
)

func TestInvoiceDashboardRange(t *testing.T) {
	fromMonth, fromWeek, through := invoiceDashboardRange(time.Date(2026, time.July, 29, 13, 0, 0, 0, time.Local))
	if fromMonth != "2026-07-01" || fromWeek != "2026-07-27" || through != "2026-07-29" {
		t.Fatalf("unexpected range: month=%s week=%s through=%s", fromMonth, fromWeek, through)
	}
}

func TestShouldRefreshBOSession(t *testing.T) {
	now := time.Date(2026, time.July, 29, 13, 0, 0, 0, time.UTC)
	if shouldRefreshBOSession(now.Add(-30*time.Second), now) {
		t.Fatal("30 second heartbeat should not refresh")
	}
	if !shouldRefreshBOSession(now.Add(-time.Minute), now) {
		t.Fatal("one minute heartbeat should refresh")
	}
}
