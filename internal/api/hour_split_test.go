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

func approxEqual(a, b float64) bool {
	const eps = 0.011
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func TestRebalancePercentagesScalesOthers(t *testing.T) {
	in := map[string]float64{"13:00": 20, "13:30": 20, "14:00": 20, "14:30": 20, "15:00": 20}
	out := rebalancePercentages(in, "13:00", 40)

	if out["13:00"] != 40 {
		t.Fatalf("changed hour = %v; want 40", out["13:00"])
	}
	// Remaining 60 spread across 4 others => 15 each.
	for _, h := range []string{"13:30", "14:00", "14:30", "15:00"} {
		if !approxEqual(out[h], 15) {
			t.Fatalf("sibling %s = %v; want ~15", h, out[h])
		}
	}
	if sum := sumPct(out); !approxEqual(sum, 100) {
		t.Fatalf("sum = %v; want 100", sum)
	}
}

func TestRebalancePercentagesSingleHour(t *testing.T) {
	out := rebalancePercentages(map[string]float64{"13:00": 0}, "13:00", 50)
	if out["13:00"] != 100 {
		t.Fatalf("single hour = %v; want 100", out["13:00"])
	}
}

func TestRebalancePercentagesZeroWeightsDistributesEqually(t *testing.T) {
	in := map[string]float64{"13:00": 0, "13:30": 0, "14:00": 0}
	out := rebalancePercentages(in, "13:00", 40)
	if !approxEqual(sumPct(out), 100) {
		t.Fatalf("sum = %v; want 100", sumPct(out))
	}
	for _, h := range []string{"13:30", "14:00"} {
		if !approxEqual(out[h], 30) {
			t.Fatalf("zero-weight sibling %s = %v; want 30", h, out[h])
		}
	}
}

func TestRebalancePercentagesClamps(t *testing.T) {
	out := rebalancePercentages(map[string]float64{"13:00": 50, "14:00": 50}, "13:00", 150)
	if out["13:00"] != 100 {
		t.Fatalf("clamped = %v; want 100", out["13:00"])
	}
	if out["14:00"] != 0 {
		t.Fatalf("sibling = %v; want 0", out["14:00"])
	}
}

func TestPercentagesToPeopleAndBack(t *testing.T) {
	pcts := map[string]float64{"13:00": 40, "14:00": 60}
	people := percentagesToPeople(pcts, 100)
	if people["13:00"] != 40 || people["14:00"] != 60 {
		t.Fatalf("people = %v", people)
	}
	if got := peopleToPercentage(34, 100); !approxEqual(got, 34) {
		t.Fatalf("peopleToPercentage = %v; want 34", got)
	}
}

func TestValidatePercentagesSum(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]float64
		want bool
	}{
		{"exact", map[string]float64{"13:00": 40, "14:00": 60}, true},
		{"drift", map[string]float64{"13:00": 33.3, "13:30": 33.3, "14:00": 33.4}, true},
		{"off", map[string]float64{"13:00": 40, "14:00": 50}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePercentagesSum(tc.in); got != tc.want {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizePercentagesSynthesizesEqual(t *testing.T) {
	out, synthesized := normalizePercentages(map[string]float64{}, []string{"13:00", "13:30", "14:00"})
	if !synthesized {
		t.Fatalf("expected synthesized split")
	}
	if !approxEqual(sumPct(out), 100) {
		t.Fatalf("sum = %v; want 100", sumPct(out))
	}
}

// ---------------------------------------------------------------------------
// Integration (requires TEST_DB_DSN): override precedence + persistence.
// ---------------------------------------------------------------------------

func cleanHourSplit(t *testing.T, db *sql.DB, restaurantID int, date string) {
	t.Helper()
	for _, q := range []string{
		"DELETE FROM hour_split_override WHERE restaurant_id = ? AND reservationDate = ?",
		"DELETE FROM hours_percentage WHERE restaurant_id = ? AND reservationDate = ?",
	} {
		if _, err := db.Exec(q, restaurantID, date); err != nil {
			t.Fatalf("cleanup %q: %v", q, err)
		}
	}
}

func TestResolveHourSplitEnabledPrecedence(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	date := time.Date(2099, time.February, 3, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	cleanHourSplit(t, db, restaurantID, date)
	t.Cleanup(func() { cleanHourSplit(t, db, restaurantID, date) })

	// Default (no override): restaurant default row absent → true.
	got, source, err := srv.resolveHourSplitEnabled(context.Background(), restaurantID, date)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !got || source != "default" {
		t.Fatalf("default = (%v,%q); want (true,default)", got, source)
	}

	// Set default to false via upsert.
	defaults, err := srv.loadReservationDefaults(context.Background(), restaurantID)
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	defaults.HourSplitEnabled = false
	if err := srv.upsertReservationDefaults(context.Background(), restaurantID, defaults); err != nil {
		t.Fatalf("upsert defaults: %v", err)
	}
	got, source, err = srv.resolveHourSplitEnabled(context.Background(), restaurantID, date)
	if err != nil || source != "default" {
		t.Fatalf("after default=false: (%v,%q,err=%v)", got, source, err)
	}
	if got {
		t.Fatalf("expected default=false to propagate")
	}

	// Per-date override flips it back to true for this date only.
	if err := srv.setHourSplitOverride(context.Background(), restaurantID, date, true); err != nil {
		t.Fatalf("set override: %v", err)
	}
	got, source, err = srv.resolveHourSplitEnabled(context.Background(), restaurantID, date)
	if err != nil || !got || source != "override" {
		t.Fatalf("override = (%v,%q,err=%v); want (true,override)", got, source, err)
	}

	// Clear override falls back to default (false).
	if err := srv.clearHourSplitOverride(context.Background(), restaurantID, date); err != nil {
		t.Fatalf("clear override: %v", err)
	}
	got, source, err = srv.resolveHourSplitEnabled(context.Background(), restaurantID, date)
	if err != nil || got || source != "default" {
		t.Fatalf("after clear = (%v,%q,err=%v); want (false,default)", got, source, err)
	}

	// Restore default to true so other tests are unaffected.
	defaults, _ = srv.loadReservationDefaults(context.Background(), restaurantID)
	defaults.HourSplitEnabled = true
	_ = srv.upsertReservationDefaults(context.Background(), restaurantID, defaults)
}

func TestSaveLoadHourPercentagesRoundTrip(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	date := time.Date(2099, time.March, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	hours := []string{"13:00", "13:30", "14:00"}
	cleanHourSplit(t, db, restaurantID, date)
	t.Cleanup(func() { cleanHourSplit(t, db, restaurantID, date) })

	pcts := map[string]float64{"13:00": 50, "13:30": 25, "14:00": 25}
	if err := srv.saveHourPercentages(context.Background(), restaurantID, date, pcts); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _, err := srv.loadHourPercentages(context.Background(), restaurantID, date, hours)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for h, want := range pcts {
		if !approxEqual(got[h], want) {
			t.Fatalf("loaded[%s] = %v; want %v", h, got[h], want)
		}
	}
}

func TestHandleGetHourDataDisabledOmitsTotalCapacity(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	date := time.Date(2099, time.April, 5, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	cleanHourSplit(t, db, restaurantID, date)
	t.Cleanup(func() { cleanHourSplit(t, db, restaurantID, date) })

	// Ensure default enabled=true so the "disabled" path is exercised only after override.
	defaults, _ := srv.loadReservationDefaults(context.Background(), restaurantID)
	defaults.HourSplitEnabled = true
	_ = srv.upsertReservationDefaults(context.Background(), restaurantID, defaults)

	// Force disabled for the date.
	if err := srv.setHourSplitOverride(context.Background(), restaurantID, date, false); err != nil {
		t.Fatalf("override: %v", err)
	}

	// Set a daily limit so disabled-mode capacity is deterministic.
	if _, err := db.Exec(`DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?`, restaurantID, date); err != nil {
		t.Fatalf("clear rm: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reservation_manager (restaurant_id, reservationDate, dailyLimit) VALUES (?, ?, ?)`, restaurantID, date, 100); err != nil {
		t.Fatalf("insert rm: %v", err)
	}

	// Seed opening hours for the date so getOpeningHoursForDate returns them.
	hoursJSON, _ := json.Marshal([]string{"13:00", "13:30", "14:00"})
	if _, err := db.Exec(`DELETE FROM openinghours WHERE restaurant_id = ? AND dateselected = ?`, restaurantID, date); err != nil {
		t.Fatalf("clear oh: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO openinghours (restaurant_id, dateselected, hoursarray) VALUES (?, ?, ?)`, restaurantID, date, string(hoursJSON)); err != nil {
		t.Fatalf("insert oh: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM openinghours WHERE restaurant_id = ? AND dateselected = ?`, restaurantID, date)
		_, _ = db.Exec(`DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?`, restaurantID, date)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/reservations/hour-data?date="+date, strings.NewReader(""))
	req = req.WithContext(withRestaurantID(context.Background(), restaurantID))
	rec := httptest.NewRecorder()
	srv.handleGetHourData(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, rec.Body.String())
	}
	if resp["hourSplitEnabled"] != false {
		t.Fatalf("hourSplitEnabled = %v; want false", resp["hourSplitEnabled"])
	}
	if _, ok := resp["hourlyCapacities"]; ok {
		t.Fatalf("hourlyCapacities must be omitted when disabled")
	}
	hourData := resp["hourData"].(map[string]any)
	for h, raw := range hourData {
		slot := raw.(map[string]any)
		if _, hasTotal := slot["totalCapacity"]; hasTotal {
			t.Fatalf("slot %s must omit totalCapacity when disabled", h)
		}
		cap, _ := slot["capacity"].(float64)
		if int(cap) != 100 {
			t.Fatalf("slot %s capacity = %v; want 100 (full day limit)", h, cap)
		}
	}
}

func TestHandleGetHourDataEnabledReportsCapacities(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	date := time.Date(2099, time.May, 9, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	cleanHourSplit(t, db, restaurantID, date)
	t.Cleanup(func() { cleanHourSplit(t, db, restaurantID, date) })

	defaults, _ := srv.loadReservationDefaults(context.Background(), restaurantID)
	defaults.HourSplitEnabled = true
	_ = srv.upsertReservationDefaults(context.Background(), restaurantID, defaults)

	if _, err := db.Exec(`DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?`, restaurantID, date); err != nil {
		t.Fatalf("clear rm: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reservation_manager (restaurant_id, reservationDate, dailyLimit) VALUES (?, ?, ?)`, restaurantID, date, 100); err != nil {
		t.Fatalf("insert rm: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?`, restaurantID, date)
	})

	// Store custom percentages via the canonical table.
	pcts := map[string]float64{"13:00": 40, "13:30": 30, "14:00": 30}
	if err := srv.saveHourPercentages(context.Background(), restaurantID, date, pcts); err != nil {
		t.Fatalf("save pct: %v", err)
	}

	hoursJSON, _ := json.Marshal([]string{"13:00", "13:30", "14:00"})
	if _, err := db.Exec(`DELETE FROM openinghours WHERE restaurant_id = ? AND dateselected = ?`, restaurantID, date); err != nil {
		t.Fatalf("clear oh: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO openinghours (restaurant_id, dateselected, hoursarray) VALUES (?, ?, ?)`, restaurantID, date, string(hoursJSON)); err != nil {
		t.Fatalf("insert oh: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM openinghours WHERE restaurant_id = ? AND dateselected = ?`, restaurantID, date)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/reservations/hour-data?date="+date, strings.NewReader(""))
	req = req.WithContext(withRestaurantID(context.Background(), restaurantID))
	rec := httptest.NewRecorder()
	srv.handleGetHourData(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, rec.Body.String())
	}
	if resp["hourSplitEnabled"] != true {
		t.Fatalf("hourSplitEnabled = %v; want true", resp["hourSplitEnabled"])
	}
	hourly, ok := resp["hourlyCapacities"].(map[string]any)
	if !ok {
		t.Fatalf("hourlyCapacities missing")
	}
	if int(hourly["13:00"].(float64)) != 40 {
		t.Fatalf("13:00 capacity = %v; want 40", hourly["13:00"])
	}
	hourData := resp["hourData"].(map[string]any)
	slot := hourData["13:00"].(map[string]any)
	if int(slot["totalCapacity"].(float64)) != 40 {
		t.Fatalf("13:00 totalCapacity = %v; want 40", slot["totalCapacity"])
	}
}
