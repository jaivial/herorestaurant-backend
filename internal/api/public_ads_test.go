package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicAdVisibleOnDate(t *testing.T) {
	start := "2026-08-29"
	end := "2026-08-31"
	ad := boAd{Active: true, StartsAt: &start, EndsAt: &end}

	for _, date := range []string{"2026-08-29", "2026-08-30", "2026-08-31"} {
		if !publicAdVisibleOnDate(ad, date) {
			t.Fatalf("expected ad visible on inclusive date %s", date)
		}
	}
	if publicAdVisibleOnDate(ad, "2026-09-01") {
		t.Fatal("expected ad hidden outside its range")
	}
}

func TestPublicAdVisibleOnDateRequiresActiveAndAllowsOpenSchedule(t *testing.T) {
	if !publicAdVisibleOnDate(boAd{Active: true}, "2026-08-29") {
		t.Fatal("active ad without dates should be visible")
	}
	if publicAdVisibleOnDate(boAd{Active: false}, "2026-08-29") {
		t.Fatal("inactive ad must never be visible")
	}
}

func TestPublicAdsRejectsInvalidRestaurantIDBeforeDatabaseAccess(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/public/ads?restaurant_id=nope&date=2026-08-29", nil)
	rec := httptest.NewRecorder()
	s.handlePublicAdsList(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPublicAdsRejectsInvalidDateBeforeDatabaseAccess(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/public/ads?restaurant_id=1&date=29-08-2026", nil)
	rec := httptest.NewRecorder()
	s.handlePublicAdsList(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected no-store cache policy, got %q", got)
	}
}
