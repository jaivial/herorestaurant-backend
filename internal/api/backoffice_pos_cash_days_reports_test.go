package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPOSDateRange(t *testing.T) {
	dates, err := posDateRange("2024-03-01", "2024-03-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 3 || dates[0] != "2024-03-01" || dates[2] != "2024-03-03" {
		t.Fatalf("unexpected expansion: %v", dates)
	}
	if _, err = posDateRange("2024-03-03", "2024-03-01"); err == nil {
		t.Fatal("reversed range must be rejected")
	}
	if _, err = posDateRange("2024-03-01", "2024-12-31"); err == nil {
		t.Fatal("range beyond the cap must be rejected")
	}
	// The cap is inclusive: exactly 92 days is allowed.
	if _, err = posDateRange("2024-03-01", "2024-05-31"); err != nil {
		t.Fatalf("92-day range must be allowed: %v", err)
	}
	for _, bad := range []string{"", "2024-3-1", "not-a-date"} {
		if _, err = posDateRange(bad, "2024-03-03"); err == nil {
			t.Fatalf("malformed %q must be rejected", bad)
		}
	}
}

// posCashDayReportSeed builds two trading days: one closed with two tables and a
// refund, one with activity but no cash day at all.
func posCashDayReportSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(6,1,2,'Mesa 2',2,1,1)`,
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,closed_by,opening_cash_cents,opened_at,closed_at) VALUES(900,1,'2024-03-07','CLOSED',7,7,10000,'2024-03-07 09:00:00','2024-03-07 23:30:00')`,
		// Mesa 1: two visits, the second with two tickets.
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(40,1,900,'DINE_IN',5,'2024-03-07','LUNCH',2,'CLOSED',7,'v40','2024-03-07 13:00:00','2024-03-07 14:00:00')`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(41,1,900,'DINE_IN',5,'2024-03-07','DINNER',3,'CLOSED',7,'v41','2024-03-07 21:00:00','2024-03-07 22:00:00')`,
		// Mesa 2: one visit, one ticket partially refunded.
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(42,1,900,'DINE_IN',6,'2024-03-07','DINNER',4,'CLOSED',7,'v42','2024-03-07 21:30:00','2024-03-07 22:30:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(50,1,40,'TPV-1','t50',3000,3000,0,'PAID',7)`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(51,1,41,'TPV-2','t51',2000,2000,0,'PAID',7)`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(52,1,41,'TPV-3','t52',1000,1000,0,'PAID',7)`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(53,1,42,'TPV-4','t53',5000,5000,1500,'PARTIALLY_REFUNDED',7)`,
		// A voided ticket must not count towards takings.
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(54,1,42,'TPV-5','t54',9900,9900,0,'VOIDED',7)`,
		// Cover adjustment, mirroring the affluence rule.
		`INSERT INTO pos_cover_adjustments(restaurant_id,service_date,service_type,delta_covers,reason,idempotency_key,actor_user_id) VALUES(1,'2024-03-07','DINNER',2,'Terraza','adj-1',7)`,
		// A later day with activity but no cash day row at all.
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(43,1,'DINE_IN',5,'2024-03-08','LUNCH',5,'CLOSED',7,'v43','2024-03-08 13:00:00','2024-03-08 14:00:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,refunded_cents,status,opened_by) VALUES(55,1,43,'TPV-6','t55',7000,7000,0,'PAID',7)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestPOSCashDaysRangeAggregates(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posCashDayReportSeed(t, db)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDaysRange(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days?from=2024-03-05&to=2024-03-10", "", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("range: got %d %s", recorder.Code, recorder.Body.String())
	}
	data, _ := decodeCashDayBody(t, recorder)["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected only the two days with a cash day or activity, got %d: %s", len(data), recorder.Body.String())
	}

	first, _ := data[0].(map[string]any)
	if first["date"] != "2024-03-07" || first["status"] != "CLOSED" {
		t.Fatalf("unexpected first day: %v", first)
	}
	// 3000 + 2000 + 1000 + (5000-1500) = 9500; the VOIDED ticket is excluded.
	if first["totalGrossCents"].(float64) != 9500 {
		t.Fatalf("takings must be net of refunds and exclude voids, got %v", first["totalGrossCents"])
	}
	// Closed dine-in covers 2+3+4 plus the +2 adjustment.
	if first["covers"].(float64) != 11 {
		t.Fatalf("covers must include adjustments, got %v", first["covers"])
	}
	if first["ticketCount"].(float64) != 4 {
		t.Fatalf("ticket count must exclude voids, got %v", first["ticketCount"])
	}
	if first["openedByName"] != "Cash Day" || first["closedByName"] != "Cash Day" {
		t.Fatalf("user names must be resolved, got %v / %v", first["openedByName"], first["closedByName"])
	}

	// A day with activity but no cash day is reported with a null status so the
	// calendar can flag it as never opened.
	second, _ := data[1].(map[string]any)
	if second["date"] != "2024-03-08" || second["status"] != nil {
		t.Fatalf("expected 2024-03-08 with null status, got %v", second)
	}
	if second["totalGrossCents"].(float64) != 7000 || second["covers"].(float64) != 5 {
		t.Fatalf("unexpected totals for the un-opened day: %v", second)
	}
}

func TestPOSCashDaysRangeRejectsBadInput(t *testing.T) {
	_, s := posCashDayTestDB(t)
	for _, query := range []string{
		"/admin/pos/cash-days",
		"/admin/pos/cash-days?from=2024-03-01",
		"/admin/pos/cash-days?from=2024-03-01&to=nope",
		"/admin/pos/cash-days?from=2024-03-10&to=2024-03-01",
		"/admin/pos/cash-days?from=2024-01-01&to=2024-12-31",
	} {
		recorder := httptest.NewRecorder()
		s.handleBOPOSCashDaysRange(recorder, posCashDayRequest(http.MethodGet, query, "", nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d %s", query, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPOSCashDayTablesBreakdown(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posCashDayReportSeed(t, db)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayTables(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/2024-03-07/tables", "", map[string]string{"date": "2024-03-07"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("tables: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	if body["readOnly"] != true {
		t.Fatalf("a CLOSED day must be read-only, got %v", body["readOnly"])
	}
	tables, _ := body["tables"].([]any)
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d: %s", len(tables), recorder.Body.String())
	}

	mesa1, _ := tables[0].(map[string]any)
	if mesa1["tableName"] != "Mesa 1" {
		t.Fatalf("unexpected first table: %v", mesa1)
	}
	visits, _ := mesa1["visits"].([]any)
	if len(visits) != 2 {
		t.Fatalf("Mesa 1 must group both visits, got %d", len(visits))
	}
	secondVisit, _ := visits[1].(map[string]any)
	tickets, _ := secondVisit["tickets"].([]any)
	if len(tickets) != 2 {
		t.Fatalf("the dinner visit must carry both tickets, got %d", len(tickets))
	}
	if secondVisit["totalGrossCents"].(float64) != 3000 {
		t.Fatalf("visit total must sum its tickets, got %v", secondVisit["totalGrossCents"])
	}
	if mesa1["totalGrossCents"].(float64) != 6000 || mesa1["covers"].(float64) != 5 {
		t.Fatalf("Mesa 1 totals wrong: %v", mesa1)
	}

	// Mesa 2 nets the refund and drops the voided ticket from the money, while
	// still listing it so the operator can see it happened.
	mesa2, _ := tables[1].(map[string]any)
	if mesa2["totalGrossCents"].(float64) != 3500 {
		t.Fatalf("Mesa 2 must be net of the refund, got %v", mesa2["totalGrossCents"])
	}
	mesa2Visits, _ := mesa2["visits"].([]any)
	mesa2Visit, _ := mesa2Visits[0].(map[string]any)
	mesa2Tickets, _ := mesa2Visit["tickets"].([]any)
	if len(mesa2Tickets) != 2 {
		t.Fatalf("the voided ticket must still be listed, got %d", len(mesa2Tickets))
	}
}

func TestPOSCashDayTablesReadOnlyAndValidation(t *testing.T) {
	db, s := posCashDayTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayTables(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/nope/tables", "", map[string]string{"date": "nope"}))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", recorder.Code, recorder.Body.String())
	}

	// A date that was never opened is history, so it is read-only too.
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayTables(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/2024-03-07/tables", "", map[string]string{"date": "2024-03-07"}))
	if recorder.Code != http.StatusOK || decodeCashDayBody(t, recorder)["readOnly"] != true {
		t.Fatalf("missing cash day must be read-only, got %d %s", recorder.Code, recorder.Body.String())
	}

	if _, err := db.Exec(`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(901,1,'2024-03-09','OPEN',7,0)`); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	s.handleBOPOSCashDayTables(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/cash-days/2024-03-09/tables", "", map[string]string{"date": "2024-03-09"}))
	if recorder.Code != http.StatusOK || decodeCashDayBody(t, recorder)["readOnly"] != false {
		t.Fatalf("an OPEN day must be writable, got %d %s", recorder.Code, recorder.Body.String())
	}
}
