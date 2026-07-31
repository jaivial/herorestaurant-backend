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
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

// posRailTestDB prepares a throwaway tenant with one open dine-in visit,
// one open ticket and one active line. Set POS_RAIL_TEST_MYSQL_DSN to enable.
func posRailTestDB(t *testing.T) (*sql.DB, *Server) {
	t.Helper()
	dsn := os.Getenv("POS_RAIL_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_RAIL_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	statements := []string{
		`SET FOREIGN_KEY_CHECKS=0`,
		`DELETE FROM pos_ticket_line_tags`, `DELETE FROM pos_ticket_tags`, `DELETE FROM pos_tags`,
		`DELETE FROM pos_drawer_events`, `DELETE FROM pos_ticket_adjustments`, `DELETE FROM pos_payments`,
		`DELETE FROM pos_ticket_lines`, `DELETE FROM pos_tickets`, `DELETE FROM pos_visits`,
		`DELETE FROM pos_shifts`, `DELETE FROM pos_products`, `DELETE FROM pos_settings`,
		`DELETE FROM restaurant_members`, `DELETE FROM restaurant_tables`, `DELETE FROM restaurant_info`, `DELETE FROM restaurant_branding`, `DELETE FROM restaurants`, `DELETE FROM bo_users`,
		`INSERT INTO restaurants(id,slug,name) VALUES(1,'rail-test','Rail Test')`,
		`INSERT INTO bo_users(id,email,name,password_hash) VALUES(7,'rail@test.local','Rail','x')`,
		`INSERT INTO restaurant_members(id,restaurant_id,first_name,last_name) VALUES(3,1,'Ana','Camarera')`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(5,1,1,'Mesa 1',4,0,1)`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(6,1,2,'Mesa 2',2,1,1)`,
		`INSERT INTO pos_settings(restaurant_id,is_enabled,stock_mode,covers_mode) VALUES(1,1,'OFF','MANUAL')`,
		`INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(30,1,'Agua',250,1)`,
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(40,1,'DINE_IN',5,CURDATE(),'LUNCH',2,'OPEN',7,'visit-a')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,opened_by) VALUES(50,1,40,'TPV-1','ticket-a',1000,1000,7)`,
		`INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by) VALUES(60,1,50,30,'Agua',4,250,10,1000,'line-a',7)`,
		`SET FOREIGN_KEY_CHECKS=1`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return db, NewServer(db, config.Config{})
}

// posRailRequest builds an authenticated request with chi URL params.
func posRailRequest(method, body string, params map[string]string) *http.Request {
	request := httptest.NewRequest(method, "/admin/pos/test", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	for key, value := range params {
		routeCtx.URLParams.Add(key, value)
	}
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	return request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
}

func decodeRailBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json %q: %v", recorder.Body.String(), err)
	}
	return out
}

func TestPOSRailParkAndUnparkVisit(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitPark(recorder, posRailRequest(http.MethodPost, `{"parked":true,"note":"Mesa esperando postre"}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("park: got %d %s", recorder.Code, recorder.Body.String())
	}
	var parkedAt sql.NullTime
	var note sql.NullString
	if err := db.QueryRow(`SELECT parked_at,parked_note FROM pos_visits WHERE id=40`).Scan(&parkedAt, &note); err != nil {
		t.Fatal(err)
	}
	if !parkedAt.Valid || note.String != "Mesa esperando postre" {
		t.Fatalf("visit not parked: %v %v", parkedAt, note)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSVisitPark(recorder, posRailRequest(http.MethodPost, `{"parked":false}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unpark: got %d %s", recorder.Code, recorder.Body.String())
	}
	if err := db.QueryRow(`SELECT parked_at FROM pos_visits WHERE id=40`).Scan(&parkedAt); err != nil {
		t.Fatal(err)
	}
	if parkedAt.Valid {
		t.Fatal("visit still parked after unpark")
	}
}

func TestPOSRailSurchargeAndDiscountCoexistOnTicket(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSTicketAdjustment(recorder, posRailRequest(http.MethodPost, `{"type":"SURCHARGE","mode":"PERCENT","percent":10,"reason":"Terraza","idempotencyKey":"adj-1"}`, map[string]string{"id": "50"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("surcharge: got %d %s", recorder.Code, recorder.Body.String())
	}
	var total, surcharge int64
	if err := db.QueryRow(`SELECT total_gross_cents,surcharge_cents FROM pos_tickets WHERE id=50`).Scan(&total, &surcharge); err != nil {
		t.Fatal(err)
	}
	if surcharge != 100 || total != 1100 {
		t.Fatalf("want surcharge 100 total 1100; got %d %d", surcharge, total)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSTicketAdjustment(recorder, posRailRequest(http.MethodPost, `{"type":"DISCOUNT","mode":"AMOUNT","amountCents":200,"reason":"Fidelidad","idempotencyKey":"adj-2"}`, map[string]string{"id": "50"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("discount: got %d %s", recorder.Code, recorder.Body.String())
	}
	if err := db.QueryRow(`SELECT total_gross_cents,surcharge_cents FROM pos_tickets WHERE id=50`).Scan(&total, &surcharge); err != nil {
		t.Fatal(err)
	}
	// 1000 base - 200 discount + 100 surcharge
	if surcharge != 100 || total != 900 {
		t.Fatalf("want surcharge 100 total 900; got %d %d", surcharge, total)
	}
	var net int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(amount_cents),0) FROM pos_ticket_adjustments WHERE ticket_id=50 AND status='ACTIVE'`).Scan(&net); err != nil {
		t.Fatal(err)
	}
	if net != -100 {
		t.Fatalf("want net adjustment -100; got %d", net)
	}
}

func TestPOSRailAdjustmentIsIdempotent(t *testing.T) {
	db, s := posRailTestDB(t)
	body := `{"type":"SURCHARGE","mode":"AMOUNT","amountCents":150,"reason":"Terraza","idempotencyKey":"adj-same"}`
	for range 3 {
		recorder := httptest.NewRecorder()
		s.handleBOPOSTicketAdjustment(recorder, posRailRequest(http.MethodPost, body, map[string]string{"id": "50"}))
		if recorder.Code != http.StatusOK {
			t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
		}
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_ticket_adjustments WHERE ticket_id=50`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("want 1 adjustment row; got %d", rows)
	}
	var surcharge int64
	if err := db.QueryRow(`SELECT surcharge_cents FROM pos_tickets WHERE id=50`).Scan(&surcharge); err != nil {
		t.Fatal(err)
	}
	if surcharge != 150 {
		t.Fatalf("replayed adjustment changed totals: got surcharge %d; want 150", surcharge)
	}
}

func TestPOSRailAdjustmentRejectsStaleVersionAndLongReason(t *testing.T) {
	_, s := posRailTestDB(t)
	for _, body := range []string{
		`{"type":"SURCHARGE","mode":"AMOUNT","amountCents":100,"reason":"Terraza","idempotencyKey":"stale","expectedVersion":99}`,
		`{"type":"SURCHARGE","mode":"AMOUNT","amountCents":100,"reason":"` + strings.Repeat("x", 501) + `","idempotencyKey":"long"}`,
	} {
		recorder := httptest.NewRecorder()
		s.handleBOPOSTicketAdjustment(recorder, posRailRequest(http.MethodPost, body, map[string]string{"id": "50"}))
		if recorder.Code != http.StatusConflict && recorder.Code != http.StatusBadRequest {
			t.Fatalf("want validation rejection; got %d %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestPOSRailCompLineZeroesRevenueButKeepsLine(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSLineComp(recorder, posRailRequest(http.MethodPost, `{"comped":true,"reason":"Invitacion casa"}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("comp: got %d %s", recorder.Code, recorder.Body.String())
	}
	var total int64
	var status string
	var compedAt sql.NullTime
	if err := db.QueryRow(`SELECT t.total_gross_cents,l.status,l.comped_at FROM pos_tickets t JOIN pos_ticket_lines l ON l.ticket_id=t.id WHERE t.id=50`).Scan(&total, &status, &compedAt); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("want total 0 after comp; got %d", total)
	}
	// The line must survive so the kitchen still fires it and stock still moves.
	if status != "ACTIVE" || !compedAt.Valid {
		t.Fatalf("line not comped-but-active: %s %v", status, compedAt)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSLineComp(recorder, posRailRequest(http.MethodPost, `{"comped":false}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("uncomp: got %d %s", recorder.Code, recorder.Body.String())
	}
	if err := db.QueryRow(`SELECT total_gross_cents FROM pos_tickets WHERE id=50`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1000 {
		t.Fatalf("want total restored to 1000; got %d", total)
	}
}

func TestPOSRailCompRequiresReason(t *testing.T) {
	_, s := posRailTestDB(t)
	recorder := httptest.NewRecorder()
	s.handleBOPOSLineComp(recorder, posRailRequest(http.MethodPost, `{"comped":true,"reason":"   "}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("want 400 without reason; got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPOSRailFullyCompedTicketChecksOutWithoutFakePayment(t *testing.T) {
	db, s := posRailTestDB(t)
	recorder := httptest.NewRecorder()
	s.handleBOPOSLineComp(recorder, posRailRequest(http.MethodPost, `{"comped":true,"reason":"Invitacion"}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("comp: got %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	s.handleBOPOSCheckout(recorder, posRailRequest(http.MethodPost, `{"idempotencyKey":"zero-checkout","expectedVersion":2,"payments":[],"closeVisit":true}`, map[string]string{"id": "50"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("zero checkout: got %d %s", recorder.Code, recorder.Body.String())
	}
	var status string
	var payments int
	if err := db.QueryRow(`SELECT status FROM pos_tickets WHERE id=50`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_payments WHERE ticket_id=50`).Scan(&payments); err != nil {
		t.Fatal(err)
	}
	if status != "PAID" || payments != 0 {
		t.Fatalf("want paid ticket and no fake payments; got %s and %d payments", status, payments)
	}
}

func TestPOSRailMergeVisitsConsolidatesLinesAndCovers(t *testing.T) {
	db, s := posRailTestDB(t)
	seed := []string{
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(41,1,'DINE_IN',6,CURDATE(),'LUNCH',3,'OPEN',7,'visit-b')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,subtotal_gross_cents,total_gross_cents,opened_by) VALUES(51,1,41,'TPV-2','ticket-b',500,500,7)`,
		`INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by) VALUES(61,1,51,30,'Agua',2,250,10,500,'line-b',7)`,
	}
	for _, statement := range seed {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitMerge(recorder, posRailRequest(http.MethodPost, `{"sourceVisitIds":[41],"idempotencyKey":"merge-1"}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("merge: got %d %s", recorder.Code, recorder.Body.String())
	}
	body := decodeRailBody(t, recorder)
	if body["covers"].(float64) != 5 {
		t.Fatalf("want merged covers 5; got %v", body["covers"])
	}

	var lines int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_ticket_lines WHERE ticket_id=50 AND status='ACTIVE'`).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if lines != 2 {
		t.Fatalf("want 2 lines on the target ticket; got %d", lines)
	}
	var total int64
	if err := db.QueryRow(`SELECT total_gross_cents FROM pos_tickets WHERE id=50`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1500 {
		t.Fatalf("want merged total 1500; got %d", total)
	}
	var sourceStatus string
	var mergedInto sql.NullInt64
	if err := db.QueryRow(`SELECT status,merged_into_visit_id FROM pos_visits WHERE id=41`).Scan(&sourceStatus, &mergedInto); err != nil {
		t.Fatal(err)
	}
	if sourceStatus != "MERGED" || mergedInto.Int64 != 40 {
		t.Fatalf("source visit not merged: %s %v", sourceStatus, mergedInto)
	}
	// The freed table must be available for a new visit.
	var openOnTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_visits WHERE table_id=6 AND status='OPEN'`).Scan(&openOnTable); err != nil {
		t.Fatal(err)
	}
	if openOnTable != 0 {
		t.Fatalf("merged table still occupied")
	}
}

func TestPOSRailMergeRejectsPaidSource(t *testing.T) {
	db, s := posRailTestDB(t)
	seed := []string{
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(42,1,'DINE_IN',6,CURDATE(),'LUNCH',2,'OPEN',7,'visit-c')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,status,total_gross_cents,opened_by) VALUES(52,1,42,'TPV-3','ticket-c','PAID',500,7)`,
	}
	for _, statement := range seed {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitMerge(recorder, posRailRequest(http.MethodPost, `{"sourceVisitIds":[42],"idempotencyKey":"merge-2"}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("want 409 merging a paid visit; got %d %s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM pos_visits WHERE id=42`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" {
		t.Fatalf("paid source was modified: %s", status)
	}
}

func TestPOSRailDrawerOpenRequiresShift(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSDrawerOpen(recorder, posRailRequest(http.MethodPost, `{"reason":"CHANGE","idempotencyKey":"drawer-1"}`, nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("want 409 without an open shift; got %d %s", recorder.Code, recorder.Body.String())
	}

	if _, err := db.Exec(`INSERT INTO pos_shifts(id,restaurant_id,terminal_key,opened_by,opening_cash_cents,status) VALUES(90,1,'main',7,10000,'OPEN')`); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	s.handleBOPOSDrawerOpen(recorder, posRailRequest(http.MethodPost, `{"reason":"CHANGE","idempotencyKey":"drawer-1"}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("want 201 with an open shift; got %d %s", recorder.Code, recorder.Body.String())
	}

	// Same idempotency key must not append a second drawer event.
	recorder = httptest.NewRecorder()
	s.handleBOPOSDrawerOpen(recorder, posRailRequest(http.MethodPost, `{"reason":"CHANGE","idempotencyKey":"drawer-1"}`, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("want 200 on replay; got %d %s", recorder.Code, recorder.Body.String())
	}
	var events int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_drawer_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("want 1 drawer event; got %d", events)
	}
}

func TestPOSRailCustomerAndOperatorAttachToVisitAndTicket(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitCustomer(recorder, posRailRequest(http.MethodPatch, `{"customerName":"Ana Ruiz","customerTaxId":"12345678Z"}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("customer: got %d %s", recorder.Code, recorder.Body.String())
	}
	if decodeRailBody(t, recorder)["visit"] == nil {
		t.Fatal("customer response missing authoritative visit")
	}
	var name, taxID sql.NullString
	if err := db.QueryRow(`SELECT customer_name,customer_tax_id FROM pos_visits WHERE id=40`).Scan(&name, &taxID); err != nil {
		t.Fatal(err)
	}
	if name.String != "Ana Ruiz" || taxID.String != "12345678Z" {
		t.Fatalf("customer not stored: %v %v", name, taxID)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSTicketOperator(recorder, posRailRequest(http.MethodPatch, `{"operatorMemberId":3}`, map[string]string{"id": "50"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("operator: got %d %s", recorder.Code, recorder.Body.String())
	}
	if decodeRailBody(t, recorder)["ticket"] == nil {
		t.Fatal("operator response missing authoritative ticket")
	}
	var operator sql.NullInt64
	if err := db.QueryRow(`SELECT operator_member_id FROM pos_tickets WHERE id=50`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if operator.Int64 != 3 {
		t.Fatalf("operator not attributed: %v", operator)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSTicketOperator(recorder, posRailRequest(http.MethodPatch, `{"operatorMemberId":999}`, map[string]string{"id": "50"}))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown member; got %d", recorder.Code)
	}
}

func TestPOSRailCustomerNormalizesAndRejectsTaxID(t *testing.T) {
	db, s := posRailTestDB(t)
	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitCustomer(recorder, posRailRequest(http.MethodPatch, `{"customerName":" Ana ","customerTaxId":" 12345678z "}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("normalize customer: got %d %s", recorder.Code, recorder.Body.String())
	}
	var taxID sql.NullString
	if err := db.QueryRow(`SELECT customer_tax_id FROM pos_visits WHERE id=40`).Scan(&taxID); err != nil {
		t.Fatal(err)
	}
	if taxID.String != "12345678Z" {
		t.Fatalf("tax ID not normalized: %q", taxID.String)
	}
	recorder = httptest.NewRecorder()
	s.handleBOPOSVisitCustomer(recorder, posRailRequest(http.MethodPatch, `{"customerName":"Ana","customerTaxId":"12345678A"}`, map[string]string{"id": "40"}))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid tax ID; got %d", recorder.Code)
	}
}

func TestPOSRailLinePatchKeepsNoteWhenOmitted(t *testing.T) {
	db, s := posRailTestDB(t)

	// Comentario stores a note on the line...
	recorder := httptest.NewRecorder()
	s.handleBOPOSLinePatch(recorder, posRailRequest(http.MethodPatch, `{"quantity":4,"notes":"Sin hielo"}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("set note: got %d %s", recorder.Code, recorder.Body.String())
	}
	var note sql.NullString
	if err := db.QueryRow(`SELECT notes FROM pos_ticket_lines WHERE id=60`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note.String != "Sin hielo" {
		t.Fatalf("note not stored: %v", note)
	}

	// ...and a later quantity change must not silently wipe it.
	recorder = httptest.NewRecorder()
	s.handleBOPOSLinePatch(recorder, posRailRequest(http.MethodPatch, `{"quantity":2}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("quantity change: got %d %s", recorder.Code, recorder.Body.String())
	}
	if err := db.QueryRow(`SELECT notes FROM pos_ticket_lines WHERE id=60`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note.String != "Sin hielo" {
		t.Fatalf("quantity change wiped the note: %v", note)
	}

	// An explicit empty string still clears it.
	recorder = httptest.NewRecorder()
	s.handleBOPOSLinePatch(recorder, posRailRequest(http.MethodPatch, `{"quantity":2,"notes":""}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear note: got %d %s", recorder.Code, recorder.Body.String())
	}
	if err := db.QueryRow(`SELECT notes FROM pos_ticket_lines WHERE id=60`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note.Valid && note.String != "" {
		t.Fatalf("note not cleared: %v", note)
	}
}

func TestPOSRailCompedLineSurvivesQuantityChange(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSLineComp(recorder, posRailRequest(http.MethodPost, `{"comped":true,"reason":"Invitacion"}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("comp: got %d %s", recorder.Code, recorder.Body.String())
	}

	// Reducing the quantity of a comped line must keep it free, not blow up
	// because the stored discount now exceeds the smaller gross.
	recorder = httptest.NewRecorder()
	s.handleBOPOSLinePatch(recorder, posRailRequest(http.MethodPatch, `{"quantity":2}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("quantity change on comped line: got %d %s", recorder.Code, recorder.Body.String())
	}
	var total, lineDiscount int64
	if err := db.QueryRow(`SELECT t.total_gross_cents,l.discount_cents FROM pos_tickets t JOIN pos_ticket_lines l ON l.ticket_id=t.id WHERE t.id=50`).Scan(&total, &lineDiscount); err != nil {
		t.Fatal(err)
	}
	if total != 0 || lineDiscount != 500 {
		t.Fatalf("want comped total 0 and discount 500; got %d %d", total, lineDiscount)
	}
}

func TestPOSRailTagsAttachToTicketAndLine(t *testing.T) {
	db, s := posRailTestDB(t)

	recorder := httptest.NewRecorder()
	s.handleBOPOSTagCreate(recorder, posRailRequest(http.MethodPost, `{"name":"Sin gluten","color":"#16a34a"}`, nil))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("tag create: got %d %s", recorder.Code, recorder.Body.String())
	}
	tagID := int64(decodeRailBody(t, recorder)["id"].(float64))

	recorder = httptest.NewRecorder()
	s.handleBOPOSLineTagAttach(recorder, posRailRequest(http.MethodPost, `{"tagId":`+strconv.FormatInt(tagID, 10)+`}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("line tag: got %d %s", recorder.Code, recorder.Body.String())
	}
	var attached int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_ticket_line_tags WHERE ticket_line_id=60 AND tag_id=?`, tagID).Scan(&attached); err != nil {
		t.Fatal(err)
	}
	if attached != 1 {
		t.Fatalf("line tag not attached")
	}
	if decodeRailBody(t, recorder)["ticket"] == nil {
		t.Fatal("tag response missing authoritative ticket")
	}
	ticket := decodeRailBody(t, recorder)["ticket"].(map[string]any)
	line := ticket["lines"].([]any)[0].(map[string]any)
	if len(line["tagIds"].([]any)) != 1 {
		t.Fatalf("persisted line tag IDs missing: %v", line)
	}

	// Re-attaching the same tag must stay a no-op rather than error.
	recorder = httptest.NewRecorder()
	s.handleBOPOSLineTagAttach(recorder, posRailRequest(http.MethodPost, `{"tagId":`+strconv.FormatInt(tagID, 10)+`}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("re-attach: got %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	s.handleBOPOSLineTagAttach(recorder, posRailRequest(http.MethodPost, `{"tagId":`+strconv.FormatInt(tagID, 10)+`,"attach":false}`, map[string]string{"id": "50", "lineId": "60"}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("detach: got %d %s", recorder.Code, recorder.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_ticket_line_tags WHERE ticket_line_id=60`).Scan(&attached); err != nil {
		t.Fatal(err)
	}
	if attached != 0 {
		t.Fatalf("line tag not detached")
	}
}

// The comanda ticket PDF prints the issuer identity, so bootstrap must carry the
// restaurant profile (name, fiscal ID, address, phone) and the branding logo.
func TestPOSBootstrapExposesRestaurantProfile(t *testing.T) {
	db, s := posRailTestDB(t)
	statements := []string{
		`DELETE FROM restaurant_info`,
		`DELETE FROM restaurant_branding`,
		`INSERT INTO restaurant_info(restaurant_id,direccion,telefono,email,cif) VALUES(1,'Calle Mayor 1','+34600000000','hola@test.local','B12345678')`,
		`INSERT INTO restaurant_branding(restaurant_id,brand_name,logo_url) VALUES(1,'Rail Brand','https://cdn.test/logo.webp')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}

	recorder := httptest.NewRecorder()
	s.handleBOPOSBootstrap(recorder, posRailRequest(http.MethodGet, "", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap: got %d %s", recorder.Code, recorder.Body.String())
	}
	restaurant, ok := decodeRailBody(t, recorder)["restaurant"].(map[string]any)
	if !ok {
		t.Fatalf("bootstrap missing restaurant block: %s", recorder.Body.String())
	}
	for key, want := range map[string]string{
		"name":    "Rail Brand",
		"taxId":   "B12345678",
		"address": "Calle Mayor 1",
		"phone":   "+34600000000",
		"email":   "hola@test.local",
		"logoUrl": "https://cdn.test/logo.webp",
	} {
		if got, _ := restaurant[key].(string); got != want {
			t.Fatalf("restaurant.%s = %q, want %q", key, got, want)
		}
	}
}

// Without branding rows the profile falls back to the restaurant name/avatar.
func TestPOSBootstrapRestaurantProfileFallsBackToAvatar(t *testing.T) {
	db, s := posRailTestDB(t)
	if _, err := db.Exec(`UPDATE restaurants SET avatar='https://cdn.test/avatar.webp' WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	s.handleBOPOSBootstrap(recorder, posRailRequest(http.MethodGet, "", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap: got %d %s", recorder.Code, recorder.Body.String())
	}
	restaurant, ok := decodeRailBody(t, recorder)["restaurant"].(map[string]any)
	if !ok {
		t.Fatalf("bootstrap missing restaurant block: %s", recorder.Body.String())
	}
	if got, _ := restaurant["name"].(string); got != "Rail Test" {
		t.Fatalf("restaurant.name = %q, want fallback restaurant name", got)
	}
	if got, _ := restaurant["logoUrl"].(string); got != "https://cdn.test/avatar.webp" {
		t.Fatalf("restaurant.logoUrl = %q, want avatar fallback", got)
	}
}
