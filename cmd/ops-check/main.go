package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/db"
	"preactvillacarmen/internal/db/migrations"
)

type summary struct {
	StockDifferences, CoverDifferences, OpenVisits, OldVisits, StockExceptions, PartialTickets, NegativeStock, OldShifts int
	// UnresolvableMarginScopes counts scopes whose key no longer points at a
	// real category. They fail silently at resolve time, so the audit is the
	// only place they become visible.
	UnresolvableMarginScopes int
}

func (s summary) issues() int {
	return s.StockDifferences + s.CoverDifferences + s.OldVisits + s.StockExceptions + s.PartialTickets + s.NegativeStock + s.OldShifts + s.UnresolvableMarginScopes
}

func main() {
	cfg := config.Load()
	database, err := db.OpenMySQL(cfg.MySQL)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err = migrations.Apply(ctx, database); err != nil {
		log.Fatal(err)
	}
	var locked int
	if err = database.QueryRowContext(ctx, `SELECT GET_LOCK('villacarmen:ops-check',0)`).Scan(&locked); err != nil || locked != 1 {
		log.Print("ops-check skipped: lock held")
		return
	}
	defer database.ExecContext(context.Background(), `SELECT RELEASE_LOCK('villacarmen:ops-check')`)
	rows, err := database.QueryContext(ctx, `SELECT id FROM restaurants ORDER BY id`)
	if err != nil {
		log.Fatal(err)
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			log.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	failed := false
	for _, id := range ids {
		runID := startRun(ctx, database, id)
		item, err := checkTenant(ctx, database, id)
		status := "OK"
		issues := item.issues()
		if err != nil {
			status = "FAILED"
			failed = true
		} else if issues > 0 {
			status = "WARNING"
			failed = true
		}
		finishRun(ctx, database, runID, status, issues, item, err)
		if status != "OK" {
			log.Printf("ops-check restaurant=%d status=%s issues=%d summary=%+v err=%v", id, status, issues, item, err)
			_ = notify(ctx, database, id, item, issues, err)
		}
	}
	if err = deleteExpiredDocuments(ctx, database, cfg); err != nil {
		failed = true
		log.Printf("ops-check retention failed: %v", err)
	}
	if failed {
		os.Exit(2)
	}
}

func startRun(ctx context.Context, database *sql.DB, restaurantID int) int64 {
	res, err := database.ExecContext(ctx, `INSERT INTO integration_job_runs (restaurant_id,job_key,status,started_at) VALUES (?,'nightly_ops','RUNNING',NOW())`, restaurantID)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}
func finishRun(ctx context.Context, database *sql.DB, id int64, status string, issues int, item summary, runErr error) {
	if id == 0 {
		return
	}
	raw, _ := json.Marshal(item)
	var message any
	if runErr != nil {
		message = runErr.Error()
	}
	_, _ = database.ExecContext(ctx, `UPDATE integration_job_runs SET status=?,issue_count=?,summary_json=?,error_message=?,finished_at=NOW() WHERE id=?`, status, issues, raw, message, id)
}
func checkTenant(ctx context.Context, database *sql.DB, id int) (out summary, err error) {
	queries := []struct {
		sql    string
		target *int
	}{
		{`SELECT COUNT(*) FROM (SELECT keys_.stock_item_id,keys_.warehouse_id FROM (SELECT stock_item_id,warehouse_id FROM stock_levels WHERE restaurant_id=? UNION SELECT stock_item_id,warehouse_id FROM stock_movements WHERE restaurant_id=?) keys_ LEFT JOIN stock_levels l ON l.restaurant_id=? AND l.stock_item_id=keys_.stock_item_id AND l.warehouse_id=keys_.warehouse_id LEFT JOIN (SELECT stock_item_id,warehouse_id,SUM(qty_base) qty_base FROM stock_movements WHERE restaurant_id=? GROUP BY stock_item_id,warehouse_id) m ON m.stock_item_id=keys_.stock_item_id AND m.warehouse_id=keys_.warehouse_id WHERE ABS(COALESCE(l.qty_base,0)-COALESCE(m.qty_base,0))>0.0001) d`, &out.StockDifferences},
		{`SELECT CASE WHEN COALESCE((SELECT covers_mode FROM pos_settings WHERE restaurant_id=?),'MANUAL')='LIVE' THEN (SELECT COUNT(*) FROM (SELECT v.service_date,v.service_type,SUM(CASE WHEN v.status='CLOSED' AND v.channel='DINE_IN' THEN v.covers ELSE 0 END)+COALESCE((SELECT SUM(c.delta_covers) FROM pos_cover_adjustments c WHERE c.restaurant_id=v.restaurant_id AND c.service_date=v.service_date AND c.service_type=v.service_type),0) expected,COALESCE(a.covers,0) actual FROM pos_visits v LEFT JOIN stock_affluence_daily a ON a.restaurant_id=v.restaurant_id AND a.service_date=v.service_date AND a.service_type=v.service_type WHERE v.restaurant_id=? GROUP BY v.restaurant_id,v.service_date,v.service_type,a.covers HAVING expected<>actual) d) ELSE 0 END`, &out.CoverDifferences},
		{`SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND status='OPEN'`, &out.OpenVisits}, {`SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND status='OPEN' AND opened_at<DATE_SUB(NOW(),INTERVAL 12 HOUR)`, &out.OldVisits}, {`SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND status='OPEN'`, &out.StockExceptions}, {`SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND stock_status='PARTIAL'`, &out.PartialTickets}, {`SELECT COUNT(*) FROM pos_stock_anomalies WHERE restaurant_id=? AND status='OPEN'`, &out.NegativeStock}, {`SELECT COUNT(*) FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' AND opened_at<DATE_SUB(NOW(),INTERVAL 24 HOUR)`, &out.OldShifts},
		// A COMIDA_CATEGORY scope stores its key as "platos:12"/"bebidas:12";
		// the two category tables have separate id spaces, so each half is
		// checked against its own table. STOCK_CATEGORY keys are plain ids.
		{`SELECT (
			SELECT COUNT(*) FROM stock_margin_scopes sc
			 WHERE sc.restaurant_id=? AND sc.is_active=1 AND sc.scope_kind='COMIDA_CATEGORY'
			   AND SUBSTRING_INDEX(sc.scope_key,':',1)='platos'
			   AND NOT EXISTS (SELECT 1 FROM comida_plato_categories c
			                    WHERE c.restaurant_id=sc.restaurant_id
			                      AND c.id=CAST(SUBSTRING_INDEX(sc.scope_key,':',-1) AS UNSIGNED))
		) + (
			SELECT COUNT(*) FROM stock_margin_scopes sc
			 WHERE sc.restaurant_id=? AND sc.is_active=1 AND sc.scope_kind='COMIDA_CATEGORY'
			   AND SUBSTRING_INDEX(sc.scope_key,':',1)='bebidas'
			   AND NOT EXISTS (SELECT 1 FROM comida_bebida_categories c
			                    WHERE c.restaurant_id=sc.restaurant_id
			                      AND c.id=CAST(SUBSTRING_INDEX(sc.scope_key,':',-1) AS UNSIGNED))
		) + (
			SELECT COUNT(*) FROM stock_margin_scopes sc
			 WHERE sc.restaurant_id=? AND sc.is_active=1 AND sc.scope_kind='STOCK_CATEGORY'
			   AND NOT EXISTS (SELECT 1 FROM stock_categories c
			                    WHERE c.restaurant_id=sc.restaurant_id
			                      AND c.id=CAST(sc.scope_key AS UNSIGNED))
		)`, &out.UnresolvableMarginScopes}}
	for index, q := range queries {
		args := []any{id}
		if index == 0 {
			args = []any{id, id, id, id}
		} else if index == 1 {
			args = []any{id, id}
		} else if q.target == &out.UnresolvableMarginScopes {
			args = []any{id, id, id}
		}
		if err = database.QueryRowContext(ctx, q.sql, args...).Scan(q.target); err != nil {
			return out, err
		}
	}
	return out, nil
}
func notify(ctx context.Context, database *sql.DB, restaurantID int, item summary, issues int, runErr error) error {
	var webhook sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT n8n_webhook_url FROM restaurant_integrations WHERE restaurant_id=?`, restaurantID).Scan(&webhook); err != nil || !webhook.Valid || strings.TrimSpace(webhook.String) == "" {
		return nil
	}
	payload := map[string]any{"event": "operations.warning", "restaurantId": restaurantID, "occurredAt": time.Now().Format(time.RFC3339), "payload": map[string]any{"issueCount": issues, "summary": item}}
	if runErr != nil {
		payload["error"] = "ops check failed"
	}
	raw, _ := json.Marshal(payload)
	resInsert, insertErr := database.ExecContext(ctx, `INSERT INTO message_deliveries (restaurant_id,channel,event,recipient,payload_json,status) VALUES (?,'webhook','operations.warning',?,?,'pending')`, restaurantID, webhook.String, string(raw))
	if insertErr != nil {
		return insertErr
	}
	deliveryID, _ := resInsert.LastInsertId()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.String, bytes.NewReader(raw))
	if err != nil {
		_, _ = database.ExecContext(ctx, `UPDATE message_deliveries SET status='failed',error=? WHERE id=?`, err.Error(), deliveryID)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		_, _ = database.ExecContext(ctx, `UPDATE message_deliveries SET status='failed',error=? WHERE id=?`, err.Error(), deliveryID)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		sendErr := fmt.Errorf("alert HTTP %d", res.StatusCode)
		_, _ = database.ExecContext(ctx, `UPDATE message_deliveries SET status='failed',error=? WHERE id=?`, sendErr.Error(), deliveryID)
		return sendErr
	}
	_, _ = database.ExecContext(ctx, `UPDATE message_deliveries SET status='sent',sent_at=NOW() WHERE id=?`, deliveryID)
	return nil
}
func deleteExpiredDocuments(ctx context.Context, database *sql.DB, cfg config.Config) error {
	if strings.TrimSpace(cfg.BunnyPrivateStorageZone) == "" || strings.TrimSpace(cfg.BunnyPrivateStorageKey) == "" {
		return nil
	}
	rows, err := database.QueryContext(ctx, `SELECT restaurant_id,id,file_path FROM stock_document_scans WHERE file_path IS NOT NULL AND original_deleted_at IS NULL AND retention_until<CURDATE() ORDER BY id LIMIT 500`)
	if err != nil {
		return err
	}
	type item struct {
		restaurantID int
		id           int64
		path         string
	}
	items := []item{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.restaurantID, &x.id, &x.path); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	for _, x := range items {
		if err = deletePrivate(ctx, cfg, x.path); err != nil {
			return err
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE stock_document_scans SET file_path=NULL,original_deleted_at=NOW() WHERE restaurant_id=? AND id=?`, x.restaurantID, x.id); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO stock_document_access_audit (restaurant_id,document_scan_id,action) VALUES (?,?,'RETENTION_DELETE')`, x.restaurantID, x.id)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
func deletePrivate(ctx context.Context, cfg config.Config, objectPath string) error {
	parts := strings.Split(strings.TrimLeft(objectPath, "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	endpoint := "https://storage.bunnycdn.com/" + url.PathEscape(cfg.BunnyPrivateStorageZone) + "/" + strings.Join(parts, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("AccessKey", cfg.BunnyPrivateStorageKey)
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound || res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	return fmt.Errorf("private delete HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
}
