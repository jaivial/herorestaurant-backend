//go:build integration

package analytics

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Run against a throwaway database with migration 080 applied by setting
// ANALYTICS_TEST_MYSQL_DSN. Source fixtures are intentionally not required:
// the test proves repeated selected-range rebuilds never duplicate facts.
func TestRefreshIsIdempotentWhenDatabaseConfigured(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("ANALYTICS_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tenantID int
	if err = db.QueryRow(`SELECT id FROM restaurants ORDER BY id LIMIT 1`).Scan(&tenantID); err != nil {
		t.Skipf("no restaurant fixture: %v", err)
	}
	service := NewService(db)
	rangeValue := DateRange{From: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)}
	first, err := service.Refresh(context.Background(), tenantID, rangeValue)
	if err != nil {
		t.Fatal(err)
	}
	firstCounts := analyticsCounts(t, db, tenantID, rangeValue)
	second, err := service.Refresh(context.Background(), tenantID, rangeValue)
	if err != nil {
		t.Fatal(err)
	}
	secondCounts := analyticsCounts(t, db, tenantID, rangeValue)
	if first.RunID == second.RunID || firstCounts != secondCounts {
		t.Fatalf("refresh not idempotent: first run=%d counts=%v, second run=%d counts=%v", first.RunID, firstCounts, second.RunID, secondCounts)
	}
}

func analyticsCounts(t *testing.T, db *sql.DB, tenantID int, dateRange DateRange) [3]int64 {
	t.Helper()
	var documents, facts, rollups int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM analytics_sales_documents WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM analytics_stock_facts WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM analytics_daily_rollups WHERE restaurant_id=? AND rollup_date BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To).Scan(&rollups); err != nil {
		t.Fatal(err)
	}
	return [3]int64{documents, facts, rollups}
}
