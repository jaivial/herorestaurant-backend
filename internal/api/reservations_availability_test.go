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

func TestBuildMonthAvailabilityDayOverrides(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	testDate := time.Date(2099, time.January, 15, 0, 0, 0, 0, time.UTC)
	date := testDate.Format("2006-01-02")
	cleanupDayAvailability(t, db, restaurantID, date)
	t.Cleanup(func() { cleanupDayAvailability(t, db, restaurantID, date) })

	if _, err := db.Exec(`INSERT INTO reservation_manager (restaurant_id, reservationDate, dailyLimit) VALUES (?, ?, ?)`, restaurantID, date, 40); err != nil {
		t.Fatalf("insert daily limit: %v", err)
	}

	cases := []struct {
		name           string
		override       *bool
		wantDailyLimit int
		wantFree       int
	}{
		{name: "explicitly closed", override: boolPtr(false), wantDailyLimit: 0, wantFree: 0},
		{name: "explicitly open", override: boolPtr(true), wantDailyLimit: 40, wantFree: 40},
		{name: "no override", wantDailyLimit: 40, wantFree: 40},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`DELETE FROM restaurant_days WHERE restaurant_id = ? AND date = ?`, restaurantID, date); err != nil {
				t.Fatalf("clear override: %v", err)
			}
			if tc.override != nil {
				if _, err := db.Exec(`INSERT INTO restaurant_days (restaurant_id, date, is_open) VALUES (?, ?, ?)`, restaurantID, date, *tc.override); err != nil {
					t.Fatalf("insert override: %v", err)
				}
			}

			availability, err := srv.buildMonthAvailability(context.Background(), restaurantID, testDate.Year(), int(testDate.Month()))
			if err != nil {
				t.Fatalf("build availability: %v", err)
			}
			got := availability[date]
			if got["dailyLimit"] != tc.wantDailyLimit || got["freeBookingSeats"] != tc.wantFree {
				t.Fatalf("availability[%s] = %#v; want dailyLimit=%d freeBookingSeats=%d", date, got, tc.wantDailyLimit, tc.wantFree)
			}
		})
	}
}

func TestHandleBOConfigDaySetRangePersistsEveryDate(t *testing.T) {
	db := testDB(t)
	t.Cleanup(func() { _ = db.Close() })
	srv := newTestServer(t, db)

	const restaurantID = 1
	dates := []string{"2099-02-10", "2099-02-11", "2099-02-12"}
	for _, date := range dates {
		cleanupDayAvailability(t, db, restaurantID, date)
	}
	t.Cleanup(func() {
		for _, date := range dates {
			cleanupDayAvailability(t, db, restaurantID, date)
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/api/admin/config/day", strings.NewReader(`{"dates":["2099-02-10","2099-02-11","2099-02-12"],"rangeDates":true,"isOpen":false}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: restaurantID}))
	rec := httptest.NewRecorder()
	srv.handleBOConfigDaySet(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Success bool     `json:"success"`
		Dates   []string `json:"dates"`
		IsOpen  bool     `json:"isOpen"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.IsOpen || len(response.Dates) != len(dates) {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}

	for _, date := range dates {
		var count, isOpen int
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(is_open), -1) FROM restaurant_days WHERE restaurant_id = ? AND date = ?`, restaurantID, date).Scan(&count, &isOpen); err != nil {
			t.Fatalf("read %s: %v", date, err)
		}
		if count != 1 || isOpen != 0 {
			t.Fatalf("restaurant_days[%s] count=%d is_open=%d; want one closed row", date, count, isOpen)
		}
	}
}

func cleanupDayAvailability(t *testing.T, db *sql.DB, restaurantID int, date string) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM restaurant_days WHERE restaurant_id = ? AND date = ?`, restaurantID, date); err != nil {
		t.Fatalf("delete day override: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?`, restaurantID, date); err != nil {
		t.Fatalf("delete daily limit: %v", err)
	}
}

func boolPtr(v bool) *bool { return &v }
