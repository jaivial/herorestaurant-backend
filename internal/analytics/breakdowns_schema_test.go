package analytics

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// breakdownColumn is one (table, column) reference used by the breakdown
// queries in breakdowns.go. Keeping it explicit here means any future rename
// or drop of a column in the POS/analytics migrations fails CI instead of
// surfacing at runtime.
type breakdownColumn struct {
	table  string
	column string
}

func loadMigrationTables(t *testing.T, files ...string) map[string]map[string]bool {
	t.Helper()
	columns := make(map[string]map[string]bool)
	tableStart := regexp.MustCompile(`(?i)^CREATE TABLE IF NOT EXISTS\s+` + "`" + `?([a-z_]+)` + "`" + `?\s*\(`)
	columnLine := regexp.MustCompile(`^\s{4}` + "`" + `?([a-z_]+)` + "`" + `?\s+[A-Z]`)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		current := ""
		for _, line := range strings.Split(string(raw), "\n") {
			if m := tableStart.FindStringSubmatch(line); m != nil {
				current = m[1]
				if columns[current] == nil {
					columns[current] = make(map[string]bool)
				}
				continue
			}
			if current != "" {
				if m := columnLine.FindStringSubmatch(line); m != nil {
					columns[current][m[1]] = true
				}
				if strings.HasPrefix(line, ") ENGINE=") || strings.HasPrefix(line, ") ENGINE ") {
					current = ""
				}
			}
		}
	}
	return columns
}

func TestBreakdownQueriesReferenceExistingColumns(t *testing.T) {
	columns := loadMigrationTables(t,
		"../db/migrations/064_pos_catalog_and_settings.sql",
		"../db/migrations/065_pos_sales.sql",
		"../db/migrations/080_analytics_foundation.sql",
	)
	want := []breakdownColumn{
		// loadTopItems
		{"pos_ticket_lines", "restaurant_id"},
		{"pos_ticket_lines", "ticket_id"},
		{"pos_ticket_lines", "pos_product_id"},
		{"pos_ticket_lines", "product_name_snapshot"},
		{"pos_ticket_lines", "quantity"},
		{"pos_ticket_lines", "line_total_gross_cents"},
		{"pos_ticket_lines", "status"},
		{"pos_tickets", "restaurant_id"},
		{"pos_tickets", "id"},
		{"pos_tickets", "visit_id"},
		{"pos_tickets", "status"},
		{"pos_visits", "restaurant_id"},
		{"pos_visits", "id"},
		{"pos_visits", "service_date"},
		{"pos_refund_lines", "restaurant_id"},
		{"pos_refund_lines", "ticket_line_id"},
		{"pos_refund_lines", "quantity"},
		{"pos_refund_lines", "refund_id"},
		{"pos_refund_lines", "amount_cents"},
		{"pos_refunds", "restaurant_id"},
		{"pos_refunds", "id"},
		{"pos_refunds", "status"},
		// loadPaymentMethods
		{"pos_payments", "restaurant_id"},
		{"pos_payments", "ticket_id"},
		{"pos_payments", "method"},
		{"pos_payments", "amount_cents"},
		{"pos_payments", "status"},
		// loadDayOfWeek (revenue) + hourly
		{"analytics_daily_rollups", "restaurant_id"},
		{"analytics_daily_rollups", "rollup_date"},
		{"analytics_daily_rollups", "invoiced_revenue_cents"},
		{"analytics_daily_rollups", "pos_revenue_cents"},
		{"pos_visits", "covers"},
		{"pos_visits", "status"},
		{"pos_tickets", "paid_at"},
		{"pos_tickets", "total_gross_cents"},
		// loadRevenueByCategory
		{"pos_products", "restaurant_id"},
		{"pos_products", "id"},
		{"pos_products", "category_id"},
		{"pos_product_categories", "restaurant_id"},
		{"pos_product_categories", "id"},
		{"pos_product_categories", "name"},
	}
	for _, ref := range want {
		tableCols, ok := columns[ref.table]
		if !ok {
			t.Errorf("breakdowns.go queries table %s which does not exist in migrations", ref.table)
			continue
		}
		if !tableCols[ref.column] {
			t.Errorf("breakdowns.go references %s.%s which does not exist in migrations", ref.table, ref.column)
		}
	}
}

func TestPaidTicketStatusValuesMatchTicketEnum(t *testing.T) {
	for _, s := range []string{"PAID", "PARTIALLY_REFUNDED", "REFUNDED"} {
		if !strings.Contains(paidTicketStatusList, s) {
			t.Errorf("paidTicketStatusList missing %q", s)
		}
	}
	text := strings.ToUpper(string(mustReadMigration(t, "../db/migrations/065_pos_sales.sql")))
	if !strings.Contains(text, "PARTIALLY_REFUNDED") {
		t.Fatal("pos_tickets enum no longer contains PARTIALLY_REFUNDED")
	}
}

func TestPaymentMethodLabel(t *testing.T) {
	cases := map[string]string{
		"CASH":  "Efectivo",
		"card":  "Tarjeta",
		"Bank":  "Transferencia",
		"OTHER": "Otros",
		"":      "Otros",
	}
	for in, want := range cases {
		if got := paymentMethodLabel(in); got != want {
			t.Errorf("paymentMethodLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRoundEUR(t *testing.T) {
	cases := map[float64]float64{
		1234.567: 1234.57,
		0.004:    0.0,
		0.005:    0.01,
		-1.235:   -1.24,
		199.999:  200.0,
	}
	for in, want := range cases {
		if got := roundEUR(in); got != want {
			t.Errorf("roundEUR(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestMaxFloat(t *testing.T) {
	if got := maxFloat(0, -5); got != 0 {
		t.Errorf("maxFloat(0,-5) = %v, want 0", got)
	}
	if got := maxFloat(3.5, 2); got != 3.5 {
		t.Errorf("maxFloat(3.5,2) = %v, want 3.5", got)
	}
}

func TestOverviewWithBreakdownsJSONKeys(t *testing.T) {
	payload := OverviewWithBreakdowns{
		TopItems:           []TopItemEntry{{Name: "Café", Quantity: 2, RevenueEUR: 3.5}},
		PaymentMethods:     []PaymentMethodEntry{{Method: "Efectivo", AmountEUR: 10, Count: 1}},
		DayOfWeek:          []DayOfWeekEntry{{Day: "Lun", RevenueEUR: 10, Covers: 2}},
		HourlyDistribution: []HourlyEntry{{Hour: "13:00", Covers: 4, RevenueEUR: 40}},
		RevenueByCategory:  []CategoryRevenueEntry{{Category: "Bebidas", AmountEUR: 40, Percentage: 100}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	// The frontend types read exactly these snake_case keys.
	for _, k := range []string{"topItems", "paymentMethods", "dayOfWeek", "hourlyDistribution", "revenueByCategory"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("OverviewWithBreakdowns JSON missing key %q", k)
		}
	}
}

func mustReadMigration(t *testing.T, file string) []byte {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
