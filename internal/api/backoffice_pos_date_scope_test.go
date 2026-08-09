package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

// posDateScopeSeed creates a closed visit on a past day and an open visit today,
// so a date filter and the unfiltered default produce visibly different results.
func posDateScopeSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,opening_cash_cents) VALUES(900,1,'2024-03-07','CLOSED',7,0)`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at,closed_at) VALUES(40,1,900,'DINE_IN',5,'2024-03-07','LUNCH',2,'CLOSED',7,'v40','2024-03-07 13:00:00','2024-03-07 14:00:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,status,opened_by) VALUES(50,1,40,'TPV-1','t50',3000,3000,'PAID',7)`,
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(41,1,'DINE_IN',5,CURDATE(),'LUNCH',2,'OPEN',7,'v41')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,status,opened_by) VALUES(51,1,41,'TPV-2','t51',1000,1000,'OPEN',7)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestPOSVisitsListDateScope(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posDateScopeSeed(t, db)

	// Without ?date= the previous behaviour is preserved: every visit, no filter.
	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/visits", "", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unfiltered: got %d %s", recorder.Code, recorder.Body.String())
	}
	visits, _ := decodeCashDayBody(t, recorder)["visits"].([]any)
	if len(visits) != 2 {
		t.Fatalf("unfiltered must return both visits, got %d", len(visits))
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSVisitsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/visits?date=2024-03-07", "", nil))
	visits, _ = decodeCashDayBody(t, recorder)["visits"].([]any)
	if len(visits) != 1 {
		t.Fatalf("date filter must return one visit, got %d", len(visits))
	}
	if visit, _ := visits[0].(map[string]any); visit["serviceDate"] != "2024-03-07" {
		t.Fatalf("wrong visit returned: %v", visits[0])
	}

	// The date filter composes with the existing status filter.
	recorder = httptest.NewRecorder()
	s.handleBOPOSVisitsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/visits?date=2024-03-07&status=OPEN", "", nil))
	visits, _ = decodeCashDayBody(t, recorder)["visits"].([]any)
	if len(visits) != 0 {
		t.Fatalf("no open visit exists on that date, got %d", len(visits))
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSVisitsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/visits?date=07-03-2024", "", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed date must be 400, got %d", recorder.Code)
	}
}

func TestPOSTicketsListDateScope(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posDateScopeSeed(t, db)

	recorder := httptest.NewRecorder()
	s.handleBOPOSTicketsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/tickets", "", nil))
	items, _ := decodeCashDayBody(t, recorder)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("unfiltered must return both tickets, got %d", len(items))
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSTicketsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/tickets?date=2024-03-07", "", nil))
	items, _ = decodeCashDayBody(t, recorder)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("date filter must return one ticket, got %d", len(items))
	}
	if ticket, _ := items[0].(map[string]any); ticket["ticketNumber"] != "TPV-1" {
		t.Fatalf("wrong ticket returned: %v", items[0])
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSTicketsList(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/tickets?date=nope", "", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed date must be 400, got %d", recorder.Code)
	}
}

func TestPOSBootstrapDateScope(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posDateScopeSeed(t, db)

	// Default: live service only, so just the open visit, and no cash day today.
	recorder := httptest.NewRecorder()
	s.handleBOPOSBootstrap(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/bootstrap", "", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeCashDayBody(t, recorder)
	visits, _ := body["visits"].([]any)
	if len(visits) != 1 {
		t.Fatalf("default bootstrap must show only open visits, got %d", len(visits))
	}
	if visit, _ := visits[0].(map[string]any); visit["status"] != "OPEN" {
		t.Fatalf("expected the open visit, got %v", visits[0])
	}
	if body["cashDay"] != nil {
		t.Fatalf("no cash day exists today, got %v", body["cashDay"])
	}
	tables, _ := body["tables"].([]any)
	if table, _ := tables[0].(map[string]any); table["occupied"] != true {
		t.Fatalf("Mesa 1 is occupied by the live visit, got %v", tables[0])
	}

	// Scoped to a past day: its closed visits and its cash day, and the table is
	// no longer reported as occupied by today's service.
	recorder = httptest.NewRecorder()
	s.handleBOPOSBootstrap(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/bootstrap?date=2024-03-07", "", nil))
	body = decodeCashDayBody(t, recorder)
	if body["date"] != "2024-03-07" {
		t.Fatalf("expected the scoped date echoed, got %v", body["date"])
	}
	visits, _ = body["visits"].([]any)
	if len(visits) != 1 {
		t.Fatalf("scoped bootstrap must show that day's visits, got %d", len(visits))
	}
	if visit, _ := visits[0].(map[string]any); visit["status"] != "CLOSED" {
		t.Fatalf("expected the closed visit, got %v", visits[0])
	}
	day, _ := body["cashDay"].(map[string]any)
	if day == nil || day["status"] != "CLOSED" {
		t.Fatalf("expected that day's cash day, got %v", body["cashDay"])
	}
	tables, _ = body["tables"].([]any)
	if table, _ := tables[0].(map[string]any); table["occupied"] != false {
		t.Fatalf("a past day must not report live occupancy, got %v", tables[0])
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSBootstrap(recorder, posCashDayRequest(http.MethodGet, "/admin/pos/bootstrap?date=2024-13-01", "", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed date must be 400, got %d", recorder.Code)
	}
}
