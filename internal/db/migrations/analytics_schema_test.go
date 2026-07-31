package migrations

import (
	"strings"
	"testing"
)

func TestAnalyticsFoundationUsesNewTablesOnly(t *testing.T) {
	source, err := migrationFS.ReadFile("080_analytics_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToUpper(string(source))
	if strings.Contains(text, "ALTER TABLE") || strings.Contains(text, "DROP TABLE") {
		t.Fatal("analytics foundation must not alter or drop existing tables")
	}
	for _, table := range []string{
		"analytics_sync_runs",
		"analytics_customers",
		"analytics_customer_sources",
		"analytics_sales_documents",
		"analytics_sales_lines",
		"analytics_stock_facts",
		"analytics_daily_rollups",
	} {
		if !strings.Contains(text, "CREATE TABLE IF NOT EXISTS "+strings.ToUpper(table)) {
			t.Fatalf("migration missing table %s", table)
		}
	}
}
