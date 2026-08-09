package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

func TestPOSValidBusinessDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2024-03-07", "2024-03-07", true},
		{"  2024-03-07  ", "2024-03-07", true},
		{"", "", false},
		{"2024-3-7", "", false},
		{"2024-02-31", "", false},
		{"2024-03-07T10:00:00Z", "", false},
		{"' OR 1=1 --", "", false},
	}
	for _, tc := range cases {
		got, ok := posValidBusinessDate(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("posValidBusinessDate(%q) = %q,%v want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// posCashDayTestDB prepares a tenant with POS enabled and no cash day yet.
// Set POS_CASH_DAY_TEST_MYSQL_DSN to enable these tests.
func posCashDayTestDB(t *testing.T) (*sql.DB, *Server) {
	t.Helper()
	dsn := os.Getenv("POS_CASH_DAY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_CASH_DAY_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	statements := []string{
		`SET FOREIGN_KEY_CHECKS=0`,
		`DELETE FROM pos_cash_closures`, `DELETE FROM pos_cash_movements`, `DELETE FROM pos_cash_days`,
		`DELETE FROM stock_affluence_daily`, `DELETE FROM pos_cover_adjustments`, `DELETE FROM pos_refund_lines`, `DELETE FROM pos_refunds`,
		`DELETE FROM pos_payments`, `DELETE FROM pos_ticket_line_tags`, `DELETE FROM pos_ticket_tags`,
		`DELETE FROM pos_tags`, `DELETE FROM pos_kitchen_dispatch_lines`, `DELETE FROM pos_kitchen_dispatches`,
		`DELETE FROM pos_kitchen_routes`, `DELETE FROM pos_kitchen_stations`,
		`DELETE FROM pos_ticket_line_stock`, `DELETE FROM pos_ticket_adjustments`, `DELETE FROM pos_ticket_lines`,
		`DELETE FROM pos_tickets`, `DELETE FROM pos_visits`, `DELETE FROM pos_shifts`,
		`DELETE FROM pos_products`, `DELETE FROM pos_settings`,
		`DELETE FROM restaurant_tables`, `DELETE FROM restaurants`, `DELETE FROM bo_users`,
		`INSERT INTO restaurants(id,slug,name) VALUES(1,'cash-day-test','Cash Day Test')`,
		`INSERT INTO bo_users(id,email,name,password_hash) VALUES(7,'cashday@test.local','Cash Day','x')`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(5,1,1,'Mesa 1',4,0,1)`,
		`INSERT INTO pos_settings(restaurant_id,is_enabled,stock_mode,covers_mode,timezone,business_day_cutoff) VALUES(1,1,'OFF','MANUAL','Europe/Madrid','05:00')`,
		`INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(30,1,'Agua',250,1)`,
		`SET FOREIGN_KEY_CHECKS=1`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return db, NewServer(db, config.Config{})
}

func posCashDayRequest(method, target, body string, params map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	for key, value := range params {
		routeCtx.URLParams.Add(key, value)
	}
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	return request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
}

func decodeCashDayBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json %q: %v", recorder.Body.String(), err)
	}
	return out
}

func TestPOSCashDayCurrentReportsNoDayThenOpen(t *testing.T) {
	_, s := posCashDayTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayCurrent(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/current?date=2024-03-07", "", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("current: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	if body["cashDay"] != nil {
		t.Fatalf("expected no cash day, got %v", body["cashDay"])
	}
	if body["date"] != "2024-03-07" {
		t.Fatalf("expected echoed date, got %v", body["date"])
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2024-03-07","openingCashCents":15000}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open: got %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayCurrent(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/current?date=2024-03-07", "", nil))
	body = decodeCashDayBody(t, recorder)
	day, _ := body["cashDay"].(map[string]any)
	if day == nil || day["status"] != "OPEN" || day["openingCashCents"].(float64) != 15000 {
		t.Fatalf("expected open cash day with float, got %v", body["cashDay"])
	}
	if day["openedByName"] != "Cash Day" {
		t.Fatalf("expected resolved opener name, got %v", day["openedByName"])
	}
}

func TestPOSCashDayCurrentRejectsMalformedDate(t *testing.T) {
	_, s := posCashDayTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayCurrent(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/current?date=not-a-date", "", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", recorder.Code, recorder.Body.String())
	}
}

// A mistyped year must not create a day that then blocks every later open as an
// "unclosed previous day".
func TestPOSCashDayOpenRejectsFutureDate(t *testing.T) {
	db, s := posCashDayTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2999-01-01","openingCashCents":0,"force":true}`, nil))
	if recorder.Code != http.StatusBadRequest || decodeCashDayBody(t, recorder)["code"] != "FUTURE_CASH_DAY" {
		t.Fatalf("expected FUTURE_CASH_DAY, got %d %s", recorder.Code, recorder.Body.String())
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_cash_days`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("no cash day may be created, got %d", rows)
	}
}

func TestPOSCashDayOpenBlocksOnUnclosedPreviousDays(t *testing.T) {
	db, s := posCashDayTestDB(t)
	if _, err := db.Exec(`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(900,1,'2024-03-06','OPEN',7,10000)`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2024-03-07","openingCashCents":0}`, nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	if body["code"] != "UNCLOSED_PREVIOUS_DAYS" {
		t.Fatalf("expected UNCLOSED_PREVIOUS_DAYS, got %v", body["code"])
	}
	unclosed, _ := body["unclosedPrevious"].([]any)
	if len(unclosed) != 1 {
		t.Fatalf("expected 1 unclosed day, got %v", body["unclosedPrevious"])
	}
	entry, _ := unclosed[0].(map[string]any)
	if entry["date"] != "2024-03-06" {
		t.Fatalf("expected the earlier day, got %v", entry["date"])
	}
	if _, ok := entry["totalGrossCents"]; !ok {
		t.Fatalf("expected takings on the alert card, got %v", entry)
	}

	// force=true bypasses the guard and flags the day as forced.
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2024-03-07","openingCashCents":0,"force":true}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("forced open: got %d %s", recorder.Code, recorder.Body.String())
	}
	day, _ := decodeCashDayBody(t, recorder)["cashDay"].(map[string]any)
	if day["forcedOpen"] != true {
		t.Fatalf("expected forcedOpen, got %v", day)
	}
}

// Two terminals opening the same day must not both win: the unique key on
// (restaurant_id, business_date) is the arbiter and the loser gets a 409.
// A terminal that claimed its drawer before the day was opened would otherwise
// keep a NULL cash_day_id forever, hiding its movements from the close.
func TestPOSCashDayOpenAdoptsOrphanShifts(t *testing.T) {
	db, s := posCashDayTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSShiftOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/shifts/open", `{"terminalKey":"caja-1","openingCashCents":0}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("shift open: got %d %s", recorder.Code, recorder.Body.String())
	}
	shiftID := int64(decodeCashDayBody(t, recorder)["id"].(float64))
	var linked sql.NullInt64
	if err := db.QueryRow(`SELECT cash_day_id FROM pos_shifts WHERE id=?`, shiftID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked.Valid {
		t.Fatalf("no cash day exists yet, got %v", linked)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"openingCashCents":0}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open: got %d %s", recorder.Code, recorder.Body.String())
	}
	day, _ := decodeCashDayBody(t, recorder)["cashDay"].(map[string]any)
	dayID := int64(day["id"].(float64))
	if err := db.QueryRow(`SELECT cash_day_id FROM pos_shifts WHERE id=?`, shiftID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if !linked.Valid || linked.Int64 != dayID {
		t.Fatalf("orphan shift must be adopted by the new day, got %v want %d", linked, dayID)
	}
}

func TestPOSCashDayOpenIsRaceSafe(t *testing.T) {
	db, s := posCashDayTestDB(t)

	const attempts = 6
	codes := make([]int, attempts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			recorder := httptest.NewRecorder()
			s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2024-03-07","openingCashCents":0}`, nil))
			codes[index] = recorder.Code
		}(i)
	}
	close(start)
	wg.Wait()

	created := 0
	for i, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Fatalf("attempt %d: unexpected status %d", i, code)
		}
	}
	if created != 1 {
		t.Fatalf("expected exactly 1 winner, got %d (%v)", created, codes)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_cash_days WHERE restaurant_id=1 AND business_date='2024-03-07'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("expected 1 cash day row, got %d", rows)
	}
}

func TestPOSCashDayCloseRefusesOpenItemsAndRequiresCount(t *testing.T) {
	db, s := posCashDayTestDB(t)
	setup := []string{
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(900,1,'2024-03-07','OPEN',7,10000)`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(40,1,900,'DINE_IN',5,'2024-03-07','LUNCH',2,'OPEN',7,'visit-a')`,
	}
	for _, statement := range setup {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	// Missing count is rejected before any accounting runs.
	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/900/close", `{}`, map[string]string{"id": "900"}))
	if recorder.Code != http.StatusBadRequest || decodeCashDayBody(t, recorder)["code"] != "COUNTED_CASH_REQUIRED" {
		t.Fatalf("expected COUNTED_CASH_REQUIRED, got %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/900/close", `{"countedCashCents":10000}`, map[string]string{"id": "900"}))
	if recorder.Code != http.StatusConflict || decodeCashDayBody(t, recorder)["code"] != "OPEN_POS_ITEMS" {
		t.Fatalf("expected OPEN_POS_ITEMS, got %d %s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM pos_cash_days WHERE id=900`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" {
		t.Fatalf("day must stay OPEN after a refused close, got %s", status)
	}
}

// Settling an old day must not be held hostage by today's live tables, which is
// the whole point of the unclosed-previous-days flow.
func TestPOSCashDayCloseIgnoresOpenVisitsFromOtherDays(t *testing.T) {
	db, s := posCashDayTestDB(t)
	setup := []string{
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(900,1,'2024-03-07','OPEN',7,10000)`,
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(901,1,'2024-03-08','OPEN',7,10000)`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(41,1,901,'DINE_IN',5,'2024-03-08','LUNCH',2,'OPEN',7,'visit-today')`,
	}
	for _, statement := range setup {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/900/close", `{"countedCashCents":10000}`, map[string]string{"id": "900"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("close of the earlier day: got %d %s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM pos_visits WHERE id=41`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" {
		t.Fatalf("today's visit must be untouched, got %s", status)
	}
}

func TestPOSCashDayCloseWritesZClosureAndClosesShifts(t *testing.T) {
	db, s := posCashDayTestDB(t)

	// The day is opened through the real handler so its business date comes from
	// the cutoff settings rather than the database server's clock.
	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"openingCashCents":10000}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open: got %d %s", recorder.Code, recorder.Body.String())
	}
	opened, _ := decodeCashDayBody(t, recorder)["cashDay"].(map[string]any)
	dayID := int64(opened["id"].(float64))
	date := opened["date"].(string)
	closePath := "/admin/pos/cash-days/" + strconv.FormatInt(dayID, 10) + "/close"
	closeParams := map[string]string{"id": strconv.FormatInt(dayID, 10)}

	setup := []string{
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(40,1,?,'DINE_IN',5,?,'LUNCH',2,'CLOSED',7,'visit-a')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,status,opened_by) VALUES(50,1,40,'TPV-1','ticket-a',5000,5000,'PAID',7)`,
		`INSERT INTO pos_payments(id,restaurant_id,ticket_id,method,amount_cents,tip_cents,status,idempotency_key,received_by) VALUES(70,1,50,'CASH',5000,0,'CAPTURED','pay-a',7)`,
	}
	if _, err := db.Exec(setup[0], dayID, date); err != nil {
		t.Fatal(err)
	}
	for _, statement := range setup[1:] {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	// The shift is opened through the real handler, not hand-seeded, so this
	// test fails if the application ever stops linking a shift to its cash day.
	recorder = httptest.NewRecorder()
	s.handleBOPOSShiftOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/shifts/open", `{"terminalKey":"caja-1","openingCashCents":0}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("shift open: got %d %s", recorder.Code, recorder.Body.String())
	}
	shiftID := int64(decodeCashDayBody(t, recorder)["id"].(float64))
	var linkedDay sql.NullInt64
	if err := db.QueryRow(`SELECT cash_day_id FROM pos_shifts WHERE id=?`, shiftID).Scan(&linkedDay); err != nil {
		t.Fatal(err)
	}
	if !linkedDay.Valid || linkedDay.Int64 != dayID {
		t.Fatalf("shift must be linked to the open cash day, got %v", linkedDay)
	}

	// A payout only reaches the day close through that link.
	if _, err := db.Exec(`INSERT INTO pos_cash_movements(restaurant_id,shift_id,terminal_key,movement_type,amount_cents,reason,idempotency_key,created_by) VALUES(1,?,'caja-1','OUT',2000,'Proveedor','mov-a',7)`, shiftID); err != nil {
		t.Fatal(err)
	}

	// Expected cash is opening 10000 + cash sales 5000 - payout 2000 = 13000.
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, closePath, `{"countedCashCents":14000}`, closeParams))
	if recorder.Code != http.StatusBadRequest || decodeCashDayBody(t, recorder)["code"] != "DISCREPANCY_REASON_REQUIRED" {
		t.Fatalf("expected DISCREPANCY_REASON_REQUIRED, got %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, closePath, `{"countedCashCents":13000}`, closeParams))
	if recorder.Code != http.StatusOK {
		t.Fatalf("close: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	if body["differenceCents"].(float64) != 0 {
		t.Fatalf("expected no difference, got %v", body["differenceCents"])
	}

	var status, closureType string
	var cashDayID sql.NullInt64
	var closureShiftID sql.NullInt64
	var expected, counted, cashOut int64
	if err := db.QueryRow(`SELECT status FROM pos_cash_days WHERE id=?`, dayID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "CLOSED" {
		t.Fatalf("expected CLOSED day, got %s", status)
	}
	if err := db.QueryRow(`SELECT closure_type,cash_day_id,shift_id,expected_cash_cents,counted_cash_cents,cash_out_cents FROM pos_cash_closures WHERE restaurant_id=1`).Scan(&closureType, &cashDayID, &closureShiftID, &expected, &counted, &cashOut); err != nil {
		t.Fatal(err)
	}
	if closureType != "Z" || !cashDayID.Valid || cashDayID.Int64 != dayID || closureShiftID.Valid {
		t.Fatalf("expected day-scoped Z closure, got %s day=%v shift=%v", closureType, cashDayID, closureShiftID)
	}
	if cashOut != 2000 {
		t.Fatalf("payout must reach the day close through the shift link, got %d", cashOut)
	}
	if expected != 13000 || counted != 13000 {
		t.Fatalf("expected 13000/13000, got %d/%d", expected, counted)
	}
	if err := db.QueryRow(`SELECT status FROM pos_shifts WHERE id=?`, shiftID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "CLOSED" {
		t.Fatalf("shift under a closed day must be closed, got %s", status)
	}

	// Re-closing is refused rather than writing a second Z.
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, closePath, `{"countedCashCents":13000}`, closeParams))
	if recorder.Code != http.StatusConflict || decodeCashDayBody(t, recorder)["code"] != "CASH_DAY_ALREADY_CLOSED" {
		t.Fatalf("expected CASH_DAY_ALREADY_CLOSED, got %d %s", recorder.Code, recorder.Body.String())
	}
	var closures int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_cash_closures WHERE restaurant_id=1`).Scan(&closures); err != nil {
		t.Fatal(err)
	}
	if closures != 1 {
		t.Fatalf("expected exactly 1 closure, got %d", closures)
	}
}
