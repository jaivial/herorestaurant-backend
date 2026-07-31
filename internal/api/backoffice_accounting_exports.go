package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

type posAccountingLine struct {
	ID         int64
	GrossCents int64
	VATRate    float64
}
type posAccountingVATBucket struct {
	VATRate                        float64
	GrossCents, NetCents, TaxCents int64
}

func accountingVATBuckets(lines []posAccountingLine, ticketDiscount int64) ([]posAccountingVATBucket, error) {
	if ticketDiscount < 0 {
		return nil, errors.New("invalid discount")
	}
	var total int64
	for _, line := range lines {
		if line.GrossCents < 0 || line.VATRate < 0 || line.VATRate > 100 {
			return nil, errors.New("invalid line")
		}
		total += line.GrossCents
	}
	if ticketDiscount > total {
		return nil, errors.New("discount exceeds gross")
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	remaining := ticketDiscount
	buckets := map[string]posAccountingVATBucket{}
	for index, line := range lines {
		allocated := int64(0)
		if ticketDiscount > 0 && total > 0 {
			if index == len(lines)-1 {
				allocated = remaining
			} else {
				allocated = int64(math.Round(float64(ticketDiscount) * float64(line.GrossCents) / float64(total)))
				if allocated > remaining {
					allocated = remaining
				}
			}
			remaining -= allocated
		}
		gross := line.GrossCents - allocated
		tax := int64(math.Round(float64(gross) * line.VATRate / (100 + line.VATRate)))
		key := strconv.FormatFloat(line.VATRate, 'f', 2, 64)
		bucket := buckets[key]
		bucket.VATRate = line.VATRate
		bucket.GrossCents += gross
		bucket.TaxCents += tax
		bucket.NetCents += gross - tax
		buckets[key] = bucket
	}
	out := make([]posAccountingVATBucket, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, bucket)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VATRate < out[j].VATRate })
	return out, nil
}

func accountingRefundVATBuckets(lines []posAccountingLine, amount int64) ([]posAccountingVATBucket, error) {
	var available int64
	for _, line := range lines {
		available += line.GrossCents
	}
	if amount <= 0 || amount > available {
		return nil, errors.New("invalid refund amount")
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].ID < lines[j].ID })
	remaining := amount
	buckets := map[string]posAccountingVATBucket{}
	for index, line := range lines {
		gross := remaining
		if index < len(lines)-1 {
			gross = int64(math.Round(float64(amount) * float64(line.GrossCents) / float64(available)))
			if gross > remaining {
				gross = remaining
			}
		}
		remaining -= gross
		tax := int64(math.Round(float64(gross) * line.VATRate / (100 + line.VATRate)))
		key := strconv.FormatFloat(line.VATRate, 'f', 2, 64)
		bucket := buckets[key]
		bucket.VATRate = line.VATRate
		bucket.GrossCents += gross
		bucket.TaxCents += tax
		bucket.NetCents += gross - tax
		buckets[key] = bucket
	}
	out := make([]posAccountingVATBucket, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, bucket)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VATRate < out[j].VATRate })
	return out, nil
}

func accountingCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		value = "'" + value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func (s *Server) accountingSalesVATCSV(ctx context.Context, restaurantID int, from, to string) (string, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.ticket_number,v.service_date,l.id,l.line_total_gross_cents,l.vat_rate_snapshot,t.ticket_discount_cents FROM pos_tickets t JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id JOIN pos_ticket_lines l ON l.restaurant_id=t.restaurant_id AND l.ticket_id=t.id AND l.status='ACTIVE' WHERE t.restaurant_id=? AND v.service_date BETWEEN ? AND ? AND t.status IN ('PAID','PARTIALLY_REFUNDED','REFUNDED') ORDER BY t.id,l.id`, restaurantID, from, to)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	type ticket struct {
		id           int64
		number, date string
		discount     int64
		lines        []posAccountingLine
	}
	tickets := []ticket{}
	var current *ticket
	for rows.Next() {
		var id, lineID, gross, discount int64
		var number, date string
		var vat float64
		if err = rows.Scan(&id, &number, &date, &lineID, &gross, &vat, &discount); err != nil {
			return "", 0, err
		}
		if current == nil || current.id != id {
			tickets = append(tickets, ticket{id: id, number: number, date: normalizePOSDate(date), discount: discount})
			current = &tickets[len(tickets)-1]
		}
		current.lines = append(current.lines, posAccountingLine{ID: lineID, GrossCents: gross, VATRate: vat})
	}
	var b strings.Builder
	b.WriteString("ticket,date,vat_rate,net_cents,tax_cents,gross_cents\n")
	count := 0
	for _, item := range tickets {
		buckets, err := accountingVATBuckets(item.lines, item.discount)
		if err != nil {
			return "", 0, err
		}
		for _, bucket := range buckets {
			fmt.Fprintf(&b, "%s,%s,%.2f,%d,%d,%d\n", accountingCSVCell(item.number), item.date, bucket.VATRate, bucket.NetCents, bucket.TaxCents, bucket.GrossCents)
			count++
		}
	}
	return b.String(), count, rows.Err()
}

func (s *Server) accountingPaymentsCSV(ctx context.Context, restaurantID int, from, to string) (string, int, error) {
	return s.accountingSimpleCSV(ctx, `SELECT t.ticket_number,v.service_date,p.method,p.amount_cents,COALESCE(p.provider,''),COALESCE(p.provider_reference,'') FROM pos_payments p JOIN pos_tickets t ON t.restaurant_id=p.restaurant_id AND t.id=p.ticket_id JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id WHERE p.restaurant_id=? AND v.service_date BETWEEN ? AND ? AND p.status='CAPTURED' ORDER BY v.service_date,p.id`, []string{"ticket", "date", "method", "amount_cents", "provider", "provider_reference"}, restaurantID, from, to)
}
func (s *Server) accountingRefundsCSV(ctx context.Context, restaurantID int, from, to string) (string, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.ticket_id,t.ticket_number,v.service_date,r.amount_cents,r.payment_method,r.reason,t.ticket_discount_cents,rl.id,rl.amount_cents,l.vat_rate_snapshot FROM pos_refunds r JOIN pos_tickets t ON t.restaurant_id=r.restaurant_id AND t.id=r.ticket_id JOIN pos_visits v ON v.restaurant_id=t.restaurant_id AND v.id=t.visit_id LEFT JOIN pos_refund_lines rl ON rl.restaurant_id=r.restaurant_id AND rl.refund_id=r.id LEFT JOIN pos_ticket_lines l ON l.restaurant_id=rl.restaurant_id AND l.id=rl.ticket_line_id WHERE r.restaurant_id=? AND v.service_date BETWEEN ? AND ? AND r.status='COMPLETED' ORDER BY r.id,rl.id`, restaurantID, from, to)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	type refund struct {
		id, ticketID, amount, ticketDiscount int64
		ticket, date, method, reason         string
		lines                                []posAccountingLine
	}
	items := []refund{}
	var current *refund
	for rows.Next() {
		var id, ticketID, amount, ticketDiscount int64
		var ticket, date, method, reason string
		var lineID, lineAmount sql.NullInt64
		var vat sql.NullFloat64
		if err = rows.Scan(&id, &ticketID, &ticket, &date, &amount, &method, &reason, &ticketDiscount, &lineID, &lineAmount, &vat); err != nil {
			return "", 0, err
		}
		if current == nil || current.id != id {
			items = append(items, refund{id: id, ticketID: ticketID, amount: amount, ticketDiscount: ticketDiscount, ticket: ticket, date: normalizePOSDate(date), method: method, reason: reason})
			current = &items[len(items)-1]
		}
		if lineID.Valid {
			current.lines = append(current.lines, posAccountingLine{ID: lineID.Int64, GrossCents: lineAmount.Int64, VATRate: vat.Float64})
		}
	}
	var b strings.Builder
	b.WriteString("ticket,date,refund_id,method,reason,vat_rate,net_cents,tax_cents,gross_cents\n")
	count := 0
	for _, item := range items {
		lines := item.lines
		if len(lines) == 0 {
			lineRows, queryErr := s.db.QueryContext(ctx, `SELECT id,line_total_gross_cents,vat_rate_snapshot FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE' ORDER BY id`, restaurantID, item.ticketID)
			if queryErr != nil {
				return "", 0, queryErr
			}
			for lineRows.Next() {
				var line posAccountingLine
				if queryErr = lineRows.Scan(&line.ID, &line.GrossCents, &line.VATRate); queryErr != nil {
					lineRows.Close()
					return "", 0, queryErr
				}
				lines = append(lines, line)
			}
			lineRows.Close()
		}
		if len(item.lines) == 0 && item.ticketDiscount > 0 {
			adjusted, discountErr := accountingVATBuckets(lines, item.ticketDiscount)
			if discountErr != nil {
				return "", 0, discountErr
			}
			lines = lines[:0]
			for index, bucket := range adjusted {
				lines = append(lines, posAccountingLine{ID: int64(index + 1), GrossCents: bucket.GrossCents, VATRate: bucket.VATRate})
			}
		}
		buckets, bucketErr := accountingRefundVATBuckets(lines, item.amount)
		if bucketErr != nil {
			return "", 0, bucketErr
		}
		for _, bucket := range buckets {
			fmt.Fprintf(&b, "%s,%s,%d,%s,%s,%.2f,%d,%d,%d\n", accountingCSVCell(item.ticket), item.date, item.id, accountingCSVCell(item.method), accountingCSVCell(item.reason), bucket.VATRate, -bucket.NetCents, -bucket.TaxCents, -bucket.GrossCents)
			count++
		}
	}
	return b.String(), count, rows.Err()
}
func (s *Server) accountingStockCSV(ctx context.Context, restaurantID int, from, to string) (string, int, error) {
	return s.accountingSimpleCSV(ctx, `SELECT m.id,DATE(m.occurred_at),m.type,i.name,w.name,m.qty_base,COALESCE(m.unit_cost,0),COALESCE(m.ref_type,''),COALESCE(m.ref_id,0) FROM stock_movements m JOIN stock_items i ON i.restaurant_id=m.restaurant_id AND i.id=m.stock_item_id JOIN stock_warehouses w ON w.restaurant_id=m.restaurant_id AND w.id=m.warehouse_id WHERE m.restaurant_id=? AND DATE(m.occurred_at) BETWEEN ? AND ? ORDER BY m.occurred_at,m.id`, []string{"movement_id", "date", "type", "item", "warehouse", "quantity_base", "unit_cost_base", "ref_type", "ref_id"}, restaurantID, from, to)
}

func (s *Server) accountingSimpleCSV(ctx context.Context, query string, headers []string, restaurantID int, from, to string) (string, int, error) {
	rows, err := s.db.QueryContext(ctx, query, restaurantID, from, to)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", 0, err
	}
	var b strings.Builder
	b.WriteString(strings.Join(headers, ",") + "\n")
	count := 0
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return "", 0, err
		}
		cells := make([]string, len(values))
		for i, value := range values {
			switch v := value.(type) {
			case nil:
				cells[i] = ""
			case []byte:
				cells[i] = accountingCSVCell(string(v))
			case time.Time:
				cells[i] = v.Format("2006-01-02")
			default:
				cells[i] = accountingCSVCell(fmt.Sprint(v))
			}
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
		count++
	}
	return b.String(), count, rows.Err()
}

func (s *Server) handleBOAccountingExport(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	from, to, ok := posReportRange(r)
	if !ok {
		httpx.WriteError(w, 400, "Invalid export range")
		return
	}
	kind := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("type")))
	var payload string
	var count int
	var err error
	switch kind {
	case "SALES_VAT":
		payload, count, err = s.accountingSalesVATCSV(r.Context(), a.ActiveRestaurantID, from, to)
	case "PAYMENTS":
		payload, count, err = s.accountingPaymentsCSV(r.Context(), a.ActiveRestaurantID, from, to)
	case "REFUNDS":
		payload, count, err = s.accountingRefundsCSV(r.Context(), a.ActiveRestaurantID, from, to)
	case "STOCK":
		payload, count, err = s.accountingStockCSV(r.Context(), a.ActiveRestaurantID, from, to)
	default:
		httpx.WriteError(w, 400, "Invalid export type")
		return
	}
	if err != nil {
		httpx.WriteError(w, 500, "Error generating accounting export")
		return
	}
	hash := sha256.Sum256([]byte(payload))
	hashText := hex.EncodeToString(hash[:])
	_, err = s.db.ExecContext(r.Context(), `INSERT IGNORE INTO accounting_exports (restaurant_id,export_type,period_from,period_to,payload_hash,row_count,generated_by) VALUES (?,?,?,?,?,?,?)`, a.ActiveRestaurantID, kind, from, to, hashText, count, a.User.ID)
	if err != nil {
		httpx.WriteError(w, 500, "Error auditing accounting export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="accounting-`+strings.ToLower(kind)+`-`+from+`-`+to+`.csv"`)
	w.Header().Set("X-Export-SHA256", hashText)
	_, _ = w.Write([]byte(payload))
}
