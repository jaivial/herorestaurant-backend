package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

// setupRealtimeStockTest sets up the database for real-time stock tests
func setupRealtimeStockTest(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	statements := []string{
		// Clean up
		`DELETE FROM pos_stock_anomalies`,
		`DELETE FROM pos_ticket_line_stock`,
		`DELETE FROM pos_stock_exceptions`,
		`DELETE FROM pos_payments`,
		`DELETE FROM pos_ticket_lines`,
		`DELETE FROM pos_tickets`,
		`DELETE FROM pos_visits`,
		`DELETE FROM pos_product_stock_rules`,
		`DELETE FROM pos_products`,
		`DELETE FROM pos_settings`,
		`DELETE FROM stock_movements`,
		`DELETE FROM stock_levels`,
		`DELETE FROM stock_item_units`,
		`DELETE FROM stock_items`,
		`DELETE FROM stock_warehouses`,
		`DELETE FROM stock_categories`,
		`DELETE FROM restaurant_tables`,
		`DELETE FROM restaurants`,
		`DELETE FROM bo_users`,

		// Setup base data
		`INSERT INTO restaurants(id,slug,name) VALUES(1,'realtime-stock-test','Realtime Stock Test')`,
		`INSERT INTO bo_users(id,email,name,password_hash) VALUES(7,'stock@test.local','Stock Test','x')`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(5,1,1,'Mesa 1',4,0,1)`,

		// Stock setup
		`INSERT INTO stock_warehouses(id,restaurant_id,name,code,type,is_default,is_active) VALUES(10,1,'Principal','MAIN','STORAGE',1,1)`,
		`INSERT INTO stock_categories(id,restaurant_id,name) VALUES(1,1,'Bebidas')`,
		`INSERT INTO stock_items(id,restaurant_id,category_id,name,kind,base_dimension,base_unit,is_tracked,deduction_source) VALUES(20,1,1,'Agua','FINISHED','COUNT','ud',1,'SALE')`,
		`INSERT INTO stock_items(id,restaurant_id,category_id,name,kind,base_dimension,base_unit,is_tracked,deduction_source) VALUES(21,1,1,'Cerveza','FINISHED','COUNT','ud',1,'SALE')`,
		`INSERT INTO stock_item_units(id,restaurant_id,stock_item_id,code,label,factor_to_base,is_default_display) VALUES(21,1,20,'ud','ud',1,1)`,
		`INSERT INTO stock_item_units(id,restaurant_id,stock_item_id,code,label,factor_to_base,is_default_display) VALUES(22,1,21,'ud','ud',1,1)`,
		`INSERT INTO stock_levels(restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES(1,20,10,100)`,
		`INSERT INTO stock_levels(restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES(1,21,10,50)`,

		// POS setup with LIVE stock mode
		`INSERT INTO pos_settings(restaurant_id,is_enabled,stock_mode,covers_mode) VALUES(1,1,'LIVE','MANUAL')`,
		`INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(30,1,'Agua',250,1)`,
		`INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(31,1,'Cerveza',350,1)`,
		`INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(32,1,'Cafe',150,1)`, // No stock rule - unmapped
		`INSERT INTO pos_product_stock_rules(id,restaurant_id,pos_product_id,stock_item_id,warehouse_id,qty_base_per_sale,is_active) VALUES(31,1,30,20,10,1,1)`,
		`INSERT INTO pos_product_stock_rules(id,restaurant_id,pos_product_id,stock_item_id,warehouse_id,qty_base_per_sale,is_active) VALUES(32,1,31,21,10,1,1)`,

		// Create an open visit and ticket
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(40,1,'DINE_IN',5,CURDATE(),'LUNCH',2,'OPEN',7,'visit-rt-1')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,total_gross_cents,opened_by) VALUES(50,1,40,'TPV-RT-1','ticket-rt-1',0,7)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestRealtimeStockDeductOnLineAdd(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	s := NewServer(db, config.Config{})

	// Add a line with quantity 2
	body := `{"productId":30,"quantity":2,"idempotencyKey":"line-rt-1"}`
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/tickets/50/lines", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSLineCreate(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock was deducted: 100 - 2 = 98
	var qty float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 98 {
		t.Fatalf("expected stock=98, got %v", qty)
	}

	// Verify stock movement was created
	var movements int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1 AND stock_item_id=20 AND type='SALE'`).Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if movements != 1 {
		t.Fatalf("expected 1 movement, got %d", movements)
	}

	// Verify pos_ticket_line_stock record was created
	var lineStockCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pos_ticket_line_stock WHERE restaurant_id=1 AND ticket_id=50`).Scan(&lineStockCount); err != nil {
		t.Fatal(err)
	}
	if lineStockCount != 1 {
		t.Fatalf("expected 1 pos_ticket_line_stock record, got %d", lineStockCount)
	}
}

func TestRealtimeStockRestoreOnLineVoid(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	// Add a line first
	ctx := context.Background()
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by,status) VALUES(60,1,50,30,'Agua',3,250,10,750,'line-void-1',7,'ACTIVE')`)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate stock was already deducted: 100 - 3 = 97
	_, err = db.ExecContext(ctx, `UPDATE stock_levels SET qty_base=97 WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`)
	if err != nil {
		t.Fatal(err)
	}

	// Create stock tracking record
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_line_stock(restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status) VALUES(1,50,60,31,20,10,3,3,'APPLIED')`)
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(db, config.Config{})

	// Void the line
	body := `{"reason":"Test void"}`
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/tickets/50/lines/60/void", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	routeCtx.URLParams.Add("lineId", "60")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSLineVoid(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock was restored: 97 + 3 = 100
	var qty float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 100 {
		t.Fatalf("expected stock=100, got %v", qty)
	}

	// Verify RETURN movement was created
	var returnMovements int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1 AND stock_item_id=20 AND type='RETURN'`).Scan(&returnMovements); err != nil {
		t.Fatal(err)
	}
	if returnMovements != 1 {
		t.Fatalf("expected 1 RETURN movement, got %d", returnMovements)
	}

	// Verify pos_ticket_line_stock status was updated to REVERSED
	var status string
	if err := db.QueryRow(`SELECT status FROM pos_ticket_line_stock WHERE restaurant_id=1 AND ticket_line_id=60`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "REVERSED" {
		t.Fatalf("expected status=REVERSED, got %s", status)
	}
}

func TestRealtimeStockAdjustOnQuantityChange(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	ctx := context.Background()

	// Add a line with quantity 2
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by,status) VALUES(60,1,50,30,'Agua',2,250,10,500,'line-qty-1',7,'ACTIVE')`)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate stock was already deducted: 100 - 2 = 98
	_, err = db.ExecContext(ctx, `UPDATE stock_levels SET qty_base=98 WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`)
	if err != nil {
		t.Fatal(err)
	}

	// Create stock tracking record
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_line_stock(id,restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status) VALUES(100,1,50,60,31,20,10,2,2,'APPLIED')`)
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(db, config.Config{})

	// Test 1: Increase quantity from 2 to 5 (should deduct 3 more)
	body := `{"quantity":5}`
	request := httptest.NewRequest(http.MethodPatch, "/admin/pos/tickets/50/lines/60", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	routeCtx.URLParams.Add("lineId", "60")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSLinePatch(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock: 98 - 3 = 95
	var qty float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 95 {
		t.Fatalf("after increase: expected stock=95, got %v", qty)
	}

	// Test 2: Decrease quantity from 5 to 3 (should restore 2)
	body = `{"quantity":3}`
	request = httptest.NewRequest(http.MethodPatch, "/admin/pos/tickets/50/lines/60", strings.NewReader(body))
	routeCtx = chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	routeCtx.URLParams.Add("lineId", "60")
	ctxWithRoute = context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder = httptest.NewRecorder()
	s.handleBOPOSLinePatch(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock: 95 + 2 = 97
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 97 {
		t.Fatalf("after decrease: expected stock=97, got %v", qty)
	}
}

func TestRealtimeStockRestoreOnVisitCancel(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	ctx := context.Background()

	// Add lines to the ticket
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by,status) VALUES(60,1,50,30,'Agua',2,250,10,500,'line-cancel-1',7,'ACTIVE')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by,status) VALUES(61,1,50,31,'Cerveza',3,350,10,1050,'line-cancel-2',7,'ACTIVE')`)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate stock was already deducted
	_, err = db.ExecContext(ctx, `UPDATE stock_levels SET qty_base=98 WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`) // 100 - 2
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE stock_levels SET qty_base=47 WHERE restaurant_id=1 AND stock_item_id=21 AND warehouse_id=10`) // 50 - 3
	if err != nil {
		t.Fatal(err)
	}

	// Create stock tracking records
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_line_stock(restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status) VALUES(1,50,60,31,20,10,2,2,'APPLIED')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_line_stock(restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status) VALUES(1,50,61,32,21,10,3,3,'APPLIED')`)
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(db, config.Config{})

	// Cancel the visit
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/visits/40/cancel", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "40")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSVisitCancel(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock was restored
	var qty1, qty2 float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=21 AND warehouse_id=10`).Scan(&qty2); err != nil {
		t.Fatal(err)
	}
	if qty1 != 100 {
		t.Fatalf("expected agua stock=100, got %v", qty1)
	}
	if qty2 != 50 {
		t.Fatalf("expected cerveza stock=50, got %v", qty2)
	}
}

func TestRealtimeStockNoDoubleDeductionOnCheckout(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	ctx := context.Background()

	// Add a line and simulate stock already deducted during add
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by,status) VALUES(60,1,50,30,'Agua',2,250,10,500,'line-checkout-1',7,'ACTIVE')`)
	if err != nil {
		t.Fatal(err)
	}

	// Stock was deducted: 100 - 2 = 98
	_, err = db.ExecContext(ctx, `UPDATE stock_levels SET qty_base=98 WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`)
	if err != nil {
		t.Fatal(err)
	}

	// Create stock tracking record with APPLIED status (already deducted)
	_, err = db.ExecContext(ctx, `INSERT INTO pos_ticket_line_stock(restaurant_id,ticket_id,ticket_line_id,stock_rule_id,stock_item_id,warehouse_id,quantity_sold,qty_base_planned,status) VALUES(1,50,60,31,20,10,2,2,'APPLIED')`)
	if err != nil {
		t.Fatal(err)
	}

	// Update ticket totals
	_, err = db.ExecContext(ctx, `UPDATE pos_tickets SET subtotal_gross_cents=500, total_gross_cents=500 WHERE id=50`)
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(db, config.Config{})

	// Checkout
	body := `{"idempotencyKey":"checkout-rt-1","expectedVersion":1,"payments":[{"method":"CASH","amountCents":500,"idempotencyKey":"pay-rt-1"}],"closeVisit":true}`
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/tickets/50/checkout", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSCheckout(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock was NOT deducted again (should still be 98)
	var qty float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 98 {
		t.Fatalf("expected stock=98 (no double deduction), got %v", qty)
	}

	// Verify only 1 SALE movement exists (from when line was added)
	var movements int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1 AND stock_item_id=20 AND type='SALE'`).Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if movements != 1 {
		t.Fatalf("expected 1 SALE movement (no double deduction), got %d", movements)
	}

	var response map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	if response["stockStatus"] != "COMPLETE" {
		t.Fatalf("expected stockStatus=COMPLETE, got %v", response["stockStatus"])
	}
}

func TestRealtimeStockShadowModeNoDeduction(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	ctx := context.Background()

	// Change to SHADOW mode
	_, err = db.ExecContext(ctx, `UPDATE pos_settings SET stock_mode='SHADOW' WHERE restaurant_id=1`)
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(db, config.Config{})

	// Add a line
	body := `{"productId":30,"quantity":5,"idempotencyKey":"line-shadow-1"}`
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/tickets/50/lines", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSLineCreate(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify stock was NOT deducted (still 100)
	var qty float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 100 {
		t.Fatalf("expected stock=100 (no deduction in SHADOW mode), got %v", qty)
	}
}

func TestRealtimeStockUnmappedProduct(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	setupRealtimeStockTest(t, db)

	s := NewServer(db, config.Config{})

	// Add an unmapped product (Cafe - no stock rule)
	body := `{"productId":32,"quantity":2,"idempotencyKey":"line-unmapped-1"}`
	request := httptest.NewRequest(http.MethodPost, "/admin/pos/tickets/50/lines", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "50")
	ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
	request = request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))

	recorder := httptest.NewRecorder()
	s.handleBOPOSLineCreate(recorder, request)

	// Should still succeed (unmapped products don't block order)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Verify no stock deduction happened
	var qty float64
	if err := db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 100 {
		t.Fatalf("expected stock=100 (unmapped product), got %v", qty)
	}
}
