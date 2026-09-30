package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// posCashDayBillingSeed builds an open day with a paid table split across
// tenders (and a partial card refund), a table still open, a paid takeaway, a
// voided ticket and a cancelled visit that must be ignored.
func posCashDayBillingSeed(t *testing.T, s *Server) {
	t.Helper()
	statements := []string{
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(6,1,2,'Mesa 2',2,1,1)`,
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents,opened_at) VALUES(910,1,'2024-04-10','OPEN',7,0,'2024-04-10 09:00:00')`,
		// Mesa 1: closed, one ticket 6000 paid 4000 cash + 2000 card (+300 tip), 500 refunded by card.
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(60,1,910,'DINE_IN',5,'2024-04-10','LUNCH',2,'CLOSED',7,'b60','2024-04-10 13:00:00','2024-04-10 14:00:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(70,1,60,'B-1','bt70',6000,6000,500,'PARTIALLY_REFUNDED',7)`,
		`INSERT INTO pos_payments(restaurant_id,ticket_id,method,amount_cents,tip_cents,idempotency_key,received_by) VALUES(1,70,'CASH',4000,0,'bp1',7)`,
		`INSERT INTO pos_payments(restaurant_id,ticket_id,method,amount_cents,tip_cents,provider,provider_reference,idempotency_key,received_by) VALUES(1,70,'CARD',2000,300,'STANDALONE','ref-1','bp2',7)`,
		`INSERT INTO pos_refunds(restaurant_id,ticket_id,amount_cents,reason,payment_method,idempotency_key,created_by) VALUES(1,70,500,'Error','CARD','br1',7)`,
		// Mesa 2: still open, two tickets (one already paid by bank, one open) + a voided ticket.
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(61,1,910,'DINE_IN',6,'2024-04-10','LUNCH',3,'OPEN',7,'b61','2024-04-10 13:30:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(71,1,61,'B-2','bt71',1500,1500,0,'PAID',7)`,
		`INSERT INTO pos_payments(restaurant_id,ticket_id,method,amount_cents,tip_cents,idempotency_key,received_by) VALUES(1,71,'BANK',1500,0,'bp3',7)`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(72,1,61,'B-3','bt72',2500,2500,0,'OPEN',7)`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(73,1,61,'B-4','bt73',9900,9900,0,'VOIDED',7)`,
		// Takeaway without table, paid in cash.
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(62,1,910,'TAKEAWAY',NULL,'2024-04-10','LUNCH',0,'CLOSED',7,'b62','2024-04-10 12:00:00','2024-04-10 12:10:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(74,1,62,'B-5','bt74',800,800,0,'PAID',7)`,
		`INSERT INTO pos_payments(restaurant_id,ticket_id,method,amount_cents,tip_cents,idempotency_key,received_by) VALUES(1,74,'CASH',800,0,'bp4',7)`,
		// Cancelled visit: never counts.
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(63,1,910,'DINE_IN',5,'2024-04-10','DINNER',2,'CANCELLED',7,'b63','2024-04-10 20:00:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(75,1,63,'B-6','bt75',4000,4000,0,'OPEN',7)`,
		// Another day must not leak in.
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(64,1,'DINE_IN',5,'2024-04-11','LUNCH',2,'OPEN',7,'b64','2024-04-11 13:00:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(76,1,64,'B-7','bt76',7777,7777,0,'OPEN',7)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestPOSCashDayBillingSummary(t *testing.T) {
	_, s := posCashDayTestDB(t)
	posCashDayBillingSeed(t, s)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayBilling(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/2024-04-10/billing", "", map[string]string{"date": "2024-04-10"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("billing: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	num := func(key string) float64 { v, _ := body[key].(float64); return v }

	// Closed: (6000-500) + 1500 + 800 = 7800. Open: 2500. Void + cancelled + other day excluded.
	if num("closedCents") != 7800 || num("openCents") != 2500 || num("totalCents") != 10300 {
		t.Fatalf("unexpected totals: %s", recorder.Body.String())
	}
	if num("openTickets") != 1 || num("closedTickets") != 3 || num("openTables") != 1 {
		t.Fatalf("unexpected counts: %s", recorder.Body.String())
	}
	byMethod, _ := body["byMethod"].(map[string]any)
	want := map[string]float64{"CASH": 4800, "CARD": 1500, "BANK": 1500, "OTHER": 0}
	var sum float64
	for method, cents := range want {
		if byMethod[method] != cents {
			t.Fatalf("%s: want %v got %v (%s)", method, cents, byMethod[method], recorder.Body.String())
		}
		sum += cents
	}
	// The split by method reconciles with the closed amount.
	if sum != num("closedCents") {
		t.Fatalf("methods (%v) must add up to closed (%v)", sum, num("closedCents"))
	}
	if num("tipsCents") != 300 {
		t.Fatalf("tips must be reported apart, got %v", body["tipsCents"])
	}

	tables, _ := body["tables"].([]any)
	if len(tables) != 3 {
		t.Fatalf("expected Mesa 1, Mesa 2 and the takeaway group, got %d: %s", len(tables), recorder.Body.String())
	}
	mesa1, _ := tables[0].(map[string]any)
	mesa2, _ := tables[1].(map[string]any)
	takeaway, _ := tables[2].(map[string]any)
	if mesa1["tableName"] != "Mesa 1" || mesa1["open"] != false || mesa1["totalCents"].(float64) != 5500 {
		t.Fatalf("Mesa 1 wrong: %v", mesa1)
	}
	if mesa2["tableName"] != "Mesa 2" || mesa2["open"] != true || mesa2["openCents"].(float64) != 2500 || mesa2["closedCents"].(float64) != 1500 || mesa2["totalCents"].(float64) != 4000 {
		t.Fatalf("Mesa 2 wrong: %v", mesa2)
	}
	if takeaway["tableId"] != nil || takeaway["channel"] != "TAKEAWAY" || takeaway["totalCents"].(float64) != 800 {
		t.Fatalf("takeaway wrong: %v", takeaway)
	}
}

func TestPOSCashDayBillingEmptyDayAndBadDate(t *testing.T) {
	_, s := posCashDayTestDB(t)
	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayBilling(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/2024-01-01/billing", "", map[string]string{"date": "2024-01-01"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("empty day: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	if body["totalCents"].(float64) != 0 {
		t.Fatalf("empty day must be zero: %v", body)
	}
	if tables, _ := body["tables"].([]any); tables == nil || len(tables) != 0 {
		t.Fatalf("tables must be an empty array, not null: %s", recorder.Body.String())
	}
	byMethod, _ := body["byMethod"].(map[string]any)
	if len(byMethod) != 4 {
		t.Fatalf("all four methods must be present: %v", byMethod)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayBilling(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/nope/billing", "", map[string]string{"date": "nope"}))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad date: expected 400, got %d", recorder.Code)
	}
}
