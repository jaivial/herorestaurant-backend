package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	GranularityDay     = "day"
	GranularityWeek    = "week"
	GranularityMonth   = "month"
	GranularityQuarter = "quarter"
	GranularityYear    = "year"
)

type CustomerIdentityInput struct {
	Email string
	Phone string
	TaxID string
	Name  string
}

// NormalizeCustomerIdentity deliberately uses one stable identifier. A source
// without any usable identifier is left unidentified instead of inventing one.
func NormalizeCustomerIdentity(in CustomerIdentityInput) string {
	if value := normalizeEmail(in.Email); value != "" {
		return "email:" + value
	}
	if value := normalizePhone(in.Phone); value != "" {
		return "phone:" + value
	}
	if value := normalizeTaxID(in.TaxID); value != "" {
		return "tax:" + value
	}
	if value := normalizeName(in.Name); value != "" {
		return "name:" + value
	}
	return ""
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizePhone(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	result := b.String()
	return strings.TrimPrefix(result, "00")
}

func normalizeTaxID(value string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

type CostCandidates struct {
	MovementTotal  *float64
	MovementUnit   *float64
	EffectivePrice *float64
	AverageLevel   *float64
	Quantity       float64
}

func Float64Value(value float64) *float64 { return &value }

func ResolveCost(in CostCandidates) (float64, bool) {
	quantity := math.Abs(in.Quantity)
	if in.MovementTotal != nil {
		return math.Abs(*in.MovementTotal), true
	}
	if in.MovementUnit != nil {
		return math.Abs(*in.MovementUnit) * quantity, true
	}
	if in.EffectivePrice != nil {
		return math.Abs(*in.EffectivePrice) * quantity, true
	}
	if in.AverageLevel != nil {
		return math.Abs(*in.AverageLevel) * quantity, true
	}
	return 0, false
}

type DailyFact struct {
	TenantID    int
	Date        time.Time
	Source      string
	Status      string
	GrossCents  int64
	RefundCents int64
	CustomerID  int64

	FactType    string
	WasteReason string
	Quantity    float64
	CostCents   int64
	CostKnown   bool
}

type WasteBreakdownValue struct {
	Quantity        float64 `json:"quantity"`
	KnownCostCents  int64   `json:"knownCostCents"`
	UnknownQuantity float64 `json:"unknownQuantity"`
}

type DailyAggregate struct {
	InvoicedRevenueCents int64
	POSRevenueCents      int64
	POSRefundCents       int64
	IdentifiedPeople     int

	COGSKnownCostCents           int64
	COGSQuantity                 float64
	COGSKnownQuantity            float64
	StockPurchaseKnownCostCents  int64
	StockPurchaseQuantity        float64
	StockPurchaseKnownQuantity   float64
	StockPurchaseUnknownQuantity float64
	StockReturnKnownCostCents    int64
	StockReturnQuantity          float64
	StockReturnKnownQuantity     float64
	WasteKnownCostCents          int64
	WasteQuantity                float64
	WasteUnknownQuantity         float64
	CostCoveragePercent          float64

	WasteBreakdown map[string]WasteBreakdownValue
}

func AggregateDailyFacts(facts []DailyFact, tenantID int, from, to time.Time) DailyAggregate {
	result := DailyAggregate{WasteBreakdown: make(map[string]WasteBreakdownValue)}
	customers := make(map[int64]struct{})
	for _, fact := range facts {
		date := dateOnly(fact.Date)
		if fact.TenantID != tenantID || date.Before(dateOnly(from)) || date.After(dateOnly(to)) {
			continue
		}
		switch fact.Source {
		case "INVOICE":
			if strings.EqualFold(strings.TrimSpace(fact.Status), "borrador") {
				continue
			}
			result.InvoicedRevenueCents += fact.GrossCents
		case "POS":
			if !includedPOSTicketStatus(fact.Status) {
				continue
			}
			result.POSRevenueCents += fact.GrossCents - fact.RefundCents
			result.POSRefundCents += fact.RefundCents
		default:
			if fact.Source != "STOCK" {
				continue
			}
		}
		if (fact.Source == "INVOICE" || fact.Source == "POS") && fact.CustomerID > 0 {
			customers[fact.CustomerID] = struct{}{}
		}
		if fact.Source != "STOCK" {
			continue
		}
		if fact.FactType == "PURCHASE" {
			result.StockPurchaseQuantity += fact.Quantity
			if fact.CostKnown {
				result.StockPurchaseKnownQuantity += fact.Quantity
				result.StockPurchaseKnownCostCents += fact.CostCents
			} else {
				result.StockPurchaseUnknownQuantity += fact.Quantity
			}
			continue
		}
		if fact.FactType == "RETURN" {
			result.StockReturnQuantity += fact.Quantity
			if fact.CostKnown {
				result.StockReturnKnownQuantity += fact.Quantity
				result.StockReturnKnownCostCents += fact.CostCents
			}
			continue
		}
		if fact.FactType == "SALE" || fact.FactType == "PRODUCTION_OUT" {
			result.COGSQuantity += fact.Quantity
			if fact.CostKnown {
				result.COGSKnownQuantity += fact.Quantity
				result.COGSKnownCostCents += fact.CostCents
			}
		}
		if fact.FactType != "WASTE" && fact.FactType != "RECIPE_WASTE" && fact.FactType != "PRODUCTION_VARIANCE" {
			continue
		}
		result.WasteQuantity += fact.Quantity
		if !fact.CostKnown {
			result.WasteUnknownQuantity += fact.Quantity
		}
		if fact.CostKnown {
			result.WasteKnownCostCents += fact.CostCents
		}
		reason := fact.WasteReason
		if reason == "" {
			reason = fact.FactType
		}
		breakdown := result.WasteBreakdown[reason]
		breakdown.Quantity += fact.Quantity
		breakdown.KnownCostCents += fact.CostCents
		if !fact.CostKnown {
			breakdown.UnknownQuantity += fact.Quantity
		}
		result.WasteBreakdown[reason] = breakdown
	}
	result.IdentifiedPeople = len(customers)
	result = normalizeCOGSReturns(result)
	return result
}

func normalizeCOGSReturns(result DailyAggregate) DailyAggregate {
	if result.StockReturnQuantity <= 0 {
		if result.COGSQuantity > 0 {
			result.CostCoveragePercent = result.COGSKnownQuantity / result.COGSQuantity * 100
		}
		return result
	}
	result.COGSQuantity = math.Max(0, result.COGSQuantity-result.StockReturnQuantity)
	result.COGSKnownQuantity = math.Max(0, result.COGSKnownQuantity-math.Min(result.COGSKnownQuantity, result.StockReturnKnownQuantity))
	result.COGSKnownCostCents = maxInt64(0, result.COGSKnownCostCents-result.StockReturnKnownCostCents)
	if result.COGSQuantity > 0 {
		result.CostCoveragePercent = result.COGSKnownQuantity / result.COGSQuantity * 100
	} else {
		result.CostCoveragePercent = 0
	}
	return result
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func includedPOSTicketStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PAID", "PARTIALLY_REFUNDED", "REFUNDED":
		return true
	default:
		return false
	}
}

type Granularity string

type Period struct {
	Start time.Time
	End   time.Time
}

func BuildPeriods(from, to time.Time, granularity Granularity) ([]Period, error) {
	from, to = dateOnly(from), dateOnly(to)
	if from.IsZero() || to.IsZero() || from.After(to) {
		return nil, errors.New("invalid analytics date range")
	}
	switch granularity {
	case GranularityDay, GranularityWeek, GranularityMonth, GranularityQuarter, GranularityYear:
	default:
		return nil, fmt.Errorf("unsupported analytics granularity %q", granularity)
	}
	periods := make([]Period, 0)
	start := periodStart(from, granularity)
	for !start.After(to) {
		next := addPeriod(start, granularity)
		end := next.AddDate(0, 0, -1)
		if end.After(to) {
			end = to
		}
		periods = append(periods, Period{Start: start, End: end})
		start = next
	}
	return periods, nil
}

func PreviousComparisonRange(from, to time.Time) Period {
	from, to = dateOnly(from), dateOnly(to)
	days := int(to.Sub(from).Hours()/24) + 1
	previousTo := from.AddDate(0, 0, -1)
	return Period{Start: previousTo.AddDate(0, 0, -(days - 1)), End: previousTo}
}

func dateOnly(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func periodStart(value time.Time, granularity Granularity) time.Time {
	value = dateOnly(value)
	switch granularity {
	case GranularityWeek:
		weekday := int(value.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return value.AddDate(0, 0, -(weekday - 1))
	case GranularityMonth:
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	case GranularityQuarter:
		month := ((int(value.Month())-1)/3)*3 + 1
		return time.Date(value.Year(), time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	case GranularityYear:
		return time.Date(value.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		return value
	}
}

func addPeriod(value time.Time, granularity Granularity) time.Time {
	switch granularity {
	case GranularityWeek:
		return value.AddDate(0, 0, 7)
	case GranularityMonth:
		return value.AddDate(0, 1, 0)
	case GranularityQuarter:
		return value.AddDate(0, 3, 0)
	case GranularityYear:
		return value.AddDate(1, 0, 0)
	default:
		return value.AddDate(0, 0, 1)
	}
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

type DateRange struct {
	From time.Time
	To   time.Time
}

type RefreshResult struct {
	RunID       int64
	RowsWritten int
	From        string
	To          string
}

func (s *Service) Refresh(ctx context.Context, tenantID int, dateRange DateRange) (result RefreshResult, err error) {
	ctx, cancel := analyticsContext(ctx, 90*time.Second)
	defer cancel()
	dateRange.From, dateRange.To = dateOnly(dateRange.From), dateOnly(dateRange.To)
	if tenantID <= 0 || dateRange.From.IsZero() || dateRange.To.IsZero() || dateRange.From.After(dateRange.To) {
		return result, errors.New("invalid analytics refresh range")
	}
	runResult, err := s.db.ExecContext(ctx, `
		INSERT INTO analytics_sync_runs (restaurant_id,from_date,to_date,status)
		VALUES (?,?,?,'RUNNING')
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return result, err
	}
	runID, err := runResult.LastInsertId()
	if err != nil {
		return result, err
	}
	result.RunID = runID
	result.From = dateRange.From.Format("2006-01-02")
	result.To = dateRange.To.Format("2006-01-02")

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.markRunFailed(ctx, runID, err)
		return result, err
	}
	committed := false
	defer func() {
		if err != nil && !committed {
			_ = tx.Rollback()
			s.markRunFailed(ctx, runID, err)
		}
	}()

	if _, err = tx.ExecContext(ctx, `
		DELETE FROM analytics_sales_documents
		WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ?
	`, tenantID, dateRange.From, dateRange.To); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM analytics_stock_facts WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM analytics_daily_rollups WHERE restaurant_id=? AND rollup_date BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To); err != nil {
		return result, err
	}
	// Customer source rows are a rebuild aid, not an aggregate input. Replacing
	// them prevents a changed source identity from leaving stale aliases behind.
	if _, err = tx.ExecContext(ctx, `DELETE FROM analytics_customer_sources WHERE restaurant_id=?`, tenantID); err != nil {
		return result, err
	}

	result.RowsWritten, err = s.refreshInvoices(ctx, tx, tenantID, dateRange)
	if err != nil {
		return result, err
	}
	var rows int
	rows, err = s.refreshPOS(ctx, tx, tenantID, dateRange)
	result.RowsWritten += rows
	if err != nil {
		return result, err
	}
	rows, err = s.refreshStock(ctx, tx, tenantID, dateRange)
	result.RowsWritten += rows
	if err != nil {
		return result, err
	}
	if err = s.rebuildRollups(ctx, tx, tenantID, dateRange); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	committed = true
	_, err = s.db.ExecContext(ctx, `UPDATE analytics_sync_runs SET status='COMPLETED',rows_written=?,completed_at=NOW() WHERE id=?`, result.RowsWritten, runID)
	return result, err
}

func (s *Service) markRunFailed(ctx context.Context, runID int64, cause error) {
	message := cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE analytics_sync_runs SET status='FAILED',error_message=?,completed_at=NOW() WHERE id=?`, message, runID)
}

func (s *Service) refreshInvoices(ctx context.Context, tx *sql.Tx, tenantID int, dateRange DateRange) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id,i.invoice_date,COALESCE(i.status,''),COALESCE(i.currency,'EUR'),
		       COALESCE(i.total,i.amount),COALESCE(i.customer_name,''),COALESCE(i.customer_surname,''),
		       COALESCE(i.customer_email,''),COALESCE(i.customer_phone,''),COALESCE(i.customer_dni_cif,'')
		FROM invoices i
		WHERE i.restaurant_id=? AND i.invoice_date BETWEEN ? AND ? AND i.status <> 'borrador'
		ORDER BY i.id
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return 0, err
	}
	type invoiceSource struct {
		invoiceID                          int64
		date                               time.Time
		status, currency                   string
		amount                             float64
		name, surname, email, phone, taxID string
	}
	invoices := make([]invoiceSource, 0)
	for rows.Next() {
		var (
			invoiceID                                            int64
			date                                                 time.Time
			amount                                               float64
			status, currency, name, surname, email, phone, taxID string
		)
		if err := rows.Scan(&invoiceID, &date, &status, &currency, &amount, &name, &surname, &email, &phone, &taxID); err != nil {
			rows.Close()
			return 0, err
		}
		invoices = append(invoices, invoiceSource{invoiceID: invoiceID, date: date, status: status, currency: currency, amount: amount, name: name, surname: surname, email: email, phone: phone, taxID: taxID})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	written := 0
	for _, invoice := range invoices {
		invoiceID, date, status, currency, amount := invoice.invoiceID, invoice.date, invoice.status, invoice.currency, invoice.amount
		name, surname, email, phone, taxID := invoice.name, invoice.surname, invoice.email, invoice.phone, invoice.taxID
		customerID, err := s.upsertCustomer(ctx, tx, tenantID, CustomerIdentityInput{Email: email, Phone: phone, TaxID: taxID, Name: strings.TrimSpace(name + " " + surname)}, date)
		if err != nil {
			return written, err
		}
		documentID, err := s.upsertDocument(ctx, tx, tenantID, "INVOICE", invoiceID, customerID, date, status, currency, cents(amount), 0)
		if err != nil {
			return written, err
		}
		if customerID != 0 {
			if err = s.upsertCustomerSource(ctx, tx, tenantID, customerID, "INVOICE", invoiceID); err != nil {
				return written, err
			}
		}
		lineRows, err := tx.QueryContext(ctx, `SELECT id,description,quantity,line_total FROM invoice_line_items WHERE invoice_id=? ORDER BY id`, invoiceID)
		if err != nil {
			return written, err
		}
		type invoiceLineSource struct {
			lineID              int64
			description         string
			quantity, lineTotal float64
		}
		lines := make([]invoiceLineSource, 0)
		for lineRows.Next() {
			var line invoiceLineSource
			if err = lineRows.Scan(&line.lineID, &line.description, &line.quantity, &line.lineTotal); err != nil {
				lineRows.Close()
				return written, err
			}
			lines = append(lines, line)
		}
		if err = lineRows.Err(); err != nil {
			lineRows.Close()
			return written, err
		}
		lineRows.Close()
		for _, line := range lines {
			if err = s.upsertSalesLine(ctx, tx, tenantID, documentID, line.lineID, line.description, line.quantity, cents(line.lineTotal), 0); err != nil {
				return written, err
			}
			written++
		}
		written++
	}
	return written, nil
}

func (s *Service) refreshPOS(ctx context.Context, tx *sql.Tx, tenantID int, dateRange DateRange) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id,t.visit_id,t.status,t.total_gross_cents,t.refunded_cents,v.service_date,
		       COALESCE(v.customer_name,''),COALESCE(v.customer_tax_id,'')
		FROM pos_tickets t
		JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id
		WHERE t.restaurant_id=? AND v.service_date BETWEEN ? AND ?
		  AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED')
		ORDER BY t.id
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return 0, err
	}
	type posSource struct {
		ticketID, visitID, gross, refunded int64
		date                               time.Time
		status, name, taxID                string
	}
	tickets := make([]posSource, 0)
	for rows.Next() {
		var ticketID, visitID, gross, refunded int64
		var date time.Time
		var status, name, taxID string
		if err := rows.Scan(&ticketID, &visitID, &status, &gross, &refunded, &date, &name, &taxID); err != nil {
			rows.Close()
			return 0, err
		}
		tickets = append(tickets, posSource{ticketID: ticketID, visitID: visitID, gross: gross, refunded: refunded, date: date, status: status, name: name, taxID: taxID})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	written := 0
	for _, ticket := range tickets {
		ticketID, gross, refunded, date := ticket.ticketID, ticket.gross, ticket.refunded, ticket.date
		status, name, taxID := ticket.status, ticket.name, ticket.taxID
		customerID, err := s.upsertCustomer(ctx, tx, tenantID, CustomerIdentityInput{TaxID: taxID, Name: name}, date)
		if err != nil {
			return written, err
		}
		documentID, err := s.upsertDocument(ctx, tx, tenantID, "POS", ticketID, customerID, date, status, "EUR", gross, refunded)
		if err != nil {
			return written, err
		}
		if customerID != 0 {
			if err = s.upsertCustomerSource(ctx, tx, tenantID, customerID, "POS", ticketID); err != nil {
				return written, err
			}
		}
		lineRows, err := tx.QueryContext(ctx, `
			SELECT l.id,l.product_name_snapshot,l.quantity,l.line_total_gross_cents,
			       COALESCE(SUM(CASE WHEN r.status='COMPLETED' THEN rl.amount_cents ELSE 0 END),0)
			FROM pos_ticket_lines l
			LEFT JOIN pos_refund_lines rl ON rl.restaurant_id=l.restaurant_id AND rl.ticket_line_id=l.id
			LEFT JOIN pos_refunds r ON r.restaurant_id=rl.restaurant_id AND r.id=rl.refund_id
			WHERE l.restaurant_id=? AND l.ticket_id=? AND l.status='ACTIVE'
			GROUP BY l.id,l.product_name_snapshot,l.quantity,l.line_total_gross_cents
			ORDER BY l.id
		`, tenantID, ticketID)
		if err != nil {
			return written, err
		}
		type posLineSource struct {
			lineID                int64
			description           string
			quantity              float64
			lineTotal, lineRefund int64
		}
		lines := make([]posLineSource, 0)
		for lineRows.Next() {
			var line posLineSource
			if err = lineRows.Scan(&line.lineID, &line.description, &line.quantity, &line.lineTotal, &line.lineRefund); err != nil {
				lineRows.Close()
				return written, err
			}
			lines = append(lines, line)
		}
		if err = lineRows.Err(); err != nil {
			lineRows.Close()
			return written, err
		}
		lineRows.Close()
		for _, line := range lines {
			if err = s.upsertSalesLine(ctx, tx, tenantID, documentID, line.lineID, line.description, line.quantity, line.lineTotal, line.lineRefund); err != nil {
				return written, err
			}
			written++
		}
		written++
	}
	return written, nil
}

func (s *Service) upsertCustomer(ctx context.Context, tx *sql.Tx, tenantID int, identity CustomerIdentityInput, seenOn time.Time) (int64, error) {
	key := NormalizeCustomerIdentity(identity)
	if key == "" {
		return 0, nil
	}
	name := strings.TrimSpace(identity.Name)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO analytics_customers (restaurant_id,identity_key,display_name,email,phone,tax_id,first_seen,last_seen)
		VALUES (?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE
		 display_name=COALESCE(NULLIF(VALUES(display_name),''),display_name),
		 email=COALESCE(NULLIF(VALUES(email),''),email),
		 phone=COALESCE(NULLIF(VALUES(phone),''),phone),
		 tax_id=COALESCE(NULLIF(VALUES(tax_id),''),tax_id),
		 first_seen=CASE WHEN first_seen IS NULL OR VALUES(first_seen)<first_seen THEN VALUES(first_seen) ELSE first_seen END,
		 last_seen=CASE WHEN last_seen IS NULL OR VALUES(last_seen)>last_seen THEN VALUES(last_seen) ELSE last_seen END
	`, tenantID, key, name, strings.TrimSpace(identity.Email), strings.TrimSpace(identity.Phone), strings.TrimSpace(identity.TaxID), dateOnly(seenOn), dateOnly(seenOn))
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM analytics_customers WHERE restaurant_id=? AND identity_key=?`, tenantID, key).Scan(&id)
	return id, err
}

func (s *Service) upsertCustomerSource(ctx context.Context, tx *sql.Tx, tenantID int, customerID int64, sourceType string, sourceID int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO analytics_customer_sources (restaurant_id,customer_id,source_type,source_ref)
		VALUES (?,?,?,?)
		ON DUPLICATE KEY UPDATE customer_id=VALUES(customer_id)
	`, tenantID, customerID, sourceType, strconv.FormatInt(sourceID, 10))
	return err
}

func (s *Service) upsertDocument(ctx context.Context, tx *sql.Tx, tenantID int, sourceType string, sourceID, customerID int64, occurredOn time.Time, status, currency string, gross, refunded int64) (int64, error) {
	var customer any
	if customerID > 0 {
		customer = customerID
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO analytics_sales_documents (restaurant_id,source_type,source_id,customer_id,occurred_on,status,currency,gross_cents,refunded_cents)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE customer_id=VALUES(customer_id),occurred_on=VALUES(occurred_on),status=VALUES(status),currency=VALUES(currency),gross_cents=VALUES(gross_cents),refunded_cents=VALUES(refunded_cents)
	`, tenantID, sourceType, sourceID, customer, dateOnly(occurredOn), status, strings.ToUpper(strings.TrimSpace(currency)), gross, refunded)
	if err != nil {
		return 0, err
	}
	var documentID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM analytics_sales_documents WHERE restaurant_id=? AND source_type=? AND source_id=?`, tenantID, sourceType, sourceID).Scan(&documentID)
	return documentID, err
}

func (s *Service) upsertSalesLine(ctx context.Context, tx *sql.Tx, tenantID int, documentID, sourceLineID int64, description string, quantity float64, revenue, refunded int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO analytics_sales_lines (restaurant_id,sales_document_id,source_line_id,description,quantity,revenue_cents,refunded_cents)
		VALUES (?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE description=VALUES(description),quantity=VALUES(quantity),revenue_cents=VALUES(revenue_cents),refunded_cents=VALUES(refunded_cents)
	`, tenantID, documentID, sourceLineID, description, quantity, revenue, refunded)
	return err
}

func cents(value float64) int64 { return int64(math.Round(value * 100)) }

func (s *Service) refreshStock(ctx context.Context, tx *sql.Tx, tenantID int, dateRange DateRange) (int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id,m.stock_item_id,m.occurred_at,m.type,COALESCE(m.waste_reason,''),m.qty_base,
		       m.total_cost,m.unit_cost,
		       (SELECT p.unit_cost_base FROM stock_item_prices p
		          WHERE p.restaurant_id=m.restaurant_id AND p.stock_item_id=m.stock_item_id AND p.effective_at<=m.occurred_at
		          ORDER BY p.effective_at DESC,p.id DESC LIMIT 1),
		       (SELECT AVG(l.avg_unit_cost) FROM stock_levels l
		          WHERE l.restaurant_id=m.restaurant_id AND l.stock_item_id=m.stock_item_id)
		FROM stock_movements m
		WHERE m.restaurant_id=? AND DATE(m.occurred_at) BETWEEN ? AND ?
		ORDER BY m.id
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return 0, err
	}
	type movementSource struct {
		movementID, itemID int64
		occurred           time.Time
		factType, reason   string
		quantity           float64
		total, unit        sql.NullFloat64
		price, average     sql.NullFloat64
	}
	movements := make([]movementSource, 0)
	for rows.Next() {
		var movement movementSource
		if err = rows.Scan(&movement.movementID, &movement.itemID, &movement.occurred, &movement.factType, &movement.reason, &movement.quantity, &movement.total, &movement.unit, &movement.price, &movement.average); err != nil {
			rows.Close()
			return 0, err
		}
		movements = append(movements, movement)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	written := 0
	for _, movement := range movements {
		movementID, itemID, occurred := movement.movementID, movement.itemID, movement.occurred
		factType, reason, quantity := movement.factType, movement.reason, movement.quantity
		cost, known := ResolveCost(CostCandidates{
			MovementTotal:  nullFloatPointer(movement.total),
			MovementUnit:   nullFloatPointer(movement.unit),
			EffectivePrice: nullFloatPointer(movement.price),
			AverageLevel:   nullFloatPointer(movement.average),
			Quantity:       quantity,
		})
		if err = s.insertStockFact(ctx, tx, tenantID, "MOVEMENT", movementID, occurred, itemID, factType, reason, math.Abs(quantity), cost, known); err != nil {
			return written, err
		}
		written++
	}
	// Recipe waste is expected production loss; explicit WASTE movements remain
	// separate, so reporting can show recorded and recipe-derived waste.
	productionRows, err := tx.QueryContext(ctx, `
		SELECT p.id,p.produced_at,p.qty_produced_base,r.output_item_id,r.waste_pct
		FROM stock_production_orders p
		JOIN stock_recipes r ON r.restaurant_id=p.restaurant_id AND r.id=p.recipe_id
		WHERE p.restaurant_id=? AND DATE(p.produced_at) BETWEEN ? AND ? AND p.status='CONFIRMED' AND r.waste_pct>0
		ORDER BY p.id
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return written, err
	}
	type productionSource struct {
		productionID, outputItemID int64
		occurred                   time.Time
		produced, wastePct         float64
	}
	productions := make([]productionSource, 0)
	for productionRows.Next() {
		var production productionSource
		if err = productionRows.Scan(&production.productionID, &production.occurred, &production.produced, &production.outputItemID, &production.wastePct); err != nil {
			productionRows.Close()
			return written, err
		}
		productions = append(productions, production)
	}
	if err = productionRows.Err(); err != nil {
		productionRows.Close()
		return written, err
	}
	productionRows.Close()
	for _, production := range productions {
		productionID, outputItemID := production.productionID, production.outputItemID
		occurred, produced, wastePct := production.occurred, production.produced, production.wastePct
		quantity := 0.0
		if wastePct < 100 {
			quantity = math.Abs(produced) * wastePct / (100 - wastePct)
		}
		if quantity <= 0 {
			continue
		}
		cost, known, costErr := s.itemCostAt(ctx, tx, tenantID, outputItemID, occurred, quantity)
		if costErr != nil {
			return written, costErr
		}
		if err = s.insertStockFact(ctx, tx, tenantID, "PRODUCTION_RECIPE", productionID, occurred, outputItemID, "RECIPE_WASTE", "RECIPE", quantity, cost, known); err != nil {
			return written, err
		}
		written++
	}

	// Actual component use above planned use is a separately attributable
	// production variance. It is not folded into recipe percentage waste.
	varianceRows, err := tx.QueryContext(ctx, `
		SELECT l.id,o.produced_at,l.stock_item_id,(l.actual_qty_base-l.planned_qty_base)
		FROM stock_production_order_lines l
		JOIN stock_production_orders o ON o.restaurant_id=l.restaurant_id AND o.id=l.production_order_id
		WHERE l.restaurant_id=? AND DATE(o.produced_at) BETWEEN ? AND ? AND o.status='CONFIRMED'
		  AND l.actual_qty_base>l.planned_qty_base
		ORDER BY l.id
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return written, err
	}
	type varianceSource struct {
		lineID, itemID int64
		occurred       time.Time
		quantity       float64
	}
	variances := make([]varianceSource, 0)
	for varianceRows.Next() {
		var variance varianceSource
		if err = varianceRows.Scan(&variance.lineID, &variance.occurred, &variance.itemID, &variance.quantity); err != nil {
			varianceRows.Close()
			return written, err
		}
		variances = append(variances, variance)
	}
	if err = varianceRows.Err(); err != nil {
		varianceRows.Close()
		return written, err
	}
	varianceRows.Close()
	for _, variance := range variances {
		lineID, itemID, occurred, quantity := variance.lineID, variance.itemID, variance.occurred, variance.quantity
		cost, known, costErr := s.itemCostAt(ctx, tx, tenantID, itemID, occurred, quantity)
		if costErr != nil {
			return written, costErr
		}
		if err = s.insertStockFact(ctx, tx, tenantID, "PRODUCTION_VARIANCE", lineID, occurred, itemID, "PRODUCTION_VARIANCE", "OVERPRODUCTION", quantity, cost, known); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func nullFloatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func (s *Service) itemCostAt(ctx context.Context, tx *sql.Tx, tenantID int, itemID int64, occurred time.Time, quantity float64) (float64, bool, error) {
	var price, average sql.NullFloat64
	err := tx.QueryRowContext(ctx, `
		SELECT
		 (SELECT p.unit_cost_base FROM stock_item_prices p
		   WHERE p.restaurant_id=? AND p.stock_item_id=? AND p.effective_at<=?
		   ORDER BY p.effective_at DESC,p.id DESC LIMIT 1),
		 (SELECT AVG(l.avg_unit_cost) FROM stock_levels l WHERE l.restaurant_id=? AND l.stock_item_id=?)
	`, tenantID, itemID, occurred, tenantID, itemID).Scan(&price, &average)
	if err != nil {
		return 0, false, err
	}
	cost, known := ResolveCost(CostCandidates{EffectivePrice: nullFloatPointer(price), AverageLevel: nullFloatPointer(average), Quantity: quantity})
	return cost, known, nil
}

func (s *Service) insertStockFact(ctx context.Context, tx *sql.Tx, tenantID int, sourceType string, sourceID int64, occurred time.Time, itemID int64, factType, reason string, quantity, cost float64, known bool) error {
	var costArg any
	if known {
		costArg = cents(cost)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO analytics_stock_facts (restaurant_id,source_type,source_id,occurred_on,stock_item_id,fact_type,waste_reason,quantity_base,cost_cents,cost_known)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE occurred_on=VALUES(occurred_on),stock_item_id=VALUES(stock_item_id),fact_type=VALUES(fact_type),waste_reason=VALUES(waste_reason),quantity_base=VALUES(quantity_base),cost_cents=VALUES(cost_cents),cost_known=VALUES(cost_known)
	`, tenantID, sourceType, sourceID, dateOnly(occurred), itemID, factType, reason, quantity, costArg, boolInt(known))
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type rollupAccumulator struct {
	DailyAggregate
	SalesDocuments           int
	IdentifiedSalesDocuments int
	NonEURDocuments          int
}

func (s *Service) rebuildRollups(ctx context.Context, tx *sql.Tx, tenantID int, dateRange DateRange) error {
	byDate := make(map[string]*rollupAccumulator)
	for date := dateRange.From; !date.After(dateRange.To); date = date.AddDate(0, 0, 1) {
		byDate[date.Format("2006-01-02")] = &rollupAccumulator{DailyAggregate: DailyAggregate{WasteBreakdown: make(map[string]WasteBreakdownValue)}}
	}
	docRows, err := tx.QueryContext(ctx, `SELECT occurred_on,source_type,status,currency,gross_cents,refunded_cents,customer_id FROM analytics_sales_documents WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return err
	}
	for docRows.Next() {
		var date time.Time
		var source, status, currency string
		var gross, refunded int64
		var customer sql.NullInt64
		if err = docRows.Scan(&date, &source, &status, &currency, &gross, &refunded, &customer); err != nil {
			docRows.Close()
			return err
		}
		day := byDate[dateOnly(date).Format("2006-01-02")]
		if day == nil {
			continue
		}
		day.SalesDocuments++
		if !strings.EqualFold(strings.TrimSpace(currency), "EUR") {
			day.NonEURDocuments++
			continue
		}
		if source == "INVOICE" {
			day.InvoicedRevenueCents += gross
		} else if source == "POS" && includedPOSTicketStatus(status) {
			day.POSRevenueCents += gross - refunded
			day.POSRefundCents += refunded
		}
		if customer.Valid {
			day.IdentifiedSalesDocuments++
		}
	}
	if err = docRows.Err(); err != nil {
		docRows.Close()
		return err
	}
	docRows.Close()
	factRows, err := tx.QueryContext(ctx, `SELECT occurred_on,fact_type,waste_reason,quantity_base,COALESCE(cost_cents,0),cost_known FROM analytics_stock_facts WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ?`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return err
	}
	for factRows.Next() {
		var date time.Time
		var factType, reason string
		var quantity float64
		var cost int64
		var known int
		if err = factRows.Scan(&date, &factType, &reason, &quantity, &cost, &known); err != nil {
			factRows.Close()
			return err
		}
		day := byDate[dateOnly(date).Format("2006-01-02")]
		if day == nil {
			continue
		}
		if factType == "PURCHASE" {
			day.StockPurchaseQuantity += quantity
			if known != 0 {
				day.StockPurchaseKnownQuantity += quantity
				day.StockPurchaseKnownCostCents += cost
			} else {
				day.StockPurchaseUnknownQuantity += quantity
			}
			continue
		}
		if factType == "RETURN" {
			day.StockReturnQuantity += quantity
			if known != 0 {
				day.StockReturnKnownQuantity += quantity
				day.StockReturnKnownCostCents += cost
			}
			continue
		}
		if factType == "SALE" || factType == "PRODUCTION_OUT" {
			day.COGSQuantity += quantity
			if known != 0 {
				day.COGSKnownQuantity += quantity
				day.COGSKnownCostCents += cost
			}
		}
		if factType != "WASTE" && factType != "RECIPE_WASTE" && factType != "PRODUCTION_VARIANCE" {
			continue
		}
		day.WasteQuantity += quantity
		if known == 0 {
			day.WasteUnknownQuantity += quantity
		}
		if known != 0 {
			day.WasteKnownCostCents += cost
		}
		if reason == "" {
			reason = factType
		}
		breakdown := day.WasteBreakdown[reason]
		breakdown.Quantity += quantity
		breakdown.KnownCostCents += cost
		if known == 0 {
			breakdown.UnknownQuantity += quantity
		}
		day.WasteBreakdown[reason] = breakdown
	}
	if err = factRows.Err(); err != nil {
		factRows.Close()
		return err
	}
	factRows.Close()
	for key, day := range byDate {
		*day = rollupAccumulator{
			DailyAggregate:           normalizeCOGSReturns(day.DailyAggregate),
			SalesDocuments:           day.SalesDocuments,
			IdentifiedSalesDocuments: day.IdentifiedSalesDocuments,
			NonEURDocuments:          day.NonEURDocuments,
		}
		wasteJSON, marshalErr := json.Marshal(day.WasteBreakdown)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO analytics_daily_rollups (restaurant_id,rollup_date,invoiced_revenue_cents,pos_revenue_cents,pos_refunded_cents,cogs_known_cost_cents,cogs_quantity,cogs_known_quantity,stock_purchase_known_cost_cents,stock_purchase_quantity,stock_purchase_known_quantity,stock_purchase_unknown_quantity,stock_return_known_cost_cents,stock_return_quantity,stock_return_known_quantity,waste_known_cost_cents,waste_quantity,waste_unknown_quantity,sales_documents,identified_sales_documents,non_eur_documents,waste_breakdown_json)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		`, tenantID, key, day.InvoicedRevenueCents, day.POSRevenueCents, day.POSRefundCents, day.COGSKnownCostCents, day.COGSQuantity, day.COGSKnownQuantity, day.StockPurchaseKnownCostCents, day.StockPurchaseQuantity, day.StockPurchaseKnownQuantity, day.StockPurchaseUnknownQuantity, day.StockReturnKnownCostCents, day.StockReturnQuantity, day.StockReturnKnownQuantity, day.WasteKnownCostCents, day.WasteQuantity, day.WasteUnknownQuantity, day.SalesDocuments, day.IdentifiedSalesDocuments, day.NonEURDocuments, wasteJSON); err != nil {
			return err
		}
	}
	return nil
}

type CostCoverage struct {
	KnownQuantity float64 `json:"knownQuantity"`
	TotalQuantity float64 `json:"totalQuantity"`
	Percent       float64 `json:"percent"`
}

type Summary struct {
	InvoicedRevenueEUR float64 `json:"invoicedRevenueEUR"`
	POSRevenueEUR      float64 `json:"posRevenueEUR"`
	POSRefundsEUR      float64 `json:"posRefundsEUR"`
	TotalRevenueEUR    float64 `json:"totalRevenueEUR"`

	CostOfGoodsEUR        *float64     `json:"costOfGoodsEUR"`
	CostOfGoodsLabel      string       `json:"costOfGoodsLabel"`
	StockPurchasesEUR     *float64     `json:"stockPurchasesEUR"`
	StockPurchasesLabel   string       `json:"stockPurchasesLabel"`
	WasteCostEUR          *float64     `json:"wasteCostEUR"`
	WasteCostLabel        string       `json:"wasteCostLabel"`
	IdentifiedPeople      int          `json:"identifiedPeople"`
	WasteQuantity         float64      `json:"wasteQuantity"`
	CostCoverage          CostCoverage `json:"costCoverage"`
	StockPurchaseCoverage CostCoverage `json:"stockPurchaseCoverage"`
}

type SeriesPoint struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Summary Summary `json:"summary"`
}

type Comparison struct {
	From         string             `json:"from"`
	To           string             `json:"to"`
	Summary      Summary            `json:"summary"`
	DeltaPercent map[string]float64 `json:"deltaPercent"`
}

type WasteBreakdownEntry struct {
	Reason          string  `json:"reason"`
	Quantity        float64 `json:"quantity"`
	KnownCostEUR    float64 `json:"knownCostEUR"`
	UnknownQuantity float64 `json:"unknownQuantity"`
	CostLabel       string  `json:"costLabel"`
}

type DataQuality struct {
	Currency                string       `json:"currency"`
	NonEURDocuments         int          `json:"nonEurDocuments"`
	UnidentifiedDocuments   int          `json:"unidentifiedDocuments"`
	CostCoverage            CostCoverage `json:"costCoverage"`
	UnknownCostQuantity     float64      `json:"unknownCostQuantity"`
	StockPurchaseCoverage   CostCoverage `json:"stockPurchaseCoverage"`
	UnknownPurchaseQuantity float64      `json:"unknownPurchaseQuantity"`
	WasteCostCoverage       CostCoverage `json:"wasteCostCoverage"`
	UnknownWasteQuantity    float64      `json:"unknownWasteQuantity"`
	RefreshRequired         bool         `json:"refreshRequired"`
}

type Overview struct {
	Currency       string                `json:"currency"`
	From           string                `json:"from"`
	To             string                `json:"to"`
	Granularity    Granularity           `json:"granularity"`
	Summary        Summary               `json:"summary"`
	Comparison     *Comparison           `json:"comparison,omitempty"`
	Series         []SeriesPoint         `json:"series"`
	WasteBreakdown []WasteBreakdownEntry `json:"wasteBreakdown"`
	DataQuality    DataQuality           `json:"dataQuality"`
}

type dailyRollup struct {
	Date time.Time
	rollupAccumulator
}

func (s *Service) Overview(ctx context.Context, tenantID int, dateRange DateRange, granularity Granularity, comparePrevious bool) (Overview, error) {
	ctx, cancel := analyticsContext(ctx, 15*time.Second)
	defer cancel()
	dateRange.From, dateRange.To = dateOnly(dateRange.From), dateOnly(dateRange.To)
	periods, err := BuildPeriods(dateRange.From, dateRange.To, granularity)
	if err != nil || tenantID <= 0 {
		if err == nil {
			err = errors.New("invalid analytics tenant")
		}
		return Overview{}, err
	}
	rollups, err := s.loadRollups(ctx, tenantID, dateRange)
	if err != nil {
		return Overview{}, fmt.Errorf("load analytics rollups: %w", err)
	}
	customers, err := s.loadPeriodCustomers(ctx, tenantID, dateRange, granularity)
	if err != nil {
		return Overview{}, fmt.Errorf("load analytics customers: %w", err)
	}
	summaryAgg := aggregateRollupRows(rollups, dateRange.From, dateRange.To)
	summaryAgg.IdentifiedPeople = len(customers["range"])
	result := Overview{
		Currency:       "EUR",
		From:           dateRange.From.Format("2006-01-02"),
		To:             dateRange.To.Format("2006-01-02"),
		Granularity:    granularity,
		Summary:        summaryFromAggregate(summaryAgg),
		Series:         make([]SeriesPoint, 0, len(periods)),
		WasteBreakdown: wasteBreakdown(summaryAgg.WasteBreakdown),
		DataQuality:    qualityFromAggregate(summaryAgg, rollups),
	}
	for _, period := range periods {
		aggregate := aggregateRollupRows(rollups, period.Start, period.End)
		aggregate.IdentifiedPeople = len(customers[period.Start.Format("2006-01-02")])
		result.Series = append(result.Series, SeriesPoint{
			From:    period.Start.Format("2006-01-02"),
			To:      period.End.Format("2006-01-02"),
			Summary: summaryFromAggregate(aggregate),
		})
	}
	if comparePrevious {
		previous := PreviousComparisonRange(dateRange.From, dateRange.To)
		previousRows, err := s.loadRollups(ctx, tenantID, DateRange{From: previous.Start, To: previous.End})
		if err != nil {
			return Overview{}, fmt.Errorf("load previous analytics rollups: %w", err)
		}
		previousCustomers, err := s.loadPeriodCustomers(ctx, tenantID, DateRange{From: previous.Start, To: previous.End}, granularity)
		if err != nil {
			return Overview{}, fmt.Errorf("load previous analytics customers: %w", err)
		}
		previousAgg := aggregateRollupRows(previousRows, previous.Start, previous.End)
		previousAgg.IdentifiedPeople = len(previousCustomers["range"])
		result.Comparison = &Comparison{
			From:         previous.Start.Format("2006-01-02"),
			To:           previous.End.Format("2006-01-02"),
			Summary:      summaryFromAggregate(previousAgg),
			DeltaPercent: comparisonDelta(summaryAgg, previousAgg),
		}
	}
	return result, nil
}

func analyticsContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func (s *Service) loadRollups(ctx context.Context, tenantID int, dateRange DateRange) ([]dailyRollup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rollup_date,invoiced_revenue_cents,pos_revenue_cents,pos_refunded_cents,
		       cogs_known_cost_cents,cogs_quantity,cogs_known_quantity,
		       stock_purchase_known_cost_cents,stock_purchase_quantity,stock_purchase_known_quantity,stock_purchase_unknown_quantity,
		       stock_return_known_cost_cents,stock_return_quantity,stock_return_known_quantity,
		       waste_known_cost_cents,waste_quantity,waste_unknown_quantity,sales_documents,identified_sales_documents,
		       non_eur_documents,waste_breakdown_json
		FROM analytics_daily_rollups
		WHERE restaurant_id=? AND rollup_date BETWEEN ? AND ?
		ORDER BY rollup_date
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]dailyRollup, 0)
	for rows.Next() {
		var row dailyRollup
		var breakdown []byte
		if err = rows.Scan(&row.Date, &row.InvoicedRevenueCents, &row.POSRevenueCents, &row.POSRefundCents, &row.COGSKnownCostCents, &row.COGSQuantity, &row.COGSKnownQuantity, &row.StockPurchaseKnownCostCents, &row.StockPurchaseQuantity, &row.StockPurchaseKnownQuantity, &row.StockPurchaseUnknownQuantity, &row.StockReturnKnownCostCents, &row.StockReturnQuantity, &row.StockReturnKnownQuantity, &row.WasteKnownCostCents, &row.WasteQuantity, &row.WasteUnknownQuantity, &row.SalesDocuments, &row.IdentifiedSalesDocuments, &row.NonEURDocuments, &breakdown); err != nil {
			return nil, err
		}
		row.WasteBreakdown = make(map[string]WasteBreakdownValue)
		if len(breakdown) > 0 {
			if err = json.Unmarshal(breakdown, &row.WasteBreakdown); err != nil {
				return nil, err
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Service) loadPeriodCustomers(ctx context.Context, tenantID int, dateRange DateRange, granularity Granularity) (map[string]map[int64]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT occurred_on,customer_id FROM analytics_sales_documents
		WHERE restaurant_id=? AND occurred_on BETWEEN ? AND ? AND currency='EUR' AND customer_id IS NOT NULL
	`, tenantID, dateRange.From, dateRange.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[int64]struct{}{"range": make(map[int64]struct{})}
	for rows.Next() {
		var date time.Time
		var customerID int64
		if err = rows.Scan(&date, &customerID); err != nil {
			return nil, err
		}
		result["range"][customerID] = struct{}{}
		key := periodStart(date, granularity).Format("2006-01-02")
		if result[key] == nil {
			result[key] = make(map[int64]struct{})
		}
		result[key][customerID] = struct{}{}
	}
	return result, rows.Err()
}

func aggregateRollupRows(rows []dailyRollup, from, to time.Time) rollupAccumulator {
	result := rollupAccumulator{DailyAggregate: DailyAggregate{WasteBreakdown: make(map[string]WasteBreakdownValue)}}
	for _, row := range rows {
		if dateOnly(row.Date).Before(dateOnly(from)) || dateOnly(row.Date).After(dateOnly(to)) {
			continue
		}
		result.InvoicedRevenueCents += row.InvoicedRevenueCents
		result.POSRevenueCents += row.POSRevenueCents
		result.POSRefundCents += row.POSRefundCents
		result.COGSKnownCostCents += row.COGSKnownCostCents
		result.COGSQuantity += row.COGSQuantity
		result.COGSKnownQuantity += row.COGSKnownQuantity
		result.StockPurchaseKnownCostCents += row.StockPurchaseKnownCostCents
		result.StockPurchaseQuantity += row.StockPurchaseQuantity
		result.StockPurchaseKnownQuantity += row.StockPurchaseKnownQuantity
		result.StockPurchaseUnknownQuantity += row.StockPurchaseUnknownQuantity
		result.StockReturnKnownCostCents += row.StockReturnKnownCostCents
		result.StockReturnQuantity += row.StockReturnQuantity
		result.StockReturnKnownQuantity += row.StockReturnKnownQuantity
		result.WasteKnownCostCents += row.WasteKnownCostCents
		result.WasteQuantity += row.WasteQuantity
		result.WasteUnknownQuantity += row.WasteUnknownQuantity
		result.SalesDocuments += row.SalesDocuments
		result.IdentifiedSalesDocuments += row.IdentifiedSalesDocuments
		result.NonEURDocuments += row.NonEURDocuments
		for reason, value := range row.WasteBreakdown {
			current := result.WasteBreakdown[reason]
			current.Quantity += value.Quantity
			current.KnownCostCents += value.KnownCostCents
			current.UnknownQuantity += value.UnknownQuantity
			result.WasteBreakdown[reason] = current
		}
	}
	result.DailyAggregate = normalizeCOGSReturns(result.DailyAggregate)
	return result
}

func summaryFromAggregate(aggregate rollupAccumulator) Summary {
	result := Summary{
		InvoicedRevenueEUR: float64(aggregate.InvoicedRevenueCents) / 100,
		POSRevenueEUR:      float64(aggregate.POSRevenueCents) / 100,
		POSRefundsEUR:      float64(aggregate.POSRefundCents) / 100,
		IdentifiedPeople:   aggregate.IdentifiedPeople,
		WasteQuantity:      aggregate.WasteQuantity,
		CostCoverage: CostCoverage{
			KnownQuantity: aggregate.COGSKnownQuantity,
			TotalQuantity: aggregate.COGSQuantity,
			Percent:       aggregate.CostCoveragePercent,
		},
	}
	result.TotalRevenueEUR = result.InvoicedRevenueEUR + result.POSRevenueEUR
	value := float64(aggregate.COGSKnownCostCents) / 100
	result.CostOfGoodsEUR = &value
	if aggregate.COGSQuantity > 0 && aggregate.COGSKnownQuantity < aggregate.COGSQuantity {
		result.CostOfGoodsLabel = "N/D"
	}
	stockPurchaseValue := float64(aggregate.StockPurchaseKnownCostCents) / 100
	result.StockPurchasesEUR = &stockPurchaseValue
	if aggregate.StockPurchaseQuantity > 0 && aggregate.StockPurchaseKnownQuantity < aggregate.StockPurchaseQuantity {
		result.StockPurchasesLabel = "N/D"
	}
	result.StockPurchaseCoverage = CostCoverage{
		KnownQuantity: aggregate.StockPurchaseKnownQuantity,
		TotalQuantity: aggregate.StockPurchaseQuantity,
		Percent:       percent(aggregate.StockPurchaseKnownQuantity, aggregate.StockPurchaseQuantity),
	}
	wasteKnownQuantity := aggregate.WasteQuantity - aggregate.WasteUnknownQuantity
	wasteValue := float64(aggregate.WasteKnownCostCents) / 100
	result.WasteCostEUR = &wasteValue
	if aggregate.WasteQuantity > 0 && wasteKnownQuantity < aggregate.WasteQuantity {
		result.WasteCostLabel = "N/D"
	}
	return result
}

func qualityFromAggregate(aggregate rollupAccumulator, rows []dailyRollup) DataQuality {
	unknownDocuments := 0
	for _, row := range rows {
		unknownDocuments += row.SalesDocuments - row.IdentifiedSalesDocuments
	}
	wasteKnown := aggregate.WasteQuantity - aggregate.WasteUnknownQuantity
	return DataQuality{
		Currency:              "EUR",
		NonEURDocuments:       aggregate.NonEURDocuments,
		UnidentifiedDocuments: unknownDocuments,
		CostCoverage: CostCoverage{
			KnownQuantity: aggregate.COGSKnownQuantity,
			TotalQuantity: aggregate.COGSQuantity,
			Percent:       aggregate.CostCoveragePercent,
		},
		UnknownCostQuantity: aggregate.COGSQuantity - aggregate.COGSKnownQuantity,
		StockPurchaseCoverage: CostCoverage{
			KnownQuantity: aggregate.StockPurchaseKnownQuantity,
			TotalQuantity: aggregate.StockPurchaseQuantity,
			Percent:       percent(aggregate.StockPurchaseKnownQuantity, aggregate.StockPurchaseQuantity),
		},
		UnknownPurchaseQuantity: aggregate.StockPurchaseUnknownQuantity,
		WasteCostCoverage: CostCoverage{
			KnownQuantity: wasteKnown,
			TotalQuantity: aggregate.WasteQuantity,
			Percent:       percent(wasteKnown, aggregate.WasteQuantity),
		},
		UnknownWasteQuantity: aggregate.WasteUnknownQuantity,
		RefreshRequired:      len(rows) == 0,
	}
}

func percent(known, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return known / total * 100
}

func wasteBreakdown(values map[string]WasteBreakdownValue) []WasteBreakdownEntry {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]WasteBreakdownEntry, 0, len(keys))
	for _, reason := range keys {
		value := values[reason]
		knownQuantity := value.Quantity - value.UnknownQuantity
		label := ""
		if value.Quantity == 0 || knownQuantity < value.Quantity {
			label = "N/D"
		}
		result = append(result, WasteBreakdownEntry{Reason: reason, Quantity: value.Quantity, KnownCostEUR: float64(value.KnownCostCents) / 100, UnknownQuantity: value.UnknownQuantity, CostLabel: label})
	}
	return result
}

func comparisonDelta(current, previous rollupAccumulator) map[string]float64 {
	return map[string]float64{
		"invoicedRevenueEUR": changePercent(current.InvoicedRevenueCents, previous.InvoicedRevenueCents),
		"posRevenueEUR":      changePercent(current.POSRevenueCents, previous.POSRevenueCents),
		"totalRevenueEUR":    changePercent(current.InvoicedRevenueCents+current.POSRevenueCents, previous.InvoicedRevenueCents+previous.POSRevenueCents),
		"identifiedPeople":   changePercent(int64(current.IdentifiedPeople), int64(previous.IdentifiedPeople)),
		"stockPurchasesEUR":  changePercent(current.StockPurchaseKnownCostCents, previous.StockPurchaseKnownCostCents),
		"costOfGoodsEUR":     changePercent(current.COGSKnownCostCents, previous.COGSKnownCostCents),
		"wasteCostEUR":       changePercent(current.WasteKnownCostCents, previous.WasteKnownCostCents),
		"wasteQuantity":      changePercentFloat(current.WasteQuantity, previous.WasteQuantity),
	}
}

func changePercent(current, previous int64) float64 {
	return changePercentFloat(float64(current), float64(previous))
}

func changePercentFloat(current, previous float64) float64 {
	if previous == 0 {
		if current == 0 {
			return 0
		}
		return 100
	}
	return (current - previous) / math.Abs(previous) * 100
}
