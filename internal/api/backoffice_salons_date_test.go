package api

import (
	"net/http"
	"testing"
)

// Date-scoped salons: creating a salon with a date only exists for that date;
// global salons (specific_date NULL) still show unless overridden.

func TestSalonDateScopedCrudIntegration(t *testing.T) {
	s, db := setupSalonsTestServer(t)
	_ = db
	floor0 := floorIDForNumber(t, db, 0)

	t.Run("create salon for a specific date only", func(t *testing.T) {
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor0)+`,"name":"Terraza Feria","hasCapacityLimit":true,"capacityLimit":50,"date":"2026-08-18"}`, nil)
		if out["success"] != true {
			t.Fatalf("create failed: %v", out)
		}
		// Visible on that date...
		day := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-18", "", nil)
		if len(day["salons"].([]any)) != 1 {
			t.Fatalf("expected date-scoped salon on its date, got %v", day["salons"])
		}
		// ...but not on other dates or the default view.
		other := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-19", "", nil)
		if len(other["salons"].([]any)) != 0 {
			t.Fatalf("expected no date-scoped salon on other date, got %v", other["salons"])
		}
		base := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons", "", nil)
		if len(base["salons"].([]any)) != 0 {
			t.Fatalf("expected no date-scoped salon in default view, got %v", base["salons"])
		}
	})

	t.Run("duplicate name check only within same date scope", func(t *testing.T) {
		// Same name as the date-scoped salon but on a different date -> allowed.
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor0)+`,"name":"Terraza Feria","hasCapacityLimit":false,"capacityLimit":45,"date":"2026-08-19"}`, nil)
		if out["success"] != true {
			t.Fatalf("same name on different date should be allowed, got %v", out)
		}
	})

	t.Run("global salon hidden when date-scoped duplicate name exists", func(t *testing.T) {
		// Create a global salon with the same name; on 2026-08-18 the date-scoped
		// one must win (shadow) and the global one must not be duplicated.
		out := salonsReq(t, s.handleBOConfigSalonsCreate, http.MethodPost, "/admin/config/salons",
			`{"floorId":`+itoa(floor0)+`,"name":"Terraza Feria","hasCapacityLimit":false,"capacityLimit":45}`, nil)
		if out["success"] != true {
			t.Fatalf("global create failed: %v", out)
		}
		day := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-18", "", nil)
		salons := day["salons"].([]any)
		if len(salons) != 1 {
			t.Fatalf("expected date-scoped salon to shadow global duplicate, got %d: %v", len(salons), salons)
		}
	})

	t.Run("update date-scoped salon", func(t *testing.T) {
		day := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-18", "", nil)
		salons := day["salons"].([]any)
		sal := salons[0].(map[string]any)
		id := int(sal["id"].(float64))
		out := salonsReq(t, s.handleBOConfigSalonsUpdate, http.MethodPut, "/admin/config/salons/"+itoa(id),
			`{"floorId":`+itoa(floor0)+`,"name":"Terraza Feria VIP","hasCapacityLimit":true,"capacityLimit":60}`, map[string]string{"salonId": itoa(id)})
		if out["success"] != true {
			t.Fatalf("update failed: %v", out)
		}
		day = salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-18", "", nil)
		sal = day["salons"].([]any)[0].(map[string]any)
		if sal["name"] != "Terraza Feria VIP" || sal["capacityLimit"].(float64) != 60 {
			t.Fatalf("bad update: %v", sal)
		}
		// Other date unaffected.
		other := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-19", "", nil)
		for _, raw := range other["salons"].([]any) {
			s := raw.(map[string]any)
			if s["name"] == "Terraza Feria" && s["id"].(float64) == float64(id) {
				t.Fatalf("date-scoped salon leaked to other date")
			}
		}
	})

	t.Run("delete date-scoped salon only removes that date", func(t *testing.T) {
		day := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons?date=2026-08-19", "", nil)
		sal := day["salons"].([]any)[0].(map[string]any)
		id := int(sal["id"].(float64))
		out := salonsReq(t, s.handleBOConfigSalonsDelete, http.MethodDelete, "/admin/config/salons/"+itoa(id), "", map[string]string{"salonId": itoa(id)})
		if out["success"] != true {
			t.Fatalf("delete failed: %v", out)
		}
		base := salonsReq(t, s.handleBOConfigSalonsList, http.MethodGet, "/admin/config/salons", "", nil)
		if len(base["salons"].([]any)) != 1 {
			t.Fatalf("global salon should remain after date-scoped delete, got %v", base["salons"])
		}
	})
}
