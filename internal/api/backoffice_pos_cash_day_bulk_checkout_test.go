package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// posBulkCheckoutSeed opens a cash day + shift for 2024-03-07 and seeds two
// open DINE_IN visits, each with one open ticket and one active 250c line, so
// the bulk sweep has something to close.
func posBulkCheckoutSeed(t *testing.T, db *sql.DB, s *Server) (dayID int64) {
	t.Helper()
	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days", `{"date":"2024-03-07","openingCashCents":0}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open cash day: got %d %s", recorder.Code, recorder.Body.String())
	}
	day, _ := decodeCashDayBody(t, recorder)["cashDay"].(map[string]any)
	dayID = int64(day["id"].(float64))

	recorder = httptest.NewRecorder()
	s.handleBOPOSShiftOpen(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/shifts/open", `{"terminalKey":"caja-1","openingCashCents":0}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open shift: got %d %s", recorder.Code, recorder.Body.String())
	}
	shiftID := int64(decodeCashDayBody(t, recorder)["id"].(float64))

	statements := []string{
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(6,1,'2','Mesa 2',4,1,1)`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(140,1,` + strconv.FormatInt(dayID, 10) + `,'DINE_IN',5,'2024-03-07','DINNER',2,'OPEN',7,'bv140','2024-03-07 21:00:00')`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(141,1,` + strconv.FormatInt(dayID, 10) + `,'DINE_IN',6,'2024-03-07','DINNER',2,'OPEN',7,'bv141','2024-03-07 21:05:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,shift_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,status,opened_by) VALUES(150,1,140,` + strconv.FormatInt(shiftID, 10) + `,'TPV-150','t150',250,250,'OPEN',7)`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,shift_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,status,opened_by) VALUES(151,1,141,` + strconv.FormatInt(shiftID, 10) + `,'TPV-151','t151',250,250,'OPEN',7)`,
		`INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,status,idempotency_key,created_by) VALUES(160,1,150,30,'Agua',1,250,0,0,250,'ACTIVE','l160',7)`,
		`INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,status,idempotency_key,created_by) VALUES(161,1,151,30,'Agua',1,250,0,0,250,'ACTIVE','l161',7)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return dayID
}

func TestPOSCashDayBulkCheckoutPaysAndClosesAllOpenTickets(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posBulkCheckoutSeed(t, db, s)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayBulkCheckout(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/2024-03-07/bulk-checkout", `{"paymentMethod":"CASH","idempotencyKey":"bulk-1"}`, map[string]string{"date": "2024-03-07"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bulk checkout: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	if body["closedTickets"].(float64) != 2 {
		t.Fatalf("expected 2 closed tickets, got %v", body["closedTickets"])
	}
	if body["closedVisits"].(float64) != 2 {
		t.Fatalf("expected 2 closed visits, got %v", body["closedVisits"])
	}
	if body["totalGrossCents"].(float64) != 500 {
		t.Fatalf("expected 500 total, got %v", body["totalGrossCents"])
	}
	byMethod, _ := body["byMethod"].(map[string]any)
	if byMethod["CASH"].(float64) != 500 {
		t.Fatalf("expected 500 CASH, got %v", byMethod["CASH"])
	}

	for _, id := range []int64{150, 151} {
		var status string
		if err := db.QueryRow(`SELECT status FROM pos_tickets WHERE id=?`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "PAID" {
			t.Fatalf("ticket %d expected PAID, got %s", id, status)
		}
	}
	for _, id := range []int64{140, 141} {
		var status string
		if err := db.QueryRow(`SELECT status FROM pos_visits WHERE id=?`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "CLOSED" {
			t.Fatalf("visit %d expected CLOSED, got %s", id, status)
		}
	}
	var paymentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_payments WHERE restaurant_id=1`).Scan(&paymentCount); err != nil {
		t.Fatal(err)
	}
	if paymentCount != 2 {
		t.Fatalf("expected 2 payments, got %d", paymentCount)
	}

	// Idempotent replay: no open tickets remain, so the sweep closes zero.
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayBulkCheckout(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/2024-03-07/bulk-checkout", `{"paymentMethod":"CASH","idempotencyKey":"bulk-1"}`, map[string]string{"date": "2024-03-07"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("replay: got %d %s", recorder.Code, recorder.Body.String())
	}
	if decodeCashDayBody(t, recorder)["closedTickets"].(float64) != 0 {
		t.Fatalf("replay expected 0 closed, got %v", recorder.Body.String())
	}
}

// Once the day is Z-closed, bulk checkout must refuse exactly the way a single
// checkout does: a closed day is a signed accounting document.
func TestPOSCashDayBulkCheckoutRefusesOnClosedDay(t *testing.T) {
	db, s := posCashDayTestDB(t)
	dayID := posBulkCheckoutSeed(t, db, s)

	// All open tickets are swept first so the day can actually close.
	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayBulkCheckout(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/2024-03-07/bulk-checkout", `{"paymentMethod":"CASH","idempotencyKey":"bulk-2"}`, map[string]string{"date": "2024-03-07"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bulk before close: got %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/"+strconv.FormatInt(dayID, 10)+"/close", `{"countedCashCents":500}`, map[string]string{"id": strconv.FormatInt(dayID, 10)}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("close day: got %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayBulkCheckout(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/2024-03-07/bulk-checkout", `{"paymentMethod":"CASH","idempotencyKey":"bulk-3"}`, map[string]string{"date": "2024-03-07"}))
	if recorder.Code != http.StatusConflict || decodeCashDayBody(t, recorder)["code"] != "CASH_DAY_CLOSED" {
		t.Fatalf("expected 409 CASH_DAY_CLOSED, got %d %s", recorder.Code, recorder.Body.String())
	}
}
