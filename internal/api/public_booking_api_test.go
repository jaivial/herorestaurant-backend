package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// Tests for public booking JSON API handlers
// ---------------------------------------------------------------------------

func TestPublicBookingGetInvalidID(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/api/public/booking?id=abc", nil)
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingGet(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != false {
		t.Error("expected success=false")
	}
}

func TestPublicBookingGetMissingID(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/api/public/booking", nil)
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingGet(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPublicBookingConfirmInvalidBody(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/api/public/booking/confirm", bytes.NewBufferString(`{}`))
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingConfirm(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["success"] != false {
		t.Error("expected success=false")
	}
}

func TestPublicBookingCancelInvalidBody(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/api/public/booking/cancel", bytes.NewBufferString(`{}`))
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingCancel(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPublicBookingRiceInvalidBody(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/api/public/booking/rice", bytes.NewBufferString(`{}`))
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingRice(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPublicBookingRiceMissingType(t *testing.T) {
	s := &Server{}
	body := `{"id": 1, "riceType": "", "servings": 2}`
	r := httptest.NewRequest("POST", "/api/public/booking/rice", bytes.NewBufferString(body))
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingRice(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPublicBookingRiceInvalidServings(t *testing.T) {
	s := &Server{}
	body := `{"id": 1, "riceType": "Arroz negro", "servings": 0}`
	r := httptest.NewRequest("POST", "/api/public/booking/rice", bytes.NewBufferString(body))
	r = r.WithContext(withRestaurantID(r.Context(), 1))
	w := httptest.NewRecorder()
	s.handlePublicBookingRice(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestPublicBookingPoliciesNoRestaurant(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/api/public/booking-policies", nil)
	w := httptest.NewRecorder()
	s.handlePublicBookingPolicies(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestPublicBookingPoliciesContent(t *testing.T) {
	// Test the static policies content directly (no DB needed).
	if BookingPoliciesHTML == "" {
		t.Error("expected non-empty BookingPoliciesHTML")
	}
	if !bytes.Contains([]byte(BookingPoliciesHTML), []byte("No Asistencia")) {
		t.Error("policies should mention No-Show policy")
	}
	if !bytes.Contains([]byte(BookingPoliciesHTML), []byte("Arroces")) {
		t.Error("policies should mention Arroces section")
	}
}

func TestPublicBookingToResponse(t *testing.T) {
	resp := publicBookingToResponse(&publicBooking{
		ID:              123,
		ReservationDate: "2026-05-15",
		ReservationTime: "14:00:00",
		PartySize:       4,
		Children:        1,
		CustomerName:    "Juan García",
		Commentary:      nullSQLStr("Mesa tranquila"),
		BabyStrollers:   sql.NullInt64{Int64: 1, Valid: true},
		HighChairs:      sql.NullInt64{Int64: 2, Valid: true},
		PreferredFloor:  sql.NullInt64{Int64: 2, Valid: true},
		TableNumber:     nullSQLStr("12"),
		Status:          nullSQLStr("confirmed"),
	})
	if resp.ID != 123 {
		t.Errorf("expected ID 123, got %d", resp.ID)
	}
	if resp.ReservationDate != "15/05/2026" {
		t.Errorf("expected formatted date, got %s", resp.ReservationDate)
	}
	if resp.ReservationTime != "14:00" {
		t.Errorf("expected formatted time, got %s", resp.ReservationTime)
	}
	if resp.PartySize != 4 {
		t.Errorf("expected party size 4, got %d", resp.PartySize)
	}
	if resp.CustomerName != "Juan García" {
		t.Errorf("expected customer name, got %s", resp.CustomerName)
	}
	if resp.Adults != 3 || resp.Children != 1 || resp.BabyStrollers != 1 || resp.HighChairs != 2 {
		t.Errorf("unexpected party details: %+v", resp)
	}
	if resp.FloorDisplay != "Planta 2" || resp.TableNumber != "12" || resp.Commentary != "Mesa tranquila" {
		t.Errorf("unexpected booking details: %+v", resp)
	}
	if !resp.IsConfirmed {
		t.Error("expected IsConfirmed=true for status=confirmed")
	}
}

func TestPublicBookingToResponseArroz(t *testing.T) {
	resp := publicBookingToResponse(&publicBooking{
		ID:              456,
		ReservationDate: "2026-06-01",
		ReservationTime: "21:30:00",
		PartySize:       6,
		CustomerName:    "María López",
		ArrozType:       nullSQLStr(`["Arroz negro"]`),
		ArrozServings:   nullSQLStr(`[4]`),
	})
	if resp.ArrozDisplay != "Arroz negro x 4" {
		t.Errorf("expected arroz display, got %s", resp.ArrozDisplay)
	}
}

// Helper to create sql.NullString for tests
func nullSQLStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
