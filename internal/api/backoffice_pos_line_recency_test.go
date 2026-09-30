package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"preactvillacarmen/internal/config"
)

// posLineRecencyDB mirrors the POS integration fixture: one open visit with a
// ticket and a single line.
func posLineRecencyDB(t *testing.T) (*sql.DB, *Server) {
	t.Helper()
	dsn := os.Getenv("POS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("POS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// These tests share one MySQL schema with the other POS integration tests,
	// and some of those clean up in an order that trips the FK from
	// pos_ticket_lines to pos_products. Leaving our rows behind would break
	// them, so drop the rows we created on the way out.
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_ticket_lines WHERE restaurant_id=1`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_tickets WHERE restaurant_id=1`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM pos_visits WHERE restaurant_id=1`)
		db.Close()
	})
	statements := []string{
		`DELETE FROM stock_document_scans`, `DELETE FROM stock_affluence_daily`, `DELETE FROM pos_payments`, `DELETE FROM pos_ticket_line_stock`, `DELETE FROM pos_stock_exceptions`, `DELETE FROM pos_ticket_lines`, `DELETE FROM pos_tickets`, `DELETE FROM pos_visits`, `DELETE FROM pos_product_stock_rules`, `DELETE FROM pos_products`, `DELETE FROM pos_settings`, `DELETE FROM stock_movements`, `DELETE FROM stock_levels`, `DELETE FROM stock_item_units`, `DELETE FROM stock_items`, `DELETE FROM stock_warehouses`, `DELETE FROM stock_categories`, `DELETE FROM restaurant_tables`, `DELETE FROM restaurants`, `DELETE FROM bo_users`,
		`INSERT INTO restaurants(id,slug,name) VALUES(1,'pos-line-test','POS Line Test')`,
		`INSERT INTO bo_users(id,email,name,password_hash) VALUES(7,'posline@test.local','POS Line','x')`,
		`INSERT INTO restaurant_tables(id,restaurant_id,numero_mesa,name,capacity,display_order,is_active) VALUES(5,1,1,'Mesa 1',4,0,1)`,
		`INSERT INTO pos_settings(restaurant_id,is_enabled,stock_mode,covers_mode) VALUES(1,1,'OFF','MANUAL')`,
		`INSERT INTO pos_products(id,restaurant_id,name,price_gross_cents,is_active) VALUES(30,1,'Agua',250,1)`,
		`INSERT INTO pos_visits(id,restaurant_id,channel,table_id,service_date,service_type,covers,status,opened_by,open_idempotency_key) VALUES(40,1,'DINE_IN',5,CURDATE(),'LUNCH',2,'OPEN',7,'line-visit')`,
		`INSERT INTO pos_tickets(id,restaurant_id,visit_id,ticket_number,creation_idempotency_key,total_gross_cents,opened_by) VALUES(50,1,40,'TPV-1','line-ticket',500,7)`,
		`INSERT INTO pos_ticket_lines(id,restaurant_id,ticket_id,pos_product_id,product_name_snapshot,quantity,unit_price_gross_cents,vat_rate_snapshot,line_total_gross_cents,idempotency_key,created_by) VALUES(60,1,50,30,'Agua',2,250,10,500,'line-1',7)`,
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return db, NewServer(db, config.Config{BunnyPrivateStorageZone: "private-zone"})
}

func lineUpdatedAt(t *testing.T, s *Server, lineID int64) (time.Time, bool) {
	t.Helper()
	ticket, err := s.loadPOSTicket(context.Background(), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	lines, _ := ticket["lines"].([]map[string]any)
	for _, entry := range lines {
		if id, _ := entry["id"].(int64); id != lineID {
			continue
		}
		raw, ok := entry["updatedAt"]
		if !ok {
			return time.Time{}, false
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var parsed time.Time
		if err = json.Unmarshal(encoded, &parsed); err != nil {
			t.Fatalf("updatedAt is not a decodable time: %v", err)
		}
		return parsed, true
	}
	t.Fatalf("line %d missing from the payload", lineID)
	return time.Time{}, false
}

// The sell screen orders the ticket lines by the most recent change, so the
// line payload has to carry the row's updated_at.
func TestPOSTicketLineCarriesUpdatedAt(t *testing.T) {
	_, s := posLineRecencyDB(t)

	first, ok := lineUpdatedAt(t, s, 60)
	if !ok {
		t.Fatal("line payload must include updatedAt")
	}
	if first.IsZero() {
		t.Fatal("updatedAt must be a real timestamp")
	}

	// Move the row's clock forward and edit it, the way a quantity change does.
	if _, err := s.db.ExecContext(context.Background(), `UPDATE pos_ticket_lines SET updated_at=DATE_SUB(NOW(), INTERVAL 1 MINUTE) WHERE restaurant_id=1 AND id=60`); err != nil {
		t.Fatal(err)
	}
	backdated, _ := lineUpdatedAt(t, s, 60)
	if _, err := s.db.ExecContext(context.Background(), `UPDATE pos_ticket_lines SET quantity=3,updated_at=NOW() WHERE restaurant_id=1 AND id=60`); err != nil {
		t.Fatal(err)
	}
	edited, ok := lineUpdatedAt(t, s, 60)
	if !ok {
		t.Fatal("updatedAt must survive an edit")
	}
	if !edited.After(backdated) {
		t.Fatalf("updatedAt must advance after an edit: %v then %v", backdated, edited)
	}
}

// A ticket with no lines must still serialise, with an empty list rather than
// a null, so the client can order an empty panel without a guard.
func TestPOSTicketWithoutLinesSerialises(t *testing.T) {
	db, s := posLineRecencyDB(t)
	if _, err := db.ExecContext(context.Background(), `DELETE FROM pos_ticket_lines WHERE restaurant_id=1`); err != nil {
		t.Fatal(err)
	}
	ticket, err := s.loadPOSTicket(context.Background(), 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	lines, ok := ticket["lines"].([]map[string]any)
	if !ok || lines == nil {
		t.Fatalf("lines must be an empty slice, got %T", ticket["lines"])
	}
	if len(lines) != 0 {
		t.Fatalf("expected no lines, got %d", len(lines))
	}
}
