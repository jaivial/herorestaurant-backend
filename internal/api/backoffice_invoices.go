package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// InvoiceLineItem is a single billable line on an invoice.
type InvoiceLineItem struct {
	ID          int     `json:"id,omitempty"`
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	IVARate     float64 `json:"iva_rate"`
	IVAAmount   float64 `json:"iva_amount"`
	Total       float64 `json:"total"`
}

// Invoice types
type Invoice struct {
	ID                        int     `json:"id"`
	RestaurantID              int     `json:"restaurant_id"`
	InvoiceNumber             *string `json:"invoice_number"`
	CustomerName              string  `json:"customer_name"`
	CustomerSurname           *string `json:"customer_surname"`
	CustomerEmail             string  `json:"customer_email"`
	CustomerDniCif            *string `json:"customer_dni_cif"`
	CustomerPhone             *string `json:"customer_phone"`
	CustomerAddressStreet     *string `json:"customer_address_street"`
	CustomerAddressNumber     *string `json:"customer_address_number"`
	CustomerAddressPostalCode *string `json:"customer_address_postal_code"`
	CustomerAddressCity       *string `json:"customer_address_city"`
	CustomerAddressProvince   *string `json:"customer_address_province"`
	CustomerAddressCountry    *string `json:"customer_address_country"`
	Amount                    float64 `json:"amount"`
	PaymentMethod             *string `json:"payment_method"`
	AccountImageURL           *string `json:"account_image_url"`
	InvoiceDate               string  `json:"invoice_date"`
	PaymentDate               *string `json:"payment_date"`
	Status                    string  `json:"status"`
	IsReservation             bool    `json:"is_reservation"`
	ReservationID             *int    `json:"reservation_id"`
	ReservationDate           *string `json:"reservation_date"`
	ReservationCustomerName   *string `json:"reservation_customer_name"`
	ReservationPartySize      *int    `json:"reservation_party_size"`
	PdfURL                    *string `json:"pdf_url"`
	// Billing detail
	PdfTemplate    string            `json:"pdf_template"`
	Currency       string            `json:"currency"`
	Subtotal       *float64          `json:"subtotal"`
	IVARate        *float64          `json:"iva_rate"`
	IVAAmount      *float64          `json:"iva_amount"`
	Total          *float64          `json:"total"`
	DiscountType   *string           `json:"discount_type"`
	DiscountValue  *float64          `json:"discount_value"`
	DiscountAmount *float64          `json:"discount_amount"`
	DiscountReason *string           `json:"discount_reason"`
	DueDate        *string           `json:"due_date"`
	InternalNotes  *string           `json:"internal_notes"`
	Category       *string           `json:"category"`
	Tags           []string          `json:"tags"`
	DepositType    *string           `json:"deposit_type"`
	DepositAmount  *float64          `json:"deposit_amount"`
	LineItems      []InvoiceLineItem `json:"line_items"`
	CreatedAt      string            `json:"created_at"`
	UpdatedAt      string            `json:"updated_at"`
}

type InvoiceInput struct {
	CustomerName              string  `json:"customer_name"`
	CustomerSurname           *string `json:"customer_surname"`
	CustomerEmail             string  `json:"customer_email"`
	CustomerDniCif            *string `json:"customer_dni_cif"`
	CustomerPhone             *string `json:"customer_phone"`
	CustomerAddressStreet     *string `json:"customer_address_street"`
	CustomerAddressNumber     *string `json:"customer_address_number"`
	CustomerAddressPostalCode *string `json:"customer_address_postal_code"`
	CustomerAddressCity       *string `json:"customer_address_city"`
	CustomerAddressProvince   *string `json:"customer_address_province"`
	CustomerAddressCountry    *string `json:"customer_address_country"`
	Amount                    float64 `json:"amount"`
	PaymentMethod             *string `json:"payment_method"`
	AccountImageURL           *string `json:"account_image_url"`
	InvoiceDate               string  `json:"invoice_date"`
	PaymentDate               *string `json:"payment_date"`
	Status                    string  `json:"status"`
	IsReservation             bool    `json:"is_reservation"`
	ReservationID             *int    `json:"reservation_id"`
	ReservationDate           *string `json:"reservation_date"`
	ReservationCustomerName   *string `json:"reservation_customer_name"`
	ReservationPartySize      *int    `json:"reservation_party_size"`
	// Billing detail
	InvoiceNumber  *string                `json:"invoice_number"`
	PdfTemplate    *string                `json:"pdf_template"`
	Currency       *string                `json:"currency"`
	Subtotal       *float64               `json:"subtotal"`
	IVARate        *float64               `json:"iva_rate"`
	IVAAmount      *float64               `json:"iva_amount"`
	Total          *float64               `json:"total"`
	DiscountType   *string                `json:"discount_type"`
	DiscountValue  *float64               `json:"discount_value"`
	DiscountAmount *float64               `json:"discount_amount"`
	DiscountReason *string                `json:"discount_reason"`
	DueDate        *string                `json:"due_date"`
	InternalNotes  *string                `json:"internal_notes"`
	Category       *string                `json:"category"`
	Tags           []string               `json:"tags"`
	DepositType    *string                `json:"deposit_type"`
	DepositAmount  *float64               `json:"deposit_amount"`
	LineItems      []InvoiceLineItemInput `json:"line_items"`
}

// InvoiceLineItemInput is the create/update payload for a line item.
type InvoiceLineItemInput struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	IVARate     float64 `json:"iva_rate"`
}

type InvoiceListParams struct {
	Search        string `json:"search"`
	SearchBy      string `json:"search_by"`
	Status        string `json:"status"`
	DateType      string `json:"date_type"`
	DateFrom      string `json:"date_from"`
	DateTo        string `json:"date_to"`
	IsReservation *bool  `json:"is_reservation"`
	Sort          string `json:"sort"`
	Page          int    `json:"page"`
	Limit         int    `json:"limit"`
}

type ReservationSearchResult struct {
	ID              int    `json:"id"`
	CustomerName    string `json:"customer_name"`
	ContactEmail    string `json:"contact_email"`
	ContactPhone    string `json:"contact_phone"`
	ReservationDate string `json:"reservation_date"`
	ReservationTime string `json:"reservation_time"`
	PartySize       int    `json:"party_size"`
}

type Restaurant struct {
	ID     int     `json:"id"`
	Slug   string  `json:"slug"`
	Name   string  `json:"name"`
	Avatar *string `json:"avatar"`
}

// Handle invoice list with filters
func (s *Server) handleBOInvoicesList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID

	// Parse query params
	params := InvoiceListParams{
		Search:   strings.TrimSpace(r.URL.Query().Get("search")),
		SearchBy: strings.TrimSpace(r.URL.Query().Get("search_by")),
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
		DateType: strings.TrimSpace(r.URL.Query().Get("date_type")),
		DateFrom: strings.TrimSpace(r.URL.Query().Get("date_from")),
		DateTo:   strings.TrimSpace(r.URL.Query().Get("date_to")),
		Sort:     strings.TrimSpace(r.URL.Query().Get("sort")),
	}

	if p := r.URL.Query().Get("page"); p != "" {
		if page, err := strconv.Atoi(p); err == nil && page > 0 {
			params.Page = page
		}
	} else {
		params.Page = 1
	}

	if l := r.URL.Query().Get("limit"); l != "" {
		if limit, err := strconv.Atoi(l); err == nil && limit > 0 {
			params.Limit = limit
		}
	} else {
		params.Limit = 20
	}

	if isRes := r.URL.Query().Get("is_reservation"); isRes != "" {
		b := isRes == "true"
		params.IsReservation = &b
	}

	baseWhere := " FROM invoices WHERE restaurant_id = ?"
	args := []any{restaurantID}

	if params.Search != "" {
		prefix := params.Search + "%"
		switch strings.ToLower(params.SearchBy) {
		case "invoice_number":
			baseWhere += " AND invoice_number LIKE ?"
			args = append(args, prefix)
		case "email":
			baseWhere += " AND customer_email LIKE ?"
			args = append(args, prefix)
		case "name":
			baseWhere += " AND customer_name LIKE ?"
			args = append(args, prefix)
		default:
			baseWhere += " AND (customer_name LIKE ? OR customer_email LIKE ?)"
			args = append(args, prefix, prefix)
		}
	}

	if params.Status != "" {
		baseWhere += " AND status = ?"
		args = append(args, params.Status)
	}

	if params.DateType == "reservation_date" {
		if params.DateFrom != "" {
			baseWhere += " AND reservation_date >= ?"
			args = append(args, params.DateFrom)
		}
		if params.DateTo != "" {
			baseWhere += " AND reservation_date <= ?"
			args = append(args, params.DateTo)
		}
	} else {
		if params.DateFrom != "" {
			baseWhere += " AND invoice_date >= ?"
			args = append(args, params.DateFrom)
		}
		if params.DateTo != "" {
			baseWhere += " AND invoice_date <= ?"
			args = append(args, params.DateTo)
		}
	}

	if params.IsReservation != nil {
		if *params.IsReservation {
			baseWhere += " AND is_reservation = 1"
		} else {
			baseWhere += " AND is_reservation = 0"
		}
	}

	orderBy := " ORDER BY invoice_date DESC"
	switch params.Sort {
	case "amount_asc":
		orderBy = " ORDER BY amount ASC"
	case "amount_desc":
		orderBy = " ORDER BY amount DESC"
	case "date_asc":
		orderBy = " ORDER BY invoice_date ASC"
	}

	offset := (params.Page - 1) * params.Limit
	query := `
		SELECT 
			id, restaurant_id, invoice_number,
			customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date,
			reservation_customer_name, reservation_party_size, pdf_url,
			currency, subtotal, iva_rate, iva_amount, total,
			created_at, updated_at
	` + baseWhere + orderBy + " LIMIT ? OFFSET ?"
	queryArgs := append(append([]any{}, args...), params.Limit, offset)

	// Execute query
	rows, err := s.db.QueryContext(r.Context(), query, queryArgs...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error querying invoices: "+err.Error())
		return
	}
	defer rows.Close()

	var invoices []Invoice
	for rows.Next() {
		var inv Invoice
		var subtotal, ivaRate, ivaAmount, totalF sql.NullFloat64
		var invoiceNumber sql.NullString
		err := rows.Scan(
			&inv.ID, &inv.RestaurantID, &invoiceNumber,
			&inv.CustomerName, &inv.CustomerSurname, &inv.CustomerEmail, &inv.CustomerDniCif, &inv.CustomerPhone,
			&inv.CustomerAddressStreet, &inv.CustomerAddressNumber, &inv.CustomerAddressPostalCode,
			&inv.CustomerAddressCity, &inv.CustomerAddressProvince, &inv.CustomerAddressCountry,
			&inv.Amount, &inv.PaymentMethod, &inv.AccountImageURL, &inv.InvoiceDate, &inv.PaymentDate,
			&inv.Status, &inv.IsReservation, &inv.ReservationID, &inv.ReservationDate,
			&inv.ReservationCustomerName, &inv.ReservationPartySize, &inv.PdfURL,
			&inv.Currency,
			&subtotal, &ivaRate, &ivaAmount, &totalF,
			&inv.CreatedAt, &inv.UpdatedAt,
		)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error scanning invoice: "+err.Error())
			return
		}
		// invoice_amount_consistency_v1: the send modal and templates must see
		// the final total (with IVA), not just the base amount.
		inv.Subtotal = nullF(subtotal)
		inv.IVARate = nullF(ivaRate)
		inv.IVAAmount = nullF(ivaAmount)
		inv.Total = nullF(totalF)
		inv.InvoiceNumber = nullS(invoiceNumber)
		invoices = append(invoices, inv)
	}

	countQuery := "SELECT COUNT(*)" + baseWhere
	var total int
	if err := s.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		total = 0
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"invoices": invoices,
		"total":    total,
		"page":     params.Page,
		"limit":    params.Limit,
	})
}

// Handle get single invoice
func (s *Server) handleBOInvoiceGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}

	var inv Invoice
	var getSubtotal, getIvaRate, getIvaAmount, getTotal sql.NullFloat64
	var invoiceNumber sql.NullString
	err = s.db.QueryRowContext(r.Context(), `
		SELECT 
			id, restaurant_id, invoice_number,
			customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date,
			reservation_customer_name, reservation_party_size, pdf_url,
			currency, subtotal, iva_rate, iva_amount, total,
			created_at, updated_at
		FROM invoices
		WHERE id = ? AND restaurant_id = ?
	`, invoiceID, a.ActiveRestaurantID).Scan(
		&inv.ID, &inv.RestaurantID, &invoiceNumber,
		&inv.CustomerName, &inv.CustomerSurname, &inv.CustomerEmail, &inv.CustomerDniCif, &inv.CustomerPhone,
		&inv.CustomerAddressStreet, &inv.CustomerAddressNumber, &inv.CustomerAddressPostalCode,
		&inv.CustomerAddressCity, &inv.CustomerAddressProvince, &inv.CustomerAddressCountry,
		&inv.Amount, &inv.PaymentMethod, &inv.AccountImageURL, &inv.InvoiceDate, &inv.PaymentDate,
		&inv.Status, &inv.IsReservation, &inv.ReservationID, &inv.ReservationDate,
		&inv.ReservationCustomerName, &inv.ReservationPartySize, &inv.PdfURL,
		&inv.Currency,
		&getSubtotal, &getIvaRate, &getIvaAmount, &getTotal,
		&inv.CreatedAt, &inv.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching invoice: "+err.Error())
		return
	}
	// invoice_amount_consistency_v1 (see list handler).
	inv.Subtotal = nullF(getSubtotal)
	inv.IVARate = nullF(getIvaRate)
	inv.IVAAmount = nullF(getIvaAmount)
	inv.Total = nullF(getTotal)
	inv.InvoiceNumber = nullS(invoiceNumber)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"invoice": inv,
	})
}

// Handle create invoice
func (s *Server) handleBOInvoiceCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var input InvoiceInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate required fields
	if input.CustomerName == "" || input.CustomerEmail == "" || input.InvoiceDate == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Missing required fields: customer_name, customer_email, invoice_date")
		return
	}

	// Default status to 'borrador' if not provided
	if input.Status == "" {
		input.Status = "borrador"
	}

	normalizeInvoiceDates(&input)

	// Validate status
	switch input.Status {
	case "borrador", "solicitada", "pendiente", "enviada":
	default:
		input.Status = "borrador"
	}

	currency := normalizeCurrency(input.Currency)
	lines := computeLineItems(input.LineItems)
	bv := deriveBillingValues(&input, lines)
	if len(lines) > 0 && bv.Total != nil {
		input.Amount = *bv.Total
	}
	tags := marshalTags(input.Tags)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating invoice: "+err.Error())
		return
	}
	defer tx.Rollback()

	// Coordination id: invoice_numbering_v1 - the restaurant's invoice
	// settings: the numbering series and the default PDF design.
	settings, err := s.ensureInvoiceNumberSettingsRow(r.Context(), tx, a.ActiveRestaurantID, time.Now().Year())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando la configuracion de facturas: "+err.Error())
		return
	}
	template := normalizePdfTemplate(input.PdfTemplate)
	if input.PdfTemplate == nil || strings.TrimSpace(*input.PdfTemplate) == "" {
		template = settings.DefaultPdfTemplate
	}

	// Coordination id: invoice_numbering_v1 - the number identifies the
	// invoice in the header of its PDF: the custom one when the operator
	// typed it, the automatic one of the restaurant's series otherwise.
	number, err := s.resolveInvoiceNumber(r.Context(), tx, a.ActiveRestaurantID, input.InvoiceNumber, "")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error asignando el numero de factura: "+err.Error())
		return
	}

	result, err := tx.ExecContext(r.Context(), `
		INSERT INTO invoices (
			restaurant_id, invoice_number, customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date,
			reservation_customer_name, reservation_party_size,
			pdf_template, currency, subtotal, iva_rate, iva_amount, total,
			discount_type, discount_value, discount_amount, discount_reason,
			due_date, internal_notes, category, tags, deposit_type, deposit_amount
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.ActiveRestaurantID, number,
		input.CustomerName, input.CustomerSurname, input.CustomerEmail, input.CustomerDniCif, input.CustomerPhone,
		input.CustomerAddressStreet, input.CustomerAddressNumber, input.CustomerAddressPostalCode,
		input.CustomerAddressCity, input.CustomerAddressProvince, input.CustomerAddressCountry,
		input.Amount, input.PaymentMethod, input.AccountImageURL, input.InvoiceDate, input.PaymentDate,
		input.Status, input.IsReservation, input.ReservationID, input.ReservationDate,
		input.ReservationCustomerName, input.ReservationPartySize,
		template, currency, bv.Subtotal, bv.IVARate, bv.IVAAmount, bv.Total,
		input.DiscountType, input.DiscountValue, input.DiscountAmount, input.DiscountReason,
		input.DueDate, input.InternalNotes, input.Category, tags, input.DepositType, input.DepositAmount)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating invoice: "+err.Error())
		return
	}

	id, _ := result.LastInsertId()
	if err := replaceInvoiceLineItems(r.Context(), tx, id, lines); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving line items: "+err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating invoice: "+err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"id":      id,
		"message": "Invoice created successfully",
	})
}

// Handle update invoice
func (s *Server) handleBOInvoiceUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}

	var input InvoiceInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate required fields
	if input.CustomerName == "" || input.CustomerEmail == "" || input.InvoiceDate == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Missing required fields: customer_name, customer_email, invoice_date")
		return
	}

	// Validate status
	switch input.Status {
	case "borrador", "solicitada", "pendiente", "enviada":
	default:
		input.Status = "borrador"
	}

	normalizeInvoiceDates(&input)

	template := normalizePdfTemplate(input.PdfTemplate)
	currency := normalizeCurrency(input.Currency)
	lines := computeLineItems(input.LineItems)
	bv := deriveBillingValues(&input, lines)
	if len(lines) > 0 && bv.Total != nil {
		input.Amount = *bv.Total
	}
	tags := marshalTags(input.Tags)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating invoice: "+err.Error())
		return
	}
	defer tx.Rollback()

	// Coordination id: invoice_numbering_v1 - a save never wipes the number:
	// the custom one the operator typed wins, the stored one is kept when the
	// payload does not mention it, and an invoice without a number gets the
	// automatic one of the restaurant's series.
	var storedNumber sql.NullString
	if err := tx.QueryRowContext(r.Context(), `
		SELECT invoice_number FROM invoices WHERE id = ? AND restaurant_id = ?
	`, invoiceID, a.ActiveRestaurantID).Scan(&storedNumber); err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading invoice: "+err.Error())
		return
	}
	number, err := s.resolveInvoiceNumber(r.Context(), tx, a.ActiveRestaurantID, input.InvoiceNumber, storedNumber.String)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error asignando el numero de factura: "+err.Error())
		return
	}

	res, err := tx.ExecContext(r.Context(), `
		UPDATE invoices SET
			invoice_number = ?, customer_name = ?, customer_surname = ?, customer_email = ?, customer_dni_cif = ?, customer_phone = ?,
			customer_address_street = ?, customer_address_number = ?, customer_address_postal_code = ?,
			customer_address_city = ?, customer_address_province = ?, customer_address_country = ?,
			amount = ?, payment_method = ?, account_image_url = ?, invoice_date = ?, payment_date = ?,
			status = ?, is_reservation = ?, reservation_id = ?, reservation_date = ?,
			reservation_customer_name = ?, reservation_party_size = ?,
			pdf_template = ?, currency = ?, subtotal = ?, iva_rate = ?, iva_amount = ?, total = ?,
			discount_type = ?, discount_value = ?, discount_amount = ?, discount_reason = ?,
			due_date = ?, internal_notes = ?, category = ?, tags = ?, deposit_type = ?, deposit_amount = ?
		WHERE id = ? AND restaurant_id = ?
	`, number, input.CustomerName, input.CustomerSurname, input.CustomerEmail, input.CustomerDniCif, input.CustomerPhone,
		input.CustomerAddressStreet, input.CustomerAddressNumber, input.CustomerAddressPostalCode,
		input.CustomerAddressCity, input.CustomerAddressProvince, input.CustomerAddressCountry,
		input.Amount, input.PaymentMethod, input.AccountImageURL, input.InvoiceDate, input.PaymentDate,
		input.Status, input.IsReservation, input.ReservationID, input.ReservationDate,
		input.ReservationCustomerName, input.ReservationPartySize,
		template, currency, bv.Subtotal, bv.IVARate, bv.IVAAmount, bv.Total,
		input.DiscountType, input.DiscountValue, input.DiscountAmount, input.DiscountReason,
		input.DueDate, input.InternalNotes, input.Category, tags, input.DepositType, input.DepositAmount,
		invoiceID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating invoice: "+err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Ensure the invoice belongs to this restaurant before touching line items.
		var exists int
		_ = tx.QueryRowContext(r.Context(), `SELECT 1 FROM invoices WHERE id = ? AND restaurant_id = ?`, invoiceID, a.ActiveRestaurantID).Scan(&exists)
		if exists == 0 {
			httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
			return
		}
	}
	if err := replaceInvoiceLineItems(r.Context(), tx, int64(invoiceID), lines); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving line items: "+err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating invoice: "+err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Invoice updated successfully",
	})
}

// Handle delete invoice
func (s *Server) handleBOInvoiceDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}

	result, err := s.db.ExecContext(r.Context(), `
		DELETE FROM invoices WHERE id = ? AND restaurant_id = ?
	`, invoiceID, a.ActiveRestaurantID)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error deleting invoice: "+err.Error())
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Invoice deleted successfully",
	})
}

// Handle search reservations for auto-fill
func (s *Server) handleBOInvoicesSearchReservation(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	restaurantID := a.ActiveRestaurantID

	// Parse query params
	dateFrom := strings.TrimSpace(r.URL.Query().Get("date_from"))
	dateTo := strings.TrimSpace(r.URL.Query().Get("date_to"))
	searchName := strings.TrimSpace(r.URL.Query().Get("name"))
	searchPhone := strings.TrimSpace(r.URL.Query().Get("phone"))
	partySizeStr := strings.TrimSpace(r.URL.Query().Get("party_size"))
	reservationTime := strings.TrimSpace(r.URL.Query().Get("time"))

	// Build query
	query := `
		SELECT id, customer_name, contact_email, contact_phone, reservation_date, reservation_time, party_size
		FROM bookings
		WHERE restaurant_id = ? AND status != 'cancelled'
	`
	args := []interface{}{restaurantID}

	if dateFrom != "" {
		query += " AND reservation_date >= ?"
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		query += " AND reservation_date <= ?"
		args = append(args, dateTo)
	}
	if searchName != "" {
		query += " AND customer_name LIKE CONCAT('%', ?, '%')"
		args = append(args, searchName)
	}
	if searchPhone != "" {
		query += " AND contact_phone LIKE CONCAT('%', ?, '%')"
		args = append(args, searchPhone)
	}
	if partySizeStr != "" {
		if partySize, err := strconv.Atoi(partySizeStr); err == nil {
			query += " AND party_size = ?"
			args = append(args, partySize)
		}
	}
	if reservationTime != "" {
		query += " AND reservation_time = ?"
		args = append(args, reservationTime)
	}

	query += " ORDER BY reservation_date DESC, reservation_time DESC LIMIT 20"

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error searching reservations: "+err.Error())
		return
	}
	defer rows.Close()

	// Initialize so an empty result marshals as [] instead of null —
	// clients type this field as an array (invoices_date_coercion_v1 sibling
	// contract: invoices_reservation_search_null_v1).
	reservations := make([]ReservationSearchResult, 0, 20)
	for rows.Next() {
		var res ReservationSearchResult
		err := rows.Scan(
			&res.ID, &res.CustomerName, &res.ContactEmail, &res.ContactPhone,
			&res.ReservationDate, &res.ReservationTime, &res.PartySize,
		)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error scanning reservation: "+err.Error())
			return
		}
		reservations = append(reservations, res)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"reservations": reservations,
	})
}

// Handle send invoice (generate PDF, upload, send email)
func (s *Server) handleBOInvoiceSend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}

	// Parse optional body with subject/message
	var req struct {
		Subject     *string `json:"subject,omitempty"`
		Message     *string `json:"message,omitempty"`
		MessageHTML *string `json:"message_html,omitempty"`
	}
	// Ignore body parse errors — body is optional
	json.NewDecoder(r.Body).Decode(&req)

	// Load email provider config
	cfg, _ := s.loadEmailProviderConfig(r.Context(), a.ActiveRestaurantID)

	isComplete, missingFields := checkEmailProviderCompleteness(cfg)
	if !isComplete {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":       false,
			"message":       "Email settings incomplete. Configure them in Settings → Email.",
			"isComplete":    false,
			"missingFields": missingFields,
		})
		return
	}

	// Load the full invoice (billing detail + line items).
	inv, err := s.loadFullInvoice(r.Context(), a.ActiveRestaurantID, invoiceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching invoice: "+err.Error())
		return
	}
	if inv == nil {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}

	info, _ := s.loadRestaurantInfo(r.Context(), a.ActiveRestaurantID)
	branding, _ := s.loadRestaurantBranding(r.Context(), a.ActiveRestaurantID)
	branding.LogoURL = s.resolveInvoiceLogoURL(r.Context(), a.ActiveRestaurantID, branding)
	fromName := "Restaurante"
	if branding.BrandName != "" {
		fromName = branding.BrandName
	}

	baseURL := "https://backoffice-dev.menustudioai.com"
	invoiceURL := fmt.Sprintf("%s/app/facturas?tab=añadir&id=%d", baseURL, inv.ID)

	// View model shared by the email summary (and the PDF).
	data := buildInvoiceRenderData(inv, info, branding)
	invoiceLabel := data.Number

	subject := fmt.Sprintf("Factura - %s", fromName)
	if invoiceLabel != "" {
		subject = fmt.Sprintf("Factura %s - %s", invoiceLabel, fromName)
	}
	if req.Subject != nil && *req.Subject != "" {
		subject = *req.Subject
	}

	customMessage := ""
	if req.Message != nil {
		customMessage = *req.Message
	}
	// Email body: app shell + per-template invoice summary (HTML inside email).
	customHTML := ""
	if req.MessageHTML != nil {
		customHTML = sanitizeEmailHTML(*req.MessageHTML)
	}
	htmlBody := buildBackofficeInvoiceEmailHTML(data, invoiceURL, customMessage, customHTML)

	// Attachment: full-fidelity PDF of the selected template.
	var attachments []emailAttachment
	pdfBytes, pdfErr := s.generateInvoicePDF(r.Context(), inv, info, branding)
	if pdfErr != nil {
		log.Printf("[invoice-send] PDF generation failed for #%d: %v", inv.ID, pdfErr)
	} else if len(pdfBytes) > 0 {
		attachments = append(attachments, emailAttachment{
			Filename:    invoicePDFFilename(invoiceLabel),
			ContentType: "application/pdf",
			Data:        pdfBytes,
		})
	}

	// Determine from address (branding override > provider default > customer).
	fromAddr := cfg.SMTPFromEmail
	if cfg.Provider == "gmail" {
		fromAddr = cfg.GmailFromEmail
	}
	if strings.TrimSpace(branding.EmailFromAddress) != "" {
		fromAddr = strings.TrimSpace(branding.EmailFromAddress)
	}
	if fromAddr == "" {
		fromAddr = inv.CustomerEmail
	}

	if err := sendViaConfigWithAttachments(r.Context(), cfg, fromName, fromAddr, inv.CustomerEmail, subject, htmlBody, attachments); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error sending email: " + err.Error(),
		})
		return
	}

	// Best-effort: upload the PDF to the CDN and record its URL.
	pdfURL := ""
	if len(pdfBytes) > 0 && s.bunnyConfigured(r.Context(), inv.RestaurantID) {
		objectPath := fmt.Sprintf("%d/facturas/pdf/factura_%d.pdf", inv.RestaurantID, inv.ID)
		if uerr := s.bunnyPut(r.Context(), inv.RestaurantID, objectPath, pdfBytes, "application/pdf"); uerr == nil {
			pdfURL = s.bunnyPullURL(r.Context(), inv.RestaurantID, objectPath)
		} else {
			log.Printf("[invoice-send] PDF upload failed for #%d: %v", inv.ID, uerr)
		}
	}

	if pdfURL != "" {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE invoices SET status = 'enviada', pdf_url = ? WHERE id = ? AND restaurant_id = ?`, pdfURL, invoiceID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error updating invoice status: "+err.Error())
			return
		}
	} else {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE invoices SET status = 'enviada' WHERE id = ? AND restaurant_id = ?`, invoiceID, a.ActiveRestaurantID); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error updating invoice status: "+err.Error())
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"message":      "Invoice sent successfully",
		"pdf_url":      pdfURL,
		"pdf_attached": len(attachments) > 0,
	})
}

// handleBOInvoiceSendWhatsApp sends the invoice to the customer's WhatsApp:
// the (operator-edited) text first, then the PDF as a document, through the
// restaurant's own gateway — the same tracked path as booking confirmations.
//
// Coordination id: invoice_send_whatsapp_v1
//
// Contract (review iteration 4):
//   - Idempotency: the operator's client uses one idempotency_key per send
//     intent (persisted client-side per user+restaurant+invoice, recoverable
//     via the attempt endpoint after logout). Retries with the same key
//     resume the stored attempt and never re-run a stage that already
//     succeeded. A deliberate resend after full success uses a fresh key.
//   - No provider idempotency: the gateway (Evolution) has no provider-side
//     idempotency key, so exactly-once is NOT claimed. Each stage is claimed
//     atomically in the DB (conditional UPDATE to inflight) BEFORE the
//     provider call. A stage found inflight/unknown on resume is retained
//     and reported (code=stage_uncertain), never re-sent automatically.
//     Same-key reclaim is allowed ONLY from unknown: the request that wrote
//     unknown demonstrably ended (it persisted the status after the provider
//     call returned). A stage in inflight may still have a live request
//     behind it, so it is never reclaimed under the same key — the only way
//     forward is the operator explicitly starting a new intent (fresh key)
//     after verifying delivery, accepting that it DUPLICATES the message if
//     the original actually arrived; the old row stays as an audit record
//     (no age-based reclaim, no assumed request duration).
//   - Error taxonomy: only failures that demonstrably happen BEFORE the
//     provider call (gateway not configured, WhatsApp disconnected, PDF
//     generation/upload failure) mark a stage error, which a same-key retry
//     may re-run automatically. Any provider-call failure (timeout, 5xx,
//     network cut after acceptance, parse errors) marks the stage unknown
//     and requires the explicit operator retry — delivery is uncertain.
//   - Concurrency: two concurrent same-key requests cannot both send; the
//     loser's conditional UPDATE affects 0 rows and it reloads + reports
//     instead of sending blind.
//   - Phone anchoring: the destination is frozen in the attempt row at
//     creation. A resumed attempt whose invoice phone no longer matches is
//     rejected with code=phone_changed (the old attempt is preserved; the
//     operator starts a new intent for the new number) so a retry can never
//     deliver to a different number than the original send.
//   - Ambiguous phone: the destination is trusted without confirmation only
//     in explicit international form ("+"/"00" prefix) or when the digits
//     already carry the ES country code (34 followed by 9 digits). Anything
//     else is normalised with the default cc 34 but requires
//     confirm_phone=true; otherwise the response is code=phone_confirm_needed
//     with phone_normalized so the UI asks the operator to confirm first.
//   - Text fidelity: the operator's text goes through the tracked bot path;
//     if it contains URLs, wa_link_buttons_v1 converts them to interactive
//     buttons (the delivered format differs from the raw text and the UI
//     says so). Verbatim delivery with links is NOT claimed.
//   - Invoice status: marked enviada only from borrador/solicitada/
//     pendiente/enviada; a pagada invoice is never moved back.
//   - Stages: text and PDF are reported independently (text_status,
//     pdf_status); a PDF failure never blocks or re-sends the text.
func (s *Server) handleBOInvoiceSendWhatsApp(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}

	var req struct {
		Message        *string `json:"message,omitempty"`
		IdempotencyKey *string `json:"idempotency_key,omitempty"`
		ConfirmPhone   bool    `json:"confirm_phone,omitempty"`
		// RetryStage ("text"|"pdf") is the operator's explicit decision to
		// re-run one stage left unknown by a crashed or uncertain attempt
		// (duplicate risk acknowledged in the UI after verifying with the
		// customer). Claims never touch inflight: a live concurrent request
		// keeps its stage and the claim just fails (0 rows). pending/error
		// claim directly; unknown only via this explicit retry.
		RetryStage string `json:"retry_stage,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&req) // body optional except the key

	idemKey := strings.TrimSpace(deref(req.IdempotencyKey))
	if idemKey == "" || len(idemKey) > 80 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"code":    "idempotency_key_required",
			"message": "Falta la clave de idempotencia del envío",
		})
		return
	}

	// Object-level ACL: the invoice only loads scoped to the session's active
	// restaurant; anything else is a 404, never a cross-tenant send.
	inv, err := s.loadFullInvoice(r.Context(), a.ActiveRestaurantID, invoiceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching invoice: "+err.Error())
		return
	}
	if inv == nil {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}

	// Phone analysis: explicit international forms ("+" / "00" / full digits
	// >9) are trusted as-is; a national-form number gets the default cc 34
	// but is flagged ambiguous so the operator confirms it first.
	rawPhone := deref(inv.CustomerPhone)
	explicitIntl := strings.HasPrefix(rawPhone, "+") || strings.HasPrefix(rawPhone, "00")
	// Normalise the explicit "00" international prefix to "+" before the
	// digits-only parse so it is not double-prefixed with the default cc.
	if strings.HasPrefix(rawPhone, "00") {
		rawPhone = "+" + strings.TrimPrefix(rawPhone, "00")
	}
	digits := onlyDigits(rawPhone)
	// Trusted without confirmation: explicit "+"/"00" international form, or
	// digits already carrying the ES country code (34 + 9 digits). A bare
	// national number (or any other shape) requires operator confirmation.
	ambiguous := !explicitIntl && !(strings.HasPrefix(digits, "34") && len(digits) == 11)
	_, _, cleanPhone, ok := normalizePhoneParts("", rawPhone)
	if !ok || cleanPhone == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"code":    "invalid_phone",
			"message": "La factura no tiene un teléfono de cliente válido",
		})
		return
	}
	if ambiguous && !req.ConfirmPhone {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":          false,
			"code":             "phone_confirm_needed",
			"phone_normalized": "+" + cleanPhone,
			"message":          "Confirma el número de destino antes de enviar",
		})
		return
	}

	// Resume or create the durable attempt.
	attempt, err := s.loadInvoiceWaSend(r.Context(), a.ActiveRestaurantID, idemKey)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error checking send attempt: "+err.Error())
		return
	}
	if attempt != nil && attempt.InvoiceID != invoiceID {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"code":    "idempotency_key_conflict",
			"message": "La clave de idempotencia ya se usó para otra factura",
		})
		return
	}
	if attempt == nil {
		attempt = &invoiceWaSend{RestaurantID: a.ActiveRestaurantID, InvoiceID: invoiceID, IdempotencyKey: idemKey, Phone: cleanPhone, TextStatus: "pending", PdfStatus: "pending"}
		if err := s.insertInvoiceWaSend(r.Context(), attempt); err != nil {
			// Lost the insert race: reload the winner and resume it.
			if attempt, err = s.loadInvoiceWaSend(r.Context(), a.ActiveRestaurantID, idemKey); err != nil || attempt == nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error starting send attempt")
				return
			}
		}
	}

	// Phone anchoring: the destination frozen in the attempt wins. If the
	// invoice phone was edited after the intent started, refuse under this
	// key — a retry must never deliver to a different number than the
	// original send. The old attempt stays as an audit record.
	if attempt.Phone != cleanPhone {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":          false,
			"code":             "phone_changed",
			"attempt_phone":    "+" + attempt.Phone,
			"phone_normalized": "+" + cleanPhone,
			"message":          "El teléfono de la factura cambió desde que se inició este intento. El intento anterior se conserva; inicia un nuevo envío para el nuevo número.",
		})
		return
	}

	writeUncertain := func(stage string) {
		stageStatus := attempt.TextStatus
		if stage == "pdf" {
			stageStatus = attempt.PdfStatus
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":          false,
			"code":             "stage_uncertain",
			"stage":            stage,
			"stage_status":     stageStatus,
			"text_status":      attempt.TextStatus,
			"pdf_status":       attempt.PdfStatus,
			"phone_normalized": "+" + cleanPhone,
			"message":          "Un intento anterior pudo haber enviado " + map[string]string{"text": "el texto", "pdf": "el PDF"}[stage] + " con resultado incierto. No se ha reenviado automáticamente: verifica la entrega con el cliente antes de reenviar.",
		})
	}

	info, _ := s.loadRestaurantInfo(r.Context(), a.ActiveRestaurantID)
	branding, _ := s.loadRestaurantBranding(r.Context(), a.ActiveRestaurantID)
	branding.LogoURL = s.resolveInvoiceLogoURL(r.Context(), a.ActiveRestaurantID, branding)
	brandName := "Restaurante"
	if branding.BrandName != "" {
		brandName = branding.BrandName
	}
	data := buildInvoiceRenderData(inv, info, branding)
	invoiceLabel := data.Number

	// Stage 1: text. Skipped entirely when this attempt already sent it.
	for attempt.TextStatus != "sent" {
		// Atomic claim: only one concurrent request moves the stage out of
		// pending/error (or, with the operator's explicit retry, out of an
		// aged inflight/unknown). The loser reloads and reports.
		claimed, cerr := s.claimInvoiceWaSendStage(r.Context(), attempt.ID, "text", req.RetryStage == "text")
		if cerr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error claiming send stage: "+cerr.Error())
			return
		}
		if !claimed {
			latest, lerr := s.loadInvoiceWaSend(r.Context(), a.ActiveRestaurantID, idemKey)
			if lerr == nil && latest != nil && latest.TextStatus == "sent" {
				attempt = latest
				continue // re-evaluate the loop condition with fresh state
			}
			if latest != nil {
				attempt = latest
			}
			writeUncertain("text")
			return
		}

		// Pre-send local checks: failures here demonstrably never reached the
		// provider, so the stage is error (a same-key retry may re-run it).
		gw, ok := s.botGatewayFor(r.Context(), a.ActiveRestaurantID)
		if !ok {
			s.setInvoiceWaSendStage(r.Context(), attempt.ID, "text", "error", "", "whatsapp_not_configured")
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"code":    "whatsapp_not_configured",
				"message": "WhatsApp no configurado para este restaurante",
			})
			return
		}
		if rec, found, err := s.loadRestaurantUAZAPIInstance(r.Context(), a.ActiveRestaurantID); err == nil && found && strings.EqualFold(rec.Provider, "evolution") {
			if _, refreshErr := s.refreshRestaurantUAZAPIConnectionStatus(r.Context(), a.ActiveRestaurantID); refreshErr != nil {
				log.Printf("[invoice-wa] connection refresh failed for invoice #%d: %v", invoiceID, refreshErr)
			} else if refreshed, ok2, loadErr := s.loadRestaurantUAZAPIInstance(r.Context(), a.ActiveRestaurantID); loadErr == nil && ok2 && !isUAZAPIConnected(refreshed.Status) {
				s.setInvoiceWaSendStage(r.Context(), attempt.ID, "text", "error", "", "whatsapp_disconnected:"+refreshed.Status)
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"code":    "whatsapp_disconnected",
					"message": fmt.Sprintf("WhatsApp desconectado (estado: %s). Conéctalo en Ajustes → WhatsApp.", refreshed.Status),
				})
				return
			}
		}

		customerName := inv.CustomerName
		if inv.CustomerSurname != nil && strings.TrimSpace(*inv.CustomerSurname) != "" {
			customerName = strings.TrimSpace(customerName + " " + *inv.CustomerSurname)
		}
		total := inv.Amount
		if inv.Total != nil {
			total = *inv.Total
		}
		text := strings.TrimSpace(deref(req.Message))
		if text == "" {
			text = fmt.Sprintf("Hola %s, le enviamos la factura %s por importe de %.2f €.\n\nQuedamos a su disposición para cualquier consulta.\n\n%s", customerName, invoiceLabel, total, brandName)
		}
		// The claim already persisted inflight BEFORE this provider call. A
		// provider error is uncertain (timeout/5xx/network cut may still have
		// delivered), so the stage becomes unknown, never error: no automatic
		// retry, only the operator's explicit retry_stage.
		if err := s.sendWhatsAppTextTracked(r.Context(), a.ActiveRestaurantID, gw, cleanPhone, text, "invoice_send"); err != nil {
			s.setInvoiceWaSendStage(r.Context(), attempt.ID, "text", "unknown", "", "text:"+err.Error())
			attempt.TextStatus = "unknown"
			writeUncertain("text")
			return
		}
		attempt.TextStatus = "sent"
		attempt.Phone = cleanPhone
		s.setInvoiceWaSendStage(r.Context(), attempt.ID, "text", "sent", "", "")
		break
	}

	// Stage 2: PDF document. Best effort and independently reported: its
	// failure never re-sends the text and is retried on its own on the next
	// same-key attempt. An inflight/unknown PDF (crash window or provider
	// error) is retained and reported uncertain, never re-sent automatically.
	pdfUncertain := false
	if attempt.TextStatus == "sent" && attempt.PdfStatus != "sent" {
		if (attempt.PdfStatus == "inflight" || attempt.PdfStatus == "unknown") && req.RetryStage != "pdf" {
			pdfUncertain = true
			log.Printf("[invoice-wa] PDF stage %s for #%d: retained, not re-sent", attempt.PdfStatus, inv.ID)
		} else {
			// Local preparation is repeatable and race-free: generation and
			// upload failures are only logged (the stage stays pending), so a
			// concurrent request's inflight state can never be overwritten.
			pdfURL := attempt.PdfURL
			if pdfURL == "" {
				if b, pdfErr := s.generateInvoicePDF(r.Context(), inv, info, branding); pdfErr != nil {
					log.Printf("[invoice-wa] PDF generation failed for #%d: %v", inv.ID, pdfErr)
				} else if len(b) > 0 && s.bunnyConfigured(r.Context(), inv.RestaurantID) {
					objectPath := fmt.Sprintf("%d/facturas/pdf/factura_%d.pdf", inv.RestaurantID, inv.ID)
					if uerr := s.bunnyPut(r.Context(), inv.RestaurantID, objectPath, b, "application/pdf"); uerr == nil {
						pdfURL = s.bunnyPullURL(r.Context(), inv.RestaurantID, objectPath)
					} else {
						log.Printf("[invoice-wa] PDF upload failed for #%d: %v", inv.ID, uerr)
					}
				}
			}
			if pdfURL != "" {
				claimed, cerr := s.claimInvoiceWaSendStage(r.Context(), attempt.ID, "pdf", req.RetryStage == "pdf")
				if cerr != nil {
					httpx.WriteError(w, http.StatusInternalServerError, "Error claiming pdf stage: "+cerr.Error())
					return
				}
				if !claimed {
					latest, lerr := s.loadInvoiceWaSend(r.Context(), a.ActiveRestaurantID, idemKey)
					if lerr == nil && latest != nil && latest.PdfStatus == "sent" {
						attempt = latest
					} else {
						pdfUncertain = true
					}
				} else {
					if gw, ok := s.botGatewayFor(r.Context(), a.ActiveRestaurantID); !ok {
						s.setInvoiceWaSendStage(r.Context(), attempt.ID, "pdf", "error", pdfURL, "whatsapp_not_configured")
					} else if err := gw.SendMedia(r.Context(), cleanPhone, waMedia{Kind: "document", URL: pdfURL, Filename: invoicePDFFilename(invoiceLabel), Caption: "Factura " + invoiceLabel}); err != nil {
						// Provider-call failure: delivery uncertain (see text
						// stage), so unknown, not automatically retryable.
						log.Printf("[invoice-wa] PDF document failed for #%d: %v", inv.ID, err)
						s.setInvoiceWaSendStage(r.Context(), attempt.ID, "pdf", "unknown", pdfURL, "pdf_send:"+err.Error())
						pdfUncertain = true
					} else {
						attempt.PdfStatus = "sent"
						attempt.PdfURL = pdfURL
						s.setInvoiceWaSendStage(r.Context(), attempt.ID, "pdf", "sent", pdfURL, "")
					}
				}
			}
		}
	}

	// Status mirrors the email flow: enviada once the text reached the
	// customer; pdf_url recorded when available.
	// Mark enviada only from pre-send states; a pagada invoice is never
	// moved back (enviada is kept so a same-key resume is a no-op).
	if attempt.TextStatus == "sent" {
		if attempt.PdfURL != "" {
			s.db.ExecContext(r.Context(), `UPDATE invoices SET status = 'enviada', pdf_url = ? WHERE id = ? AND restaurant_id = ? AND status IN ('borrador','solicitada','pendiente','enviada')`, attempt.PdfURL, invoiceID, a.ActiveRestaurantID)
		} else {
			s.db.ExecContext(r.Context(), `UPDATE invoices SET status = 'enviada' WHERE id = ? AND restaurant_id = ? AND status IN ('borrador','solicitada','pendiente','enviada')`, invoiceID, a.ActiveRestaurantID)
		}
	}

	pdfAttached := attempt.PdfStatus == "sent"
	stageMessage := "WhatsApp enviado sin PDF adjunto"
	if pdfAttached {
		stageMessage = "WhatsApp enviado con PDF adjunto"
	} else if pdfUncertain {
		stageMessage = "Texto enviado; el PDF quedó con resultado incierto (pudo haberse enviado en un intento anterior). No se ha reenviado automáticamente."
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"message":          stageMessage,
		"text_sent":        true,
		"pdf_attached":     pdfAttached,
		"pdf_uncertain":    pdfUncertain,
		"pdf_url":          attempt.PdfURL,
		"text_status":      attempt.TextStatus,
		"pdf_status":       attempt.PdfStatus,
		"phone_normalized": "+" + cleanPhone,
	})
}

// handleBOInvoiceWhatsAppAttempt returns the latest durable WhatsApp send
// attempt for the invoice (invoice_send_whatsapp_v1) so a fresh browser — or
// a post-logout session whose localStorage key is gone — can resume the same
// idempotent intent instead of starting a blind new one.
func (s *Server) handleBOInvoiceWhatsAppAttempt(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}
	// Same object-level ACL as the send endpoint.
	inv, err := s.loadFullInvoice(r.Context(), a.ActiveRestaurantID, invoiceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching invoice: "+err.Error())
		return
	}
	if inv == nil {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}
	attempt, err := s.loadLatestInvoiceWaSend(r.Context(), a.ActiveRestaurantID, invoiceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading send attempt: "+err.Error())
		return
	}
	if attempt == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "attempt": nil})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"attempt": map[string]any{
			"idempotency_key": attempt.IdempotencyKey,
			"text_status":     attempt.TextStatus,
			"pdf_status":      attempt.PdfStatus,
			"phone":           attempt.Phone,
			"pdf_url":         attempt.PdfURL,
			"stage_error":     attempt.StageError,
		},
	})
}

// invoiceWaSend is the durable idempotency record of one operator send intent
// (invoice_send_whatsapp_v1, migration 188). Stage statuses: pending,
// inflight (claimed, provider call started or crashed mid-request), sent,
// error (demonstrably pre-send failure; automatically retryable), unknown
// (provider call failed; delivery uncertain; explicit operator retry only).
type invoiceWaSend struct {
	ID             int64
	RestaurantID   int
	InvoiceID      int
	IdempotencyKey string
	Phone          string
	TextStatus     string
	PdfStatus      string
	PdfURL         string
	StageError     string
}

func (s *Server) loadInvoiceWaSend(ctx context.Context, restaurantID int, key string) (*invoiceWaSend, error) {
	return s.scanInvoiceWaSend(s.db.QueryRowContext(ctx, `SELECT id, restaurant_id, invoice_id, idempotency_key, phone, text_status, pdf_status, IFNULL(pdf_url,''), stage_error FROM invoice_wa_sends WHERE restaurant_id = ? AND idempotency_key = ?`, restaurantID, key))
}

func (s *Server) loadLatestInvoiceWaSend(ctx context.Context, restaurantID, invoiceID int) (*invoiceWaSend, error) {
	return s.scanInvoiceWaSend(s.db.QueryRowContext(ctx, `SELECT id, restaurant_id, invoice_id, idempotency_key, phone, text_status, pdf_status, IFNULL(pdf_url,''), stage_error FROM invoice_wa_sends WHERE restaurant_id = ? AND invoice_id = ? ORDER BY id DESC LIMIT 1`, restaurantID, invoiceID))
}

func (s *Server) scanInvoiceWaSend(row *sql.Row) (*invoiceWaSend, error) {
	var out invoiceWaSend
	err := row.Scan(&out.ID, &out.RestaurantID, &out.InvoiceID, &out.IdempotencyKey, &out.Phone, &out.TextStatus, &out.PdfStatus, &out.PdfURL, &out.StageError)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Server) insertInvoiceWaSend(ctx context.Context, row *invoiceWaSend) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO invoice_wa_sends (restaurant_id, invoice_id, idempotency_key, phone) VALUES (?,?,?,?)`, row.RestaurantID, row.InvoiceID, row.IdempotencyKey, row.Phone)
	if err != nil {
		return err
	}
	row.ID, _ = res.LastInsertId()
	return nil
}

// claimInvoiceWaSendStage atomically moves one stage into inflight. A normal
// claim only succeeds from pending/error; an explicit operator retry
// (allowUncertain) may additionally reclaim an unknown stage — safe because
// the request that wrote unknown demonstrably ended (it persisted the status
// after the provider call returned). inflight is NEVER reclaimed under the
// same key: its writer may still be live, and no request-duration bound is
// assumed. Moving past inflight means the operator starts a new intent
// (fresh key) with an explicit duplicate warning — not a safe "recovery":
// if the original send landed, the customer gets the message twice. The old
// row is preserved as an audit record. Exactly one concurrent claimant
// wins: the loser's UPDATE affects 0 rows.
func (s *Server) claimInvoiceWaSendStage(ctx context.Context, id int64, stage string, allowUncertain bool) (bool, error) {
	col := "text_status"
	if stage == "pdf" {
		col = "pdf_status"
	}
	// col is an internal constant, never request data.
	q := `UPDATE invoice_wa_sends SET ` + col + ` = 'inflight', stage_error = '' WHERE id = ? AND (` + col + ` IN ('pending','error')`
	if allowUncertain {
		q += ` OR ` + col + ` = 'unknown'`
	}
	q += `)`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// setInvoiceWaSendStage persists one stage transition for an attempt whose
// claim the caller owns (or a terminal state). Stage errors are
// informational; the transition itself is best effort — a failed persist of
// a post-send transition leaves the stage inflight, which the resume path
// then retains instead of re-sending.
func (s *Server) setInvoiceWaSendStage(ctx context.Context, id int64, stage, status, pdfURL, stageError string) {
	if len(stageError) > 500 {
		stageError = stageError[:500]
	}
	var err error
	if stage == "text" {
		_, err = s.db.ExecContext(ctx, `UPDATE invoice_wa_sends SET text_status = ?, stage_error = ? WHERE id = ?`, status, stageError, id)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE invoice_wa_sends SET pdf_status = ?, pdf_url = NULLIF(?, ''), stage_error = ? WHERE id = ?`, status, pdfURL, stageError, id)
	}
	if err != nil {
		log.Printf("[invoice-wa] attempt %d stage %s->%s persist failed: %v", id, stage, status, err)
	}
}

// handleBOInvoicePdf renders and streams the invoice PDF for preview/download.
func (s *Server) handleBOInvoicePdf(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}
	inv, err := s.loadFullInvoice(r.Context(), a.ActiveRestaurantID, invoiceID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching invoice: "+err.Error())
		return
	}
	if inv == nil {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}
	info, _ := s.loadRestaurantInfo(r.Context(), a.ActiveRestaurantID)
	branding, _ := s.loadRestaurantBranding(r.Context(), a.ActiveRestaurantID)
	branding.LogoURL = s.resolveInvoiceLogoURL(r.Context(), a.ActiveRestaurantID, branding)
	pdfBytes, err := s.generateInvoicePDF(r.Context(), inv, info, branding)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error generating PDF: "+err.Error())
		return
	}
	label := ""
	if inv.InvoiceNumber != nil && strings.TrimSpace(*inv.InvoiceNumber) != "" {
		label = *inv.InvoiceNumber
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+invoicePDFFilename(label)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// Get restaurant by ID
func (s *Server) getRestaurant(ctx context.Context, restaurantID int) (*Restaurant, error) {
	var r Restaurant
	err := s.db.QueryRowContext(ctx, `
		SELECT id, slug, name, avatar FROM restaurants WHERE id = ?
	`, restaurantID).Scan(&r.ID, &r.Slug, &r.Name, &r.Avatar)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Upload invoice image to BunnyCDN
func (s *Server) handleBOInvoiceUploadImage(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	invoiceID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid invoice ID")
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
		httpx.WriteError(w, http.StatusBadRequest, "Invalid form data")
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "No image file provided")
		return
	}
	defer file.Close()

	// Read file content
	buffer := make([]byte, 10<<20) // 10MB
	n, err := file.Read(buffer)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Error reading file")
		return
	}
	buffer = buffer[:n]

	// Upload to BunnyCDN
	objectPath := fmt.Sprintf("%d/facturas/images/image_%d.webp", a.ActiveRestaurantID, invoiceID)
	if err := s.bunnyPut(r.Context(), a.ActiveRestaurantID, objectPath, buffer, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error uploading image: "+err.Error())
		return
	}

	// Get the public URL
	imageURL := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath)

	// Update invoice with image URL
	_, err = s.db.ExecContext(r.Context(), `
		UPDATE invoices SET account_image_url = ? WHERE id = ? AND restaurant_id = ?
	`, imageURL, invoiceID, a.ActiveRestaurantID)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error updating invoice: "+err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"url":     imageURL,
	})
}

// Helper function to check if date is valid ISO format
func isValidInvoiceISODate(dateStr string) bool {
	_, err := time.Parse("2006-01-02", dateStr)
	return err == nil
}
