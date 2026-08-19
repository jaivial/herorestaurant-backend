package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func cleanLocationBooking(t *testing.T, db *sql.DB, restaurantID int, date string) {
	t.Helper()
	for _, q := range []string{
		"DELETE FROM location_booking_override WHERE restaurant_id = ? AND reservationDate = ?",
	} {
		if _, err := db.Exec(q, restaurantID, date); err != nil {
			t.Fatalf("cleanup %q: %v", q, err)
		}
	}
}

func restoreLocationBookingDefaults(t *testing.T, srv *Server, restaurantID int) {
	t.Helper()
	defaults, err := srv.loadReservationDefaults(context.Background(), restaurantID)
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	defaults.AllowFloorReservation = false
	defaults.AllowSalonReservation = false
	if err := srv.upsertReservationDefaults(context.Background(), restaurantID, defaults); err != nil {
		t.Fatalf("restore defaults: %v", err)
	}
}

func TestResolveLocationBookingPrecedence(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	date := time.Date(2099, time.February, 10, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	cleanLocationBooking(t, db, restaurantID, date)
	t.Cleanup(func() {
		cleanLocationBooking(t, db, restaurantID, date)
		restoreLocationBookingDefaults(t, srv, restaurantID)
	})

	// 1) No defaults row, no override: both flags default to false.
	flags, err := srv.resolveLocationBooking(context.Background(), restaurantID, date)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if flags.Floor.Value || flags.Floor.Global || flags.Salon.Value || flags.Salon.Global {
		t.Fatalf("defaults = %+v; want all false", flags)
	}

	// 2) Global defaults true: effective follows global.
	defaults, err := srv.loadReservationDefaults(context.Background(), restaurantID)
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	defaults.AllowFloorReservation = true
	defaults.AllowSalonReservation = true
	if err := srv.upsertReservationDefaults(context.Background(), restaurantID, defaults); err != nil {
		t.Fatalf("upsert defaults: %v", err)
	}
	flags, err = srv.resolveLocationBooking(context.Background(), restaurantID, date)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !flags.Floor.Value || !flags.Floor.Global || !flags.Salon.Value || !flags.Salon.Global {
		t.Fatalf("after global=true: %+v; want all true", flags)
	}

	// 3) Floor-only override false: floor flips, salon inherits true.
	floorFalse := locationOverrideUpdate{Set: true, Value: false}
	if err := srv.applyLocationBookingOverride(context.Background(), restaurantID, date, floorFalse, locationOverrideUpdate{}); err != nil {
		t.Fatalf("apply override: %v", err)
	}
	flags, err = srv.resolveLocationBooking(context.Background(), restaurantID, date)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if flags.Floor.Value || !flags.Floor.Global {
		t.Fatalf("floor override = %+v; want value=false global=true", flags.Floor)
	}
	if !flags.Salon.Value || !flags.Salon.Global {
		t.Fatalf("salon inherit = %+v; want value=true global=true", flags.Salon)
	}

	// 4) Clear floor override (inherit): row has no overrides left → deleted.
	if err := srv.applyLocationBookingOverride(context.Background(), restaurantID, date, locationOverrideUpdate{Set: true, Inherit: true}, locationOverrideUpdate{Set: true, Inherit: true}); err != nil {
		t.Fatalf("clear override: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM location_booking_override WHERE restaurant_id = ? AND reservationDate = ?", restaurantID, date).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("override rows = %d; want 0 after clearing both flags", count)
	}
	flags, err = srv.resolveLocationBooking(context.Background(), restaurantID, date)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !flags.Floor.Value || !flags.Salon.Value {
		t.Fatalf("after clear = %+v; want both inheriting true", flags)
	}
}

func TestHandleLocationBookingSetGetRoundTrip(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	date := time.Date(2099, time.February, 11, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	cleanLocationBooking(t, db, restaurantID, date)
	t.Cleanup(func() {
		cleanLocationBooking(t, db, restaurantID, date)
		restoreLocationBookingDefaults(t, srv, restaurantID)
	})
	restoreLocationBookingDefaults(t, srv, restaurantID)

	// Global: floor on, salon off.
	defaults, err := srv.loadReservationDefaults(context.Background(), restaurantID)
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	defaults.AllowFloorReservation = true
	defaults.AllowSalonReservation = false
	if err := srv.upsertReservationDefaults(context.Background(), restaurantID, defaults); err != nil {
		t.Fatalf("upsert defaults: %v", err)
	}

	// GET: inherit → effective mirrors global, overrides null.
	got := salonsReq(t, srv.handleBOConfigLocationBookingGet, http.MethodGet, "/admin/config/location-booking?date="+date, "", nil)
	if got["success"] != true {
		t.Fatalf("get = %v", got)
	}
	eff := got["effective"].(map[string]any)
	if eff["allowFloorReservation"] != true || eff["allowSalonReservation"] != false {
		t.Fatalf("effective = %v", eff)
	}
	ovr := got["override"].(map[string]any)
	if ovr["allowFloorReservation"] != nil || ovr["allowSalonReservation"] != nil {
		t.Fatalf("override = %v; want nils", ovr)
	}

	// POST: pin salon=true for the date.
	body := `{"date":"` + date + `","allowSalonReservation":true}`
	got = salonsReq(t, srv.handleBOConfigLocationBookingSet, http.MethodPost, "/admin/config/location-booking", body, nil)
	if got["success"] != true {
		t.Fatalf("set = %v", got)
	}
	eff = got["effective"].(map[string]any)
	if eff["allowSalonReservation"] != true {
		t.Fatalf("effective salon after override = %v", eff)
	}
	if eff["allowFloorReservation"] != true {
		t.Fatalf("effective floor must stay inherited true, got %v", eff)
	}
	ovr = got["override"].(map[string]any)
	if ovr["allowSalonReservation"] != true {
		t.Fatalf("override salon = %v; want true", ovr)
	}
	if ovr["allowFloorReservation"] != nil {
		t.Fatalf("override floor = %v; want nil (inherit)", ovr)
	}

	// POST: back to inherit (null) → override row removed.
	body = `{"date":"` + date + `","allowSalonReservation":null}`
	got = salonsReq(t, srv.handleBOConfigLocationBookingSet, http.MethodPost, "/admin/config/location-booking", body, nil)
	if got["success"] != true {
		t.Fatalf("clear = %v", got)
	}
	ovr = got["override"].(map[string]any)
	if ovr["allowSalonReservation"] != nil {
		t.Fatalf("override salon after inherit = %v; want nil", ovr)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM location_booking_override WHERE restaurant_id = ? AND reservationDate = ?", restaurantID, date).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("override rows = %d; want 0", count)
	}
}

func TestHandleDefaultsSetIncludesLocationBookingFlags(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	t.Cleanup(func() { restoreLocationBookingDefaults(t, srv, restaurantID) })

	req := httptest.NewRequest(http.MethodPost, "/admin/config/defaults", strings.NewReader(`{"allowFloorReservation":true}`))
	ctx := withBOAuth(req.Context(), boAuth{ActiveRestaurantID: restaurantID, Role: "root", User: boUser{ID: 7}})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handleBOConfigDefaultsSet(rec, req)

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if out["success"] != true {
		t.Fatalf("set defaults = %v", out)
	}
	if out["allowFloorReservation"] != true {
		t.Fatalf("allowFloorReservation = %v; want true", out["allowFloorReservation"])
	}
	if v, ok := out["allowSalonReservation"].(bool); !ok || v {
		t.Fatalf("allowSalonReservation = %v; want false", out["allowSalonReservation"])
	}
}
