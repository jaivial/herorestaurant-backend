package api

import (
	"context"
	"testing"
)

func TestAnalyticsSectionAccessIsRestrictedToRootAndAdmin(t *testing.T) {
	s := &Server{}
	for _, role := range []string{"root", "admin"} {
		allowed, err := s.roleCanAccessSection(context.Background(), role, boSectionEstadisticas)
		if err != nil || !allowed {
			t.Fatalf("role %q allowed=%v err=%v, want allowed", role, allowed, err)
		}
	}
	for _, role := range []string{"metre", "jefe_cocina", "camarero"} {
		allowed, err := s.roleCanAccessSection(context.Background(), role, boSectionEstadisticas)
		if err != nil || allowed {
			t.Fatalf("role %q allowed=%v err=%v, want denied", role, allowed, err)
		}
	}
}

func TestAnalyticsRangeAndGranularityValidation(t *testing.T) {
	if _, err := parseAnalyticsRange("2026-07-01", "2026-07-31"); err != nil {
		t.Fatalf("valid range rejected: %v", err)
	}
	if _, err := parseAnalyticsRange("2026-08-01", "2026-07-31"); err == nil {
		t.Fatal("reversed range accepted")
	}
	if _, err := parseAnalyticsGranularity("month"); err != nil {
		t.Fatalf("valid granularity rejected: %v", err)
	}
	if _, err := parseAnalyticsGranularity("hour"); err == nil {
		t.Fatal("unsupported granularity accepted")
	}
}
