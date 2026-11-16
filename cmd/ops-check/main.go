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
	"preactvillacarmen/internal/sqldb"
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

// dialect selects the explicit SQL twins below; set from DB_DRIVER at start
// (db_dialect_selection_v1). PG starts only via OpenBusinessForTool gates.
var dialect = sqldb.MySQL

func nativeSQL(mysqlQ, pgQ string) string {
	if dialect == sqldb.Postgres {
		return sqldb.Rebind(sqldb.Postgres, pgQ)
	}
	return mysqlQ
}

func portableSQL(q string) string { return sqldb.Rebind(dialect, q) }

func main() {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// db_dialect_selection_v1: DB_DRIVER selects the engine; PG fails closed
	// until QA-approved and import-verified (no MySQL fallback).
	database, d, err := db.OpenBusinessForTool(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	dialect = d
	if dialect == sqldb.MySQL {
		// MySQL migration runner only; PG schema comes from the import plan.
		if err = migrations.Apply(ctx, database); err != nil {
			log.Fatal(err)
		}
	}
	// Session lock pinned to one connection (lock and release on the same session).
	release, locked, err := sqldb.TryNamedLock(ctx, dialect, database, "villacarmen:ops-check")
	if err != nil || !locked {
		log.Printf("ops-check skipped: lock held (err=%v)", err)
		return
	}
	defer release()
	rows, err := database.QueryContext(ctx, portableSQL(`SELECT id FROM restaurants ORDER BY id`))
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
	id, err := sqldb.InsertID(ctx, dialect, database, nativeSQL(`INSERT INTO integration_job_runs (restaurant_id,job_key,status,started_at) VALUES (?,'nightly_ops','RUNNING',NOW())`, `INSERT INTO integration_job_runs (restaurant_id,job_key,status,started_at) VALUES (?,'nightly_ops','RUNNING',date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC'))`), "id", restaurantID)
	if err != nil {
		return 0
	}
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
	_, _ = database.ExecContext(ctx, nativeSQL(`UPDATE integration_job_runs SET status=?,issue_count=?,summary_json=?,error_message=?,finished_at=NOW() WHERE id=?`, `UPDATE integration_job_runs SET status=?,issue_count=?,summary_json=CAST(? AS jsonb),error_message=?,finished_at=date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC') WHERE id=?`), status, issues, raw, message, id)
}
func checkTenant(ctx context.Context, database *sql.DB, id int) (out summary, err error) {
	queries := []struct {
		sql    string
		target *int
	}{
		{sqlFragment(`SELECT COUNT(*) FROM (SELECT keys_.stock_item_id,keys_.warehouse_id FROM (SELECT stock_item_id,warehouse_id FROM stock_levels WHERE restaurant_id=? UNION SELECT stock_item_id,warehouse_id FROM stock_movements WHERE restaurant_id=?) keys_ LEFT JOIN stock_levels l ON l.restaurant_id=? AND l.stock_item_id=keys_.stock_item_id AND l.warehouse_id=keys_.warehouse_id LEFT JOIN (SELECT stock_item_id,warehouse_id,SUM(qty_base) qty_base FROM stock_movements WHERE restaurant_id=? GROUP BY stock_item_id,warehouse_id) m ON m.stock_item_id=keys_.stock_item_id AND m.warehouse_id=keys_.warehouse_id WHERE ABS(COALESCE(l.qty_base,0)-COALESCE(m.qty_base,0))>0.0001) d`, `SELECT COUNT(*) FROM (SELECT keys_.stock_item_id,keys_.warehouse_id FROM (SELECT stock_item_id,warehouse_id FROM stock_levels WHERE restaurant_id=? UNION SELECT stock_item_id,warehouse_id FROM stock_movements WHERE restaurant_id=?) keys_ LEFT JOIN stock_levels l ON l.restaurant_id=? AND l.stock_item_id=keys_.stock_item_id AND l.warehouse_id=keys_.warehouse_id LEFT JOIN (SELECT stock_item_id,warehouse_id,SUM(qty_base) qty_base FROM stock_movements WHERE restaurant_id=? GROUP BY stock_item_id,warehouse_id) m ON m.stock_item_id=keys_.stock_item_id AND m.warehouse_id=keys_.warehouse_id WHERE ABS(COALESCE(l.qty_base,0)-COALESCE(m.qty_base,0))>0.0001) d`), &out.StockDifferences},
		{sqlFragment(`SELECT CASE WHEN COALESCE((SELECT covers_mode FROM pos_settings WHERE restaurant_id=?),'MANUAL')='LIVE' THEN (SELECT COUNT(*) FROM (SELECT v.service_date,v.service_type,SUM(CASE WHEN v.status='CLOSED' AND v.channel='DINE_IN' THEN v.covers ELSE 0 END)+COALESCE((SELECT SUM(c.delta_covers) FROM pos_cover_adjustments c WHERE c.restaurant_id=v.restaurant_id AND c.service_date=v.service_date AND c.service_type=v.service_type),0) expected,COALESCE(a.covers,0) actual FROM pos_visits v LEFT JOIN stock_affluence_daily a ON a.restaurant_id=v.restaurant_id AND a.service_date=v.service_date AND a.service_type=v.service_type WHERE v.restaurant_id=? GROUP BY v.restaurant_id,v.service_date,v.service_type,a.covers HAVING expected<>actual) d) ELSE 0 END`, `SELECT CASE WHEN COALESCE((SELECT CAST(covers_mode AS text) FROM pos_settings WHERE restaurant_id=?),'MANUAL')='LIVE' THEN (SELECT COUNT(*) FROM (SELECT v.service_date,v.service_type,SUM(CASE WHEN v.status='CLOSED' AND v.channel='DINE_IN' THEN v.covers ELSE 0 END)+COALESCE((SELECT SUM(c.delta_covers) FROM pos_cover_adjustments c WHERE c.restaurant_id=v.restaurant_id AND c.service_date=v.service_date AND CAST(c.service_type AS text)=CAST(v.service_type AS text)),0) expected,COALESCE(a.covers,0) actual FROM pos_visits v LEFT JOIN stock_affluence_daily a ON a.restaurant_id=v.restaurant_id AND a.service_date=v.service_date AND CAST(a.service_type AS text)=CAST(v.service_type AS text) WHERE v.restaurant_id=? GROUP BY v.restaurant_id,v.service_date,v.service_type,a.covers HAVING SUM(CASE WHEN v.status='CLOSED' AND v.channel='DINE_IN' THEN v.covers ELSE 0 END)+COALESCE((SELECT SUM(c.delta_covers) FROM pos_cover_adjustments c WHERE c.restaurant_id=v.restaurant_id AND c.service_date=v.service_date AND CAST(c.service_type AS text)=CAST(v.service_type AS text)),0)<>COALESCE(a.covers,0)) d) ELSE 0 END`), &out.CoverDifferences},
		{sqlFragment(`SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND status='OPEN'`, `SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND status='OPEN'`), &out.OpenVisits}, {sqlFragment(`SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND status='OPEN' AND opened_at<DATE_SUB(NOW(),INTERVAL 12 HOUR)`, `SELECT COUNT(*) FROM pos_visits WHERE restaurant_id=? AND status='OPEN' AND opened_at<date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC')-INTERVAL '12 hours'`), &out.OldVisits}, {sqlFragment(`SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND status='OPEN'`, `SELECT COUNT(*) FROM pos_stock_exceptions WHERE restaurant_id=? AND status='OPEN'`), &out.StockExceptions}, {sqlFragment(`SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND stock_status='PARTIAL'`, `SELECT COUNT(*) FROM pos_tickets WHERE restaurant_id=? AND stock_status='PARTIAL'`), &out.PartialTickets}, {sqlFragment(`SELECT COUNT(*) FROM pos_stock_anomalies WHERE restaurant_id=? AND status='OPEN'`, `SELECT COUNT(*) FROM pos_stock_anomalies WHERE restaurant_id=? AND status='OPEN'`), &out.NegativeStock}, {sqlFragment(`SELECT COUNT(*) FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' AND opened_at<DATE_SUB(NOW(),INTERVAL 24 HOUR)`, `SELECT COUNT(*) FROM pos_shifts WHERE restaurant_id=? AND status='OPEN' AND opened_at<date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC')-INTERVAL '24 hours'`), &out.OldShifts},
		// A COMIDA_CATEGORY scope stores its key as "platos:12"/"bebidas:12";
		// the two category tables have separate id spaces, so each half is
		// checked against its own table. STOCK_CATEGORY keys are plain ids.
		{sqlFragment(`SELECT (
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
		)`, `SELECT (
			SELECT COUNT(*) FROM stock_margin_scopes sc
			 WHERE sc.restaurant_id=? AND sc.is_active=1 AND sc.scope_kind='COMIDA_CATEGORY'
			   AND app.norm(split_part(sc.scope_key,':',1))='platos'
			   AND NOT EXISTS (SELECT 1 FROM comida_plato_categories c
			                    WHERE c.restaurant_id=sc.restaurant_id
			                      AND c.id=CAST(COALESCE(substring((regexp_match(sc.scope_key, '([^:]*)$'))[1] from '^\s*([0-9]+)'),'0') AS numeric))
		) + (
			SELECT COUNT(*) FROM stock_margin_scopes sc
			 WHERE sc.restaurant_id=? AND sc.is_active=1 AND sc.scope_kind='COMIDA_CATEGORY'
			   AND app.norm(split_part(sc.scope_key,':',1))='bebidas'
			   AND NOT EXISTS (SELECT 1 FROM comida_bebida_categories c
			                    WHERE c.restaurant_id=sc.restaurant_id
			                      AND c.id=CAST(COALESCE(substring((regexp_match(sc.scope_key, '([^:]*)$'))[1] from '^\s*([0-9]+)'),'0') AS numeric))
		) + (
			SELECT COUNT(*) FROM stock_margin_scopes sc
			 WHERE sc.restaurant_id=? AND sc.is_active=1 AND sc.scope_kind='STOCK_CATEGORY'
			   AND NOT EXISTS (SELECT 1 FROM stock_categories c
			                    WHERE c.restaurant_id=sc.restaurant_id
			                      AND c.id=CAST(COALESCE(substring(sc.scope_key from '^\s*([0-9]+)'),'0') AS numeric))
		)`), &out.UnresolvableMarginScopes}}
	for index, q := range queries {
		args := []any{id}
		if index == 0 {
			args = []any{id, id, id, id}
		} else if index == 1 {
			args = []any{id, id}
		} else if q.target == &out.UnresolvableMarginScopes {
			args = []any{id, id, id}
		}
		if err = database.QueryRowContext(ctx, portableSQL(q.sql), args...).Scan(q.target); err != nil {
			return out, err
		}
	}
	return out, nil
}

// sqlFragment picks a dialect twin without rebinding (the full statement is
// rebound once at the call site).
func sqlFragment(mysqlQ, pgQ string) string {
	if dialect == sqldb.Postgres {
		return pgQ
	}
	return mysqlQ
}

func notify(ctx context.Context, database *sql.DB, restaurantID int, item summary, issues int, runErr error) error {
	var webhook sql.NullString
	if err := database.QueryRowContext(ctx, portableSQL(`SELECT n8n_webhook_url FROM restaurant_integrations WHERE restaurant_id=?`), restaurantID).Scan(&webhook); err != nil || !webhook.Valid || strings.TrimSpace(webhook.String) == "" {
		return nil
	}
	payload := map[string]any{"event": "operations.warning", "restaurantId": restaurantID, "occurredAt": time.Now().Format(time.RFC3339), "payload": map[string]any{"issueCount": issues, "summary": item}}
	if runErr != nil {
		payload["error"] = "ops check failed"
	}
	raw, _ := json.Marshal(payload)
	deliveryID, insertErr := sqldb.InsertID(ctx, dialect, database, nativeSQL(`INSERT INTO message_deliveries (restaurant_id,channel,event,recipient,payload_json,status) VALUES (?,'webhook','operations.warning',?,?,'pending')`, `INSERT INTO message_deliveries (restaurant_id,channel,event,recipient,payload_json,status) VALUES (?,'webhook','operations.warning',?,CAST(? AS jsonb),'pending')`), "id", restaurantID, webhook.String, string(raw))
	if insertErr != nil {
		return insertErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.String, bytes.NewReader(raw))
	if err != nil {
		_, _ = database.ExecContext(ctx, portableSQL(`UPDATE message_deliveries SET status='failed',error=? WHERE id=?`), err.Error(), deliveryID)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		_, _ = database.ExecContext(ctx, portableSQL(`UPDATE message_deliveries SET status='failed',error=? WHERE id=?`), err.Error(), deliveryID)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		sendErr := fmt.Errorf("alert HTTP %d", res.StatusCode)
		_, _ = database.ExecContext(ctx, portableSQL(`UPDATE message_deliveries SET status='failed',error=? WHERE id=?`), sendErr.Error(), deliveryID)
		return sendErr
	}
	_, _ = database.ExecContext(ctx, nativeSQL(`UPDATE message_deliveries SET status='sent',sent_at=NOW() WHERE id=?`, `UPDATE message_deliveries SET status='sent',sent_at=date_trunc('second', statement_timestamp() AT TIME ZONE 'UTC') WHERE id=?`), deliveryID)
	return nil
}
func deleteExpiredDocuments(ctx context.Context, database *sql.DB, cfg config.Config) error {
	if strings.TrimSpace(cfg.BunnyPrivateStorageZone) == "" || strings.TrimSpace(cfg.BunnyPrivateStorageKey) == "" {
		return nil
	}
	rows, err := database.QueryContext(ctx, nativeSQL(`SELECT restaurant_id,id,file_path FROM stock_document_scans WHERE file_path IS NOT NULL AND original_deleted_at IS NULL AND retention_until<CURDATE() ORDER BY id LIMIT 500`, `SELECT restaurant_id,id,file_path FROM stock_document_scans WHERE file_path IS NOT NULL AND original_deleted_at IS NULL AND retention_until<CAST(statement_timestamp() AT TIME ZONE 'UTC' AS date) ORDER BY id LIMIT 500`))
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
		if _, err = tx.ExecContext(ctx, nativeSQL(`UPDATE stock_document_scans SET file_path=NULL,original_deleted_at=NOW() WHERE restaurant_id=? AND id=?`, `UPDATE stock_document_scans SET file_path=NULL,original_deleted_at=date_trunc('second', statement_timestamp()) WHERE restaurant_id=? AND id=?`), x.restaurantID, x.id); err == nil {
			_, err = tx.ExecContext(ctx, portableSQL(`INSERT INTO stock_document_access_audit (restaurant_id,document_scan_id,action) VALUES (?,?,'RETENTION_DELETE')`), x.restaurantID, x.id)
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
