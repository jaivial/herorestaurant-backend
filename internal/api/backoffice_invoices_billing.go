package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// validPdfTemplates are the supported invoice designs.
var validPdfTemplates = map[string]bool{"basic": true, "modern": true, "classic": true}

func normalizePdfTemplate(v *string) string {
	if v == nil {
		return "basic"
	}
	t := strings.ToLower(strings.TrimSpace(*v))
	if validPdfTemplates[t] {
		return t
	}
	return "basic"
}

func normalizeCurrency(v *string) string {
	if v == nil {
		return "EUR"
	}
	c := strings.ToUpper(strings.TrimSpace(*v))
	if len(c) != 3 {
		return "EUR"
	}
	return c
}

// computedLineItem enriches an input line with iva_amount and total.
type computedLineItem struct {
	InvoiceLineItemInput
	IVAAmount float64
	Total     float64
	Base      float64
}

func computeLineItems(items []InvoiceLineItemInput) []computedLineItem {
	out := make([]computedLineItem, 0, len(items))
	for _, it := range items {
		base := it.Quantity * it.UnitPrice
		iva := base * it.IVARate / 100
		out = append(out, computedLineItem{
			InvoiceLineItemInput: it,
			Base:                 round2(base),
			IVAAmount:            round2(iva),
			Total:                round2(base + iva),
		})
	}
	return out
}

// billingDefaults fills subtotal/iva/total so the template always has coherent
// numbers, deriving from line items when the client did not send explicit values.
type billingValues struct {
	Subtotal  *float64
	IVARate   *float64
	IVAAmount *float64
	Total     *float64
}

func deriveBillingValues(in *InvoiceInput, lines []computedLineItem) billingValues {
	bv := billingValues{
		Subtotal:  in.Subtotal,
		IVARate:   in.IVARate,
		IVAAmount: in.IVAAmount,
		Total:     in.Total,
	}

	// When line items exist they are the source of truth: compute subtotal, IVA
	// and total from them, ignoring any (possibly stale/incorrect) client totals.
	if len(lines) > 0 {
		var subtotal, ivaTotal float64
		for _, l := range lines {
			subtotal += l.Base
			ivaTotal += l.IVAAmount
		}
		subtotal = round2(subtotal)
		ivaTotal = round2(ivaTotal)
		bv.Subtotal = &subtotal
		bv.IVAAmount = &ivaTotal
		total := subtotal + ivaTotal
		if in.DiscountAmount != nil {
			total -= *in.DiscountAmount
		}
		total = round2(total)
		bv.Total = &total
		return bv
	}

	// No line items: fall back to the client-provided values / amount.
	if bv.Subtotal == nil {
		amt := round2(in.Amount)
		bv.Subtotal = &amt
	}
	if bv.Total == nil {
		total := round2(*bv.Subtotal)
		if bv.IVAAmount != nil {
			total = round2(total + *bv.IVAAmount)
		}
		if in.DiscountAmount != nil {
			total = round2(total - *in.DiscountAmount)
		}
		bv.Total = &total
	}
	return bv
}

func marshalTags(tags []string) *string {
	if len(tags) == 0 {
		return nil
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

func unmarshalTags(v sql.NullString) []string {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(v.String), &tags); err != nil {
		return nil
	}
	return tags
}

// replaceInvoiceLineItems deletes existing line items for the invoice and
// inserts the provided computed lines. Runs inside the given tx.
func replaceInvoiceLineItems(ctx context.Context, tx *sql.Tx, invoiceID int64, lines []computedLineItem) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM invoice_line_items WHERE invoice_id = ?`, invoiceID); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO invoice_line_items
				(invoice_id, description, quantity, unit_price, iva_rate, iva_amount, line_total, sort_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, invoiceID, l.Description, l.Quantity, l.UnitPrice, l.IVARate, l.IVAAmount, l.Total, i); err != nil {
			return err
		}
	}
	return nil
}

func nullF(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func nullS(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullI(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// resolveInvoiceLogoURL returns the logo to print on the invoice: the branding
// logo if configured, otherwise the restaurant avatar (both stored as CDN URLs).
func (s *Server) resolveInvoiceLogoURL(ctx context.Context, restaurantID int, branding restaurantBrandingCfg) string {
	if v := strings.TrimSpace(branding.LogoURL); v != "" {
		return v
	}
	var avatar sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT avatar FROM restaurants WHERE id = ?`, restaurantID).Scan(&avatar); err != nil {
		return ""
	}
	return strings.TrimSpace(avatar.String)
}

// loadFullInvoice loads a single invoice (scoped to restaurant) with every
// billing field and its line items. Returns (nil, nil) when not found.
func (s *Server) loadFullInvoice(ctx context.Context, restaurantID, invoiceID int) (*Invoice, error) {
	var inv Invoice
	var (
		invoiceNumber, customerSurname, customerDniCif, customerPhone                                 sql.NullString
		addrStreet, addrNumber, addrPostal, addrCity, addrProvince, addrCountry                       sql.NullString
		paymentMethod, accountImageURL, paymentDate, reservationDate, reservationCustomerName, pdfURL sql.NullString
		reservationID, reservationPartySize                                                           sql.NullInt64
		subtotal, ivaRate, ivaAmount, total, discountValue, discountAmount, depositAmount             sql.NullFloat64
		discountType, discountReason, dueDate, internalNotes, category, tags, depositType             sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			id, restaurant_id, invoice_number, customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date, reservation_customer_name, reservation_party_size,
			pdf_url, pdf_template, currency, subtotal, iva_rate, iva_amount, total,
			discount_type, discount_value, discount_amount, discount_reason,
			due_date, internal_notes, category, tags, deposit_type, deposit_amount,
			created_at, updated_at
		FROM invoices
		WHERE id = ? AND restaurant_id = ?
	`, invoiceID, restaurantID).Scan(
		&inv.ID, &inv.RestaurantID, &invoiceNumber, &inv.CustomerName, &customerSurname, &inv.CustomerEmail, &customerDniCif, &customerPhone,
		&addrStreet, &addrNumber, &addrPostal, &addrCity, &addrProvince, &addrCountry,
		&inv.Amount, &paymentMethod, &accountImageURL, &inv.InvoiceDate, &paymentDate,
		&inv.Status, &inv.IsReservation, &reservationID, &reservationDate, &reservationCustomerName, &reservationPartySize,
		&pdfURL, &inv.PdfTemplate, &inv.Currency, &subtotal, &ivaRate, &ivaAmount, &total,
		&discountType, &discountValue, &discountAmount, &discountReason,
		&dueDate, &internalNotes, &category, &tags, &depositType, &depositAmount,
		&inv.CreatedAt, &inv.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	inv.InvoiceNumber = nullS(invoiceNumber)
	inv.CustomerSurname = nullS(customerSurname)
	inv.CustomerDniCif = nullS(customerDniCif)
	inv.CustomerPhone = nullS(customerPhone)
	inv.CustomerAddressStreet = nullS(addrStreet)
	inv.CustomerAddressNumber = nullS(addrNumber)
	inv.CustomerAddressPostalCode = nullS(addrPostal)
	inv.CustomerAddressCity = nullS(addrCity)
	inv.CustomerAddressProvince = nullS(addrProvince)
	inv.CustomerAddressCountry = nullS(addrCountry)
	inv.PaymentMethod = nullS(paymentMethod)
	inv.AccountImageURL = nullS(accountImageURL)
	inv.PaymentDate = nullS(paymentDate)
	inv.ReservationID = nullI(reservationID)
	inv.ReservationDate = nullS(reservationDate)
	inv.ReservationCustomerName = nullS(reservationCustomerName)
	inv.ReservationPartySize = nullI(reservationPartySize)
	inv.PdfURL = nullS(pdfURL)
	inv.Subtotal = nullF(subtotal)
	inv.IVARate = nullF(ivaRate)
	inv.IVAAmount = nullF(ivaAmount)
	inv.Total = nullF(total)
	inv.DiscountType = nullS(discountType)
	inv.DiscountValue = nullF(discountValue)
	inv.DiscountAmount = nullF(discountAmount)
	inv.DiscountReason = nullS(discountReason)
	inv.DueDate = nullS(dueDate)
	inv.InternalNotes = nullS(internalNotes)
	inv.Category = nullS(category)
	inv.Tags = unmarshalTags(tags)
	inv.DepositType = nullS(depositType)
	inv.DepositAmount = nullF(depositAmount)

	items, err := s.loadInvoiceLineItems(ctx, inv.ID)
	if err != nil {
		return nil, err
	}
	inv.LineItems = items
	return &inv, nil
}

// loadInvoiceLineItems returns the ordered line items for an invoice.
func (s *Server) loadInvoiceLineItems(ctx context.Context, invoiceID int) ([]InvoiceLineItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, description, quantity, unit_price, iva_rate, iva_amount, line_total
		FROM invoice_line_items
		WHERE invoice_id = ?
		ORDER BY sort_order ASC, id ASC
	`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]InvoiceLineItem, 0)
	for rows.Next() {
		var it InvoiceLineItem
		if err := rows.Scan(&it.ID, &it.Description, &it.Quantity, &it.UnitPrice, &it.IVARate, &it.IVAAmount, &it.Total); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}
