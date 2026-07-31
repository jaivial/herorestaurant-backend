package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

func TestPOSCheckoutIntegration(t *testing.T) {
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	statements := []string{
		`DELETE FROM stock_document_scans`, `DELETE FROM stock_affluence_daily`, `DELETE FROM pos_payments`, `DELETE FROM pos_ticket_line_stock`, `DELETE FROM pos_stock_exceptions`, `DELETE FROM pos_ticket_lines`, `DELETE FROM pos_tickets`, `DELETE FROM pos_visits`, `DELETE FROM pos_product_stock_rules`, `DELETE FROM pos_products`, `DELETE FROM pos_settings`, `DELETE FROM stock_movements`, `DELETE FROM stock_levels`, `DELETE FROM stock_item_units`, `DELETE FROM stock_items`, `DELETE FROM stock_warehouses`, `DELETE FROM stock_categories`, `DELETE FROM restaurant_tables`, `DELETE FROM restaurants`, `DELETE FROM bo_users`,
		`INSERT INTO restaurants(id,slug,name) VALUES(1,'pos-checkout-test','POS Checkout Test')`, `INSERT INTO bo_users(id,email,name,password_hash) VALUES(7,'pos@test.local','POS Test','x')`, `INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(5,1,1,'Mesa 1',4,0,1)`, `INSERT INTO stock_warehouses(id,restaurant_id,name,code,type,is_default,is_active) VALUES(10,1,'Principal','MAIN','STORAGE',1,1)`, `INSERT INTO stock_categories(id,restaurant_id,name) VALUES(1,1,'Bebidas')`, `INSERT INTO stock_items(id,restaurant_id,category_id,name,kind,base_dimension,base_unit,is_tracked,deduction_source) VALUES(20,1,1,'Agua','FINISHED','COUNT','ud',1,'SALE')`, `INSERT INTO stock_item_units(id,restaurant_id,stock_item_id,code,label,factor_to_base,is_default_display) VALUES(21,1,20,'ud','ud',1,1)`, `INSERT INTO stock_levels(restaurant_id,stock_item_id,warehouse_id,qty_base) VALUES(1,20,10,10)`, `INSERT INTO pos_settings(restaurant_id,is_enabled,stock_mode,covers_mode) VALUES(1,1,'LIVE','LIVE')`, `INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(30,1,'Agua',250,1)`, `INSERT INTO pos_product_stock_rules(id,restaurant_id,pos_product_id,stock_item_id,warehouse_id,qty_base_per_sale) VALUES(31,1,30,20,10,1)`, `INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(40,1,'DINE_IN',5,CURDATE(),'LUNCH',2,'OPEN',7,'visit-1')`, `INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,total_gross_cents,opened_by) VALUES(50,1,40,'TPV-1','ticket-1',500,7)`, `INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by) VALUES(60,1,50,30,'Agua',2,250,10,500,'line-1',7)`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	s := NewServer(db, config.Config{BunnyPrivateStorageZone: "private-zone"})
	body := `{"idempotencyKey":"checkout-1","expectedVersion":1,"payments":[{"method":"CASH","amountCents":500,"idempotencyKey":"pay-1"}],"closeVisit":true}`
	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, "/admin/pos/tickets/50/checkout", strings.NewReader(body))
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", "50")
		ctxWithRoute := context.WithValue(request.Context(), chi.RouteCtxKey, routeCtx)
		return request.WithContext(withBOAuth(ctxWithRoute, boAuth{ActiveRestaurantID: 1, Role: "admin", User: boUser{ID: 7}}))
	}
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	bodies := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := httptest.NewRecorder()
			s.handleBOPOSCheckout(recorder, newRequest())
			codes <- recorder.Code
			bodies <- recorder.Body.String()
		}()
	}
	wg.Wait()
	close(codes)
	close(bodies)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("concurrent checkout code=%d bodies=%v", code, <-bodies)
		}
	}
	var movements int
	var qty float64
	var covers int
	if err = db.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1 AND type='SALE'`).Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT qty_base FROM stock_levels WHERE restaurant_id=1 AND stock_item_id=20 AND warehouse_id=10`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT covers FROM stock_affluence_daily WHERE restaurant_id=1 AND service_type='LUNCH'`).Scan(&covers); err != nil {
		t.Fatal(err)
	}
	if movements != 1 || qty != 8 || covers != 2 {
		t.Fatalf("movements=%d qty=%v covers=%d", movements, qty, covers)
	}
	recorder := httptest.NewRecorder()
	s.handleBOPOSCheckout(recorder, newRequest())
	var response map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	if response["duplicate"] != true {
		t.Fatalf("expected duplicate response: %s", recorder.Body.String())
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE restaurant_id=1 AND type='SALE'`).Scan(&movements); err != nil || movements != 1 {
		t.Fatalf("duplicate movements=%d err=%v", movements, err)
	}
	csv, rowCount, err := s.accountingSalesVATCSV(ctx, 1, time.Now().Format("2006-01-02"), time.Now().Format("2006-01-02"))
	if err != nil || rowCount != 1 || !strings.Contains(csv, "500") {
		t.Fatalf("accounting export rows=%d err=%v csv=%s", rowCount, err, csv)
	}
	documentRequest := httptest.NewRequest(http.MethodPost, "/", nil)
	documentID, err := s.saveBOStockDocumentExtractionWithFile(documentRequest, 1, 7, "INVOICE", "UPLOAD", "hash-1", "", stockDocumentExtraction{SupplierName: "Supplier", Confidence: .9}, "stock-documents/1/opaque.pdf", "application/pdf", 123, "invoice.pdf", "2027-01-01")
	if err != nil {
		t.Fatal(err)
	}
	var path, provider string
	if err = db.QueryRow(`SELECT file_path,storage_provider FROM stock_document_scans WHERE restaurant_id=1 AND id=?`, documentID).Scan(&path, &provider); err != nil || path != "stock-documents/1/opaque.pdf" || provider != "BUNNY_PRIVATE" {
		t.Fatalf("document metadata path=%q provider=%q err=%v", path, provider, err)
	}
}
