package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// posGuardSeed builds one visit with one open ticket and one active line on
// 2024-03-07, plus a cash day for that date in the requested status.
func posGuardSeed(t *testing.T, db *sql.DB, status string) {
	t.Helper()
	statements := []string{
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,closed_by,opening_cash_cents,closed_at) VALUES(900,1,'2024-03-07',?,7,7,0,'2024-03-07 23:00:00')`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(40,1,900,'DINE_IN',5,'2024-03-07','DINNER',2,'OPEN',7,'v40','2024-03-07 21:00:00')`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(6,1,2,'Mesa 2',4,1,1)`,
		// VisitCreate has no visit to resolve, so it lands on today's business
		// date. The cutoff can place that on either calendar day, so both are
		// seeded with the same status.
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,closed_by,opening_cash_cents,closed_at) VALUES(901,1,CURDATE(),?,7,7,0,NOW())`,
		`INSERT INTO pos_cash_days(id,restaurant_id,business_date,status,opened_by,closed_by,opening_cash_cents,closed_at) VALUES(902,1,CURDATE()-INTERVAL 1 DAY,?,7,7,0,NOW())`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(7,1,3,'Mesa 3',4,2,1)`,
		`INSERT INTO pos_visits(id,restaurant_id,cash_day_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key,opened_at) VALUES(41,1,900,'DINE_IN',6,'2024-03-07','DINNER',2,'OPEN',7,'v41','2024-03-07 21:05:00')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,status,opened_by) VALUES(50,1,40,'TPV-1','t50',1000,1000,'OPEN',7)`,
		`INSERT INTO pos_tags(id,restaurant_id,name,scope) VALUES(1,1,'Tag','BOTH')`,
		`INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,discount_cents,line_total_gross_cents,status,idempotency_key,created_by) VALUES(60,1,50,30,'Agua',1,250,0,0,250,'ACTIVE','l60',7)`,
	}
	for _, statement := range statements {
		var err error
		if strings.Contains(statement, "pos_cash_days") {
			_, err = db.Exec(statement, status)
		} else {
			_, err = db.Exec(statement)
		}
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// posGuardCase is one mutation endpoint, addressed the way its route does.
type posGuardCase struct {
	name    string
	call    func(s *Server, w http.ResponseWriter, r *http.Request)
	method  string
	target  string
	body    string
	params  map[string]string
	openMin int // status the handler is allowed to answer on an open day
}

func posGuardCases() []posGuardCase {
	ticket := map[string]string{"id": "50"}
	ticketLine := map[string]string{"id": "50", "lineId": "60"}
	visit := map[string]string{"id": "40"}
	return []posGuardCase{
		{"VisitCreate", (*Server).handleBOPOSVisitCreate, http.MethodPost, "/admin/pos/visits", `{"channel":"DINE_IN","tableId":7,"covers":2,"idempotencyKey":"g1"}`, nil, 0},
		{"LineCreate", (*Server).handleBOPOSLineCreate, http.MethodPost, "/admin/pos/tickets/50/lines", `{"productId":30,"quantity":1,"idempotencyKey":"g2"}`, ticket, 0},
		{"LineVoid", (*Server).handleBOPOSLineVoid, http.MethodPost, "/admin/pos/tickets/50/lines/60/void", `{"reason":"x"}`, ticketLine, 0},
		{"Discount", (*Server).handleBOPOSDiscount, http.MethodPost, "/admin/pos/tickets/50/discount", `{"amountCents":100,"reason":"x"}`, ticket, 0},
		{"VisitPatch", (*Server).handleBOPOSVisitPatch, http.MethodPatch, "/admin/pos/visits/40", `{"covers":3,"expectedVersion":1}`, visit, 0},
		{"VisitCancel", (*Server).handleBOPOSVisitCancel, http.MethodPost, "/admin/pos/visits/40/cancel", `{}`, visit, 0},
		{"VisitTicketCreate", (*Server).handleBOPOSVisitTicketCreate, http.MethodPost, "/admin/pos/visits/40/tickets", `{"idempotencyKey":"g3"}`, visit, 0},
		{"LinePatch", (*Server).handleBOPOSLinePatch, http.MethodPatch, "/admin/pos/tickets/50/lines/60", `{"quantity":2,"expectedVersion":1}`, ticketLine, 0},
		{"Checkout", (*Server).handleBOPOSCheckout, http.MethodPost, "/admin/pos/tickets/50/checkout", `{"idempotencyKey":"g4","payments":[{"method":"CASH","amountCents":250,"idempotencyKey":"p1"}]}`, ticket, 0},
		{"Refund", (*Server).handleBOPOSRefund, http.MethodPost, "/admin/pos/tickets/50/refunds", `{"idempotencyKey":"g5","amountCents":100,"reason":"x"}`, ticket, 0},
		{"VisitClose", (*Server).handleBOPOSVisitClose, http.MethodPost, "/admin/pos/visits/40/close", `{}`, visit, 0},
		{"CoverAdjustment", (*Server).handleBOPOSCoverAdjustment, http.MethodPost, "/admin/pos/covers/adjustments", `{"date":"2024-03-07","serviceType":"DINNER","delta":2,"reason":"x","idempotencyKey":"g6"}`, nil, 0},
		{"VisitPark", (*Server).handleBOPOSVisitPark, http.MethodPost, "/admin/pos/visits/40/park", `{"parked":true}`, visit, 0},
		{"VisitMerge", (*Server).handleBOPOSVisitMerge, http.MethodPost, "/admin/pos/visits/40/merge", `{"sourceVisitIds":[41],"idempotencyKey":"g7"}`, visit, 0},
		{"VisitCustomer", (*Server).handleBOPOSVisitCustomer, http.MethodPatch, "/admin/pos/visits/40/customer", `{"customerName":"X"}`, visit, 0},
		{"TicketAdjustment", (*Server).handleBOPOSTicketAdjustment, http.MethodPost, "/admin/pos/tickets/50/adjustments", `{"type":"DISCOUNT","mode":"AMOUNT","amountCents":100,"reason":"x","idempotencyKey":"g8"}`, ticket, 0},
		{"LineComp", (*Server).handleBOPOSLineComp, http.MethodPost, "/admin/pos/tickets/50/lines/60/comp", `{"comped":true,"reason":"x"}`, ticketLine, 0},
		{"TicketOperator", (*Server).handleBOPOSTicketOperator, http.MethodPatch, "/admin/pos/tickets/50/operator", `{"operatorMemberId":1,"expectedVersion":1}`, ticket, 0},
		{"TicketTagAttach", (*Server).handleBOPOSTicketTagAttach, http.MethodPost, "/admin/pos/tickets/50/tags", `{"tagId":1}`, ticket, 0},
		{"LineTagAttach", (*Server).handleBOPOSLineTagAttach, http.MethodPost, "/admin/pos/tickets/50/lines/60/tags", `{"tagId":1}`, ticketLine, 0},
		{"LineMove", (*Server).handleBOPOSLineMove, http.MethodPost, "/admin/pos/tickets/50/lines/60/move", `{"targetTicketId":51,"idempotencyKey":"g9"}`, ticketLine, 0},
		{"TicketVoid", (*Server).handleBOPOSTicketVoid, http.MethodPost, "/admin/pos/tickets/50/void", `{"reason":"x"}`, ticket, 0},
		{"KitchenDispatchCreate", (*Server).handleBOPOSKitchenDispatchCreate, http.MethodPost, "/admin/pos/tickets/50/kitchen/dispatches", `{"idempotencyKey":"g10"}`, ticket, 0},
	}
}

// Every mutation endpoint must refuse a sealed day with the same code, so the
// frontend has a single condition to react to instead of one per endpoint.
func TestPOSMutationsRejectClosedCashDay(t *testing.T) {
	cases := posGuardCases()
	if len(cases) != 23 {
		t.Fatalf("expected the 23 mutation points to be covered, got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, s := posCashDayTestDB(t)
			posGuardSeed(t, db, "CLOSED")
			recorder := httptest.NewRecorder()
			tc.call(s, recorder, posCashDayRequest(tc.method, tc.target, tc.body, tc.params))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d %s", recorder.Code, recorder.Body.String())
			}
			body := decodeCashDayBody(t, recorder)
			if body["code"] != "CASH_DAY_CLOSED" {
				t.Fatalf("expected CASH_DAY_CLOSED, got %s", recorder.Body.String())
			}
			if body["success"] != false {
				t.Fatalf("expected success=false, got %s", recorder.Body.String())
			}
		})
	}
}

// The guard must not fire on an open day: a 409 CASH_DAY_CLOSED here would mean
// the POS is bricked during normal service.
func TestPOSMutationsPassOnOpenCashDay(t *testing.T) {
	for _, tc := range posGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			db, s := posCashDayTestDB(t)
			posGuardSeed(t, db, "OPEN")
			recorder := httptest.NewRecorder()
			tc.call(s, recorder, posCashDayRequest(tc.method, tc.target, tc.body, tc.params))
			if recorder.Code == http.StatusConflict {
				if body := decodeCashDayBody(t, recorder); body["code"] == "CASH_DAY_CLOSED" {
					t.Fatalf("the guard fired on an open day: %s", recorder.Body.String())
				}
			}
		})
	}
}

// A restaurant that never opened a till must keep working exactly as before,
// otherwise this feature would brick every existing deployment on upgrade.
func TestPOSMutationsPassWithoutAnyCashDay(t *testing.T) {
	for _, tc := range posGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			db, s := posCashDayTestDB(t)
			posGuardSeed(t, db, "OPEN")
			for _, statement := range []string{`UPDATE pos_visits SET cash_day_id=NULL`, `DELETE FROM pos_cash_days`} {
				if _, err := db.Exec(statement); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}
			recorder := httptest.NewRecorder()
			tc.call(s, recorder, posCashDayRequest(tc.method, tc.target, tc.body, tc.params))
			if recorder.Code == http.StatusConflict {
				if body := decodeCashDayBody(t, recorder); body["code"] == "CASH_DAY_CLOSED" {
					t.Fatalf("a date with no cash day must not be treated as closed: %s", recorder.Body.String())
				}
			}
		})
	}
}

// Closing the day is what seals it, so the guard must never block the close
// itself or the arqueo that precedes it.
func TestPOSCashDayCloseIsNotBlockedByItsOwnGuard(t *testing.T) {
	db, s := posCashDayTestDB(t)
	posGuardSeed(t, db, "OPEN")
	if _, err := db.Exec(`UPDATE pos_visits SET status='CLOSED',closed_at='2024-03-07 22:00:00' WHERE restaurant_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE pos_tickets SET status='PAID' WHERE restaurant_id=1`); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	s.handleBOPOSCashDayClose(recorder, posCashDayRequest(http.MethodPost, "/admin/pos/cash-days/900/close", `{"countedCashCents":0}`, map[string]string{"id": "900"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("closing the day must work, got %d %s", recorder.Code, recorder.Body.String())
	}
}
