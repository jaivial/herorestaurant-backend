package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Integration tests: require TEST_DB_DSN (see testDB in public_menus_test.go).

func seedBotRestaurant(t *testing.T, s *Server) (int, func()) {
	t.Helper()
	rid, cleanup := seedRestaurant(t, s.db, "bot-test-"+time.Now().Format("150405.000"))
	return rid, cleanup
}

func TestBotToolCreateAndCancelBooking_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	defer func() { _, _ = db.Exec(`DELETE FROM bookings WHERE restaurant_id = ?`, rid) }()
	defer func() { _, _ = db.Exec(`DELETE FROM cancelled_bookings WHERE restaurant_id = ?`, rid) }()

	msg := botWebhookMessage{Sender: "34612345678", PushName: "Jaime Test"}
	future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	// Refuse without confirmed=true.
	out, err := s.botToolCreateBooking(context.Background(), rid, msg, json.RawMessage(
		`{"date":"`+future+`","time":"14:00","people":4,"confirmed":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "confirmed=true") {
		t.Errorf("expected confirmation error, got %s", out)
	}

	// Create.
	out, err = s.botToolCreateBooking(context.Background(), rid, msg, json.RawMessage(
		`{"date":"`+future+`","time":"14:00","people":4,"rice_type":"Paella Valenciana","rice_servings":4,"confirmed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Created   bool  `json:"created"`
		BookingID int64 `json:"booking_id"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || !created.Created || created.BookingID <= 0 {
		t.Fatalf("create result = %s err=%v", out, err)
	}

	// List: must find the booking by sender phone.
	out, err = s.botToolGetBookings(context.Background(), rid, msg.Sender)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Count    int             `json:"count"`
		Bookings []botBookingRow `json:"bookings"`
	}
	_ = json.Unmarshal([]byte(out), &listed)
	if listed.Count != 1 || listed.Bookings[0].ID != created.BookingID {
		t.Fatalf("get_bookings = %s", out)
	}

	// Cancel with wrong phone: refused.
	out, _ = s.botToolCancelBooking(context.Background(), rid, "34699999999", json.RawMessage(
		`{"booking_id":`+jsonInt(created.BookingID)+`,"confirmed":true}`))
	if !strings.Contains(out, "no encontrada") {
		t.Errorf("expected ownership rejection, got %s", out)
	}

	// Cancel with correct phone.
	out, err = s.botToolCancelBooking(context.Background(), rid, msg.Sender, json.RawMessage(
		`{"booking_id":`+jsonInt(created.BookingID)+`,"confirmed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"cancelled":true`) {
		t.Errorf("cancel result = %s", out)
	}

	// Verify moved to cancelled_bookings.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cancelled_bookings WHERE restaurant_id = ? AND booking_id = ? AND cancelled_by = 'whatsapp'`, rid, created.BookingID).Scan(&n); err != nil || n != 1 {
		t.Errorf("cancelled_bookings rows = %d err=%v", n, err)
	}
}

func TestBotToolSchedule_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	defer func() {
		_, _ = db.Exec(`DELETE FROM restaurant_reservation_defaults WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM openinghours WHERE restaurant_id = ?`, rid)
	}()

	ctx := context.Background()

	// Seed defaults: open Fri/Sat/Sun, morning + night hours, both mode.
	if err := s.upsertReservationDefaults(ctx, rid, reservationDefaults{
		OpeningMode:  "both",
		MorningHours: []string{"13:30", "14:00"},
		NightHours:   []string{"21:00", "21:30"},
		WeekdayOpen: map[string]bool{
			"monday": false, "tuesday": false, "wednesday": false, "thursday": false,
			"friday": true, "saturday": true, "sunday": true,
		},
		DailyLimit:       60,
		MesasDeDosLimit:  "10",
		MesasDeTresLimit: "10",
	}); err != nil {
		t.Fatalf("seed defaults: %v", err)
	}

	// get_default_schedule: open days + hours.
	out, err := s.botToolDefaultSchedule(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	var def struct {
		OpeningMode  string          `json:"opening_mode"`
		MorningHours []string        `json:"morning_hours"`
		NightHours   []string        `json:"night_hours"`
		DailyLimit   int             `json:"daily_limit"`
		WeekdayOpen  map[string]bool `json:"weekday_open"`
		OpenDays     []string        `json:"open_days"`
	}
	if err := json.Unmarshal([]byte(out), &def); err != nil {
		t.Fatal(err)
	}
	if def.OpeningMode != "both" || def.DailyLimit != 60 || len(def.MorningHours) != 2 || len(def.NightHours) != 2 {
		t.Errorf("default schedule = %s", out)
	}
	if !def.WeekdayOpen["friday"] || def.WeekdayOpen["monday"] {
		t.Errorf("weekday_open = %v", def.WeekdayOpen)
	}
	if len(def.OpenDays) != 3 {
		t.Errorf("open_days = %v", def.OpenDays)
	}

	// Find next Monday (closed weekday) and next Saturday (open weekday).
	nextWeekday := func(target time.Weekday) string {
		d := time.Now().AddDate(0, 0, 1)
		for i := 0; i < 8; i++ {
			if d.Weekday() == target {
				return d.Format("2006-01-02")
			}
			d = d.AddDate(0, 0, 1)
		}
		return d.Format("2006-01-02")
	}
	monday := nextWeekday(time.Monday)
	saturday := nextWeekday(time.Saturday)

	// get_day_schedule on a closed weekday (Monday) with no override → closed.
	out, _ = s.botToolDaySchedule(ctx, rid, json.RawMessage(`{"date":"`+monday+`"}`))
	var mon struct {
		WeekdayOpen bool `json:"weekday_open"`
		HasOverride bool `json:"has_override"`
		Open        bool `json:"open"`
	}
	_ = json.Unmarshal([]byte(out), &mon)
	if mon.WeekdayOpen || mon.HasOverride || mon.Open {
		t.Errorf("monday schedule = %s", out)
	}

	// get_day_schedule on an open weekday (Saturday) → open, no override.
	out, _ = s.botToolDaySchedule(ctx, rid, json.RawMessage(`{"date":"`+saturday+`"}`))
	var sat struct {
		WeekdayOpen bool `json:"weekday_open"`
		HasOverride bool `json:"has_override"`
		Open        bool `json:"open"`
	}
	_ = json.Unmarshal([]byte(out), &sat)
	if !sat.WeekdayOpen || sat.HasOverride || !sat.Open {
		t.Errorf("saturday schedule = %s", out)
	}

	// Individual override: open the closed Monday with special hours.
	if _, err := db.Exec(`
		INSERT INTO openinghours (restaurant_id, dateselected, hoursarray, opening_mode)
		VALUES (?, ?, ?, 'morning')
	`, rid, monday, `["12:00","12:30","13:00"]`); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	out, _ = s.botToolDaySchedule(ctx, rid, json.RawMessage(`{"date":"`+monday+`"}`))
	var ovr struct {
		HasOverride  bool     `json:"has_override"`
		Open         bool     `json:"open"`
		OpeningMode  string   `json:"opening_mode"`
		MorningHours []string `json:"morning_hours"`
	}
	_ = json.Unmarshal([]byte(out), &ovr)
	if !ovr.HasOverride || !ovr.Open || ovr.OpeningMode != "morning" || len(ovr.MorningHours) != 3 {
		t.Errorf("monday override schedule = %s", out)
	}
}

func TestBotToolModifyBooking_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	defer func() { _, _ = db.Exec(`DELETE FROM bookings WHERE restaurant_id = ?`, rid) }()

	msg := botWebhookMessage{Sender: "34612345678", PushName: "Jaime Test"}
	future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	out, err := s.botToolCreateBooking(context.Background(), rid, msg, json.RawMessage(
		`{"date":"`+future+`","time":"14:00","people":2,"confirmed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		BookingID int64 `json:"booking_id"`
	}
	_ = json.Unmarshal([]byte(out), &created)

	out, err = s.botToolModifyBooking(context.Background(), rid, msg.Sender, json.RawMessage(
		`{"booking_id":`+jsonInt(created.BookingID)+`,"people":6,"time":"14:30","confirmed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"modified":true`) {
		t.Fatalf("modify result = %s", out)
	}

	var people int
	var timeStr string
	if err := db.QueryRow(`SELECT party_size, TIME_FORMAT(reservation_time, '%H:%i') FROM bookings WHERE id = ?`, created.BookingID).Scan(&people, &timeStr); err != nil {
		t.Fatal(err)
	}
	if people != 6 || timeStr != "14:30" {
		t.Errorf("people=%d time=%s", people, timeStr)
	}
}

func TestBotHistoryRoundTrip_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	s.cfg.BotHistoryLimit = 20
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	defer func() {
		_, _ = db.Exec(`DELETE FROM whatsapp_bot_messages WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM whatsapp_bot_sessions WHERE restaurant_id = ?`, rid)
	}()

	ctx := context.Background()
	phone := "34600000042"
	s.botSaveMessage(ctx, rid, phone, "user", "hola", "")
	s.botSaveMessage(ctx, rid, phone, "assistant", "¡Hola! ¿En qué te ayudo?", "")
	s.botSaveMessage(ctx, rid, phone, "user", "quiero reservar", "")
	s.botSaveMessage(ctx, rid, phone, "assistant", "¿Para qué día?", "")
	s.botTouchSession(ctx, rid, phone, "Jaime")

	history := s.botLoadHistory(ctx, rid, phone)
	if len(history) != 4 {
		t.Fatalf("history len = %d", len(history))
	}
	if history[0].Role != "user" || history[3].Role != "assistant" {
		t.Errorf("roles = %s..%s", history[0].Role, history[3].Role)
	}
	if history[3].Content[0].Text != "¿Para qué día?" {
		t.Errorf("last = %q", history[3].Content[0].Text)
	}
}

func jsonInt(v int64) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
