package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Invoice types
type Invoice struct {
	ID                      int             `json:"id"`
	RestaurantID            int             `json:"restaurant_id"`
	InvoiceNumber           *string         `json:"invoice_number"`
	CustomerName            string          `json:"customer_name"`
	CustomerSurname         *string         `json:"customer_surname"`
	CustomerEmail           string          `json:"customer_email"`
	CustomerDniCif          *string         `json:"customer_dni_cif"`
	CustomerPhone           *string         `json:"customer_phone"`
	CustomerAddressStreet   *string         `json:"customer_address_street"`
	CustomerAddressNumber   *string         `json:"customer_address_number"`
	CustomerAddressPostalCode *string       `json:"customer_address_postal_code"`
	CustomerAddressCity     *string         `json:"customer_address_city"`
	CustomerAddressProvince *string         `json:"customer_address_province"`
	CustomerAddressCountry  *string         `json:"customer_address_country"`
	Amount                 float64         `json:"amount"`
	IvaRate                *float64        `json:"iva_rate"`
	IvaAmount              *float64        `json:"iva_amount"`
	Total                  *float64        `json:"total"`
	PaymentMethod          *string         `json:"payment_method"`
	AccountImageURL        *string         `json:"account_image_url"`
	InvoiceDate            string          `json:"invoice_date"`
	PaymentDate            *string         `json:"payment_date"`
	Status                 string          `json:"status"`
	IsReservation          bool            `json:"is_reservation"`
	ReservationID          *int            `json:"reservation_id"`
	ReservationDate        *string         `json:"reservation_date"`
	ReservationCustomerName *string         `json:"reservation_customer_name"`
	ReservationPartySize   *int            `json:"reservation_party_size"`
	PdfURL                 *string         `json:"pdf_url"`
	CreatedAt              string          `json:"created_at"`
	UpdatedAt              string          `json:"updated_at"`
}

type InvoiceInput struct {
	CustomerName            string  `json:"customer_name"`
	CustomerSurname         *string `json:"customer_surname"`
	CustomerEmail           string  `json:"customer_email"`
	CustomerDniCif          *string `json:"customer_dni_cif"`
	CustomerPhone           *string `json:"customer_phone"`
	CustomerAddressStreet   *string `json:"customer_address_street"`
	CustomerAddressNumber   *string `json:"customer_address_number"`
	CustomerAddressPostalCode *string `json:"customer_address_postal_code"`
	CustomerAddressCity     *string `json:"customer_address_city"`
	CustomerAddressProvince *string `json:"customer_address_province"`
	CustomerAddressCountry  *string `json:"customer_address_country"`
	Amount                 float64 `json:"amount"`
	IvaRate                *float64 `json:"iva_rate"`
	IvaAmount              *float64 `json:"iva_amount"`
	Total                  *float64 `json:"total"`
	PaymentMethod          *string `json:"payment_method"`
	AccountImageURL        *string `json:"account_image_url"`
	InvoiceDate            string  `json:"invoice_date"`
	PaymentDate            *string `json:"payment_date"`
	Status                 string  `json:"status"`
	IsReservation          bool    `json:"is_reservation"`
	ReservationID          *int    `json:"reservation_id"`
	ReservationDate        *string `json:"reservation_date"`
	ReservationCustomerName *string `json:"reservation_customer_name"`
	ReservationPartySize   *int    `json:"reservation_party_size"`
}

type InvoiceListParams struct {
	Search        string `json:"search"`
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
	ID                int    `json:"id"`
	CustomerName      string `json:"customer_name"`
	ContactEmail      string `json:"contact_email"`
	ContactPhone      string `json:"contact_phone"`
	ReservationDate   string `json:"reservation_date"`
	ReservationTime   string `json:"reservation_time"`
	PartySize         int    `json:"party_size"`
}

type Restaurant struct {
	ID     int    `json:"id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
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

	// Build query
	query := `
		SELECT
			id, restaurant_id, invoice_number,
			customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, iva_rate, iva_amount, total, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date,
			reservation_customer_name, reservation_party_size, pdf_url,
			created_at, updated_at
		FROM invoices
		WHERE restaurant_id = ?
	`
	args := []interface{}{restaurantID}
	argIdx := 1

	// Add search filter (name or email)
	if params.Search != "" {
		argIdx++
		query += fmt.Sprintf(" AND (customer_name LIKE CONCAT('%%%s%%', ?) OR customer_email LIKE CONCAT('%%%s%%', ?))", "%", "%")
		args = append(args, params.Search, params.Search)
	}

	// Add status filter
	if params.Status != "" {
		argIdx++
		query += " AND status = ?"
		args = append(args, params.Status)
	}

	// Add date filter
	if params.DateType == "reservation_date" {
		if params.DateFrom != "" {
			argIdx++
			query += " AND reservation_date >= ?"
			args = append(args, params.DateFrom)
		}
		if params.DateTo != "" {
			argIdx++
			query += " AND reservation_date <= ?"
			args = append(args, params.DateTo)
		}
	} else {
		if params.DateFrom != "" {
			argIdx++
			query += " AND invoice_date >= ?"
			args = append(args, params.DateFrom)
		}
		if params.DateTo != "" {
			argIdx++
			query += " AND invoice_date <= ?"
			args = append(args, params.DateTo)
		}
	}

	// Add is_reservation filter
	if params.IsReservation != nil {
		argIdx++
		if *params.IsReservation {
			query += " AND is_reservation = 1"
		} else {
			query += " AND is_reservation = 0"
		}
	}

	// Add sorting
	switch params.Sort {
	case "amount_asc":
		query += " ORDER BY amount ASC"
	case "amount_desc":
		query += " ORDER BY amount DESC"
	case "date_asc":
		query += " ORDER BY invoice_date ASC"
	default: // date_desc (default)
		query += " ORDER BY invoice_date DESC"
	}

	// Add pagination
	offset := (params.Page - 1) * params.Limit
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", params.Limit, offset)

	// Execute query
	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error querying invoices: "+err.Error())
		return
	}
	defer rows.Close()

	var invoices []Invoice
	for rows.Next() {
		var inv Invoice
		err := rows.Scan(
			&inv.ID, &inv.RestaurantID,
			&inv.CustomerName, &inv.CustomerSurname, &inv.CustomerEmail, &inv.CustomerDniCif, &inv.CustomerPhone,
			&inv.CustomerAddressStreet, &inv.CustomerAddressNumber, &inv.CustomerAddressPostalCode,
			&inv.CustomerAddressCity, &inv.CustomerAddressProvince, &inv.CustomerAddressCountry,
			&inv.Amount, &inv.PaymentMethod, &inv.AccountImageURL, &inv.InvoiceDate, &inv.PaymentDate,
			&inv.Status, &inv.IsReservation, &inv.ReservationID, &inv.ReservationDate,
			&inv.ReservationCustomerName, &inv.ReservationPartySize, &inv.PdfURL,
			&inv.CreatedAt, &inv.UpdatedAt,
		)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error scanning invoice: "+err.Error())
			return
		}
		invoices = append(invoices, inv)
	}

	// Get total count for pagination
	countQuery := strings.Replace(query, fmt.Sprintf(" LIMIT %d OFFSET %d", params.Limit, offset), "", 1)
	countQuery = "SELECT COUNT(*) FROM (" + countQuery + ") as t"

	var total int
	if err := s.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		total = 0
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"invoices": invoices,
		"total": total,
		"page": params.Page,
		"limit": params.Limit,
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
	err = s.db.QueryRowContext(r.Context(), `
		SELECT
			id, restaurant_id, invoice_number,
			customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, iva_rate, iva_amount, total, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date,
			reservation_customer_name, reservation_party_size, pdf_url,
			created_at, updated_at
		FROM invoices
		WHERE id = ? AND restaurant_id = ?
	`, invoiceID, a.ActiveRestaurantID).Scan(
		&inv.ID, &inv.RestaurantID, &inv.InvoiceNumber,
		&inv.CustomerName, &inv.CustomerSurname, &inv.CustomerEmail, &inv.CustomerDniCif, &inv.CustomerPhone,
		&inv.CustomerAddressStreet, &inv.CustomerAddressNumber, &inv.CustomerAddressPostalCode,
		&inv.CustomerAddressCity, &inv.CustomerAddressProvince, &inv.CustomerAddressCountry,
		&inv.Amount, &inv.IvaRate, &inv.IvaAmount, &inv.Total, &inv.PaymentMethod, &inv.AccountImageURL, &inv.InvoiceDate, &inv.PaymentDate,
		&inv.Status, &inv.IsReservation, &inv.ReservationID, &inv.ReservationDate,
		&inv.ReservationCustomerName, &inv.ReservationPartySize, &inv.PdfURL,
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

	// Validate status
	switch input.Status {
	case "borrador", "solicitada", "pendiente", "enviada":
	default:
		input.Status = "borrador"
	}

	result, err := s.db.ExecContext(r.Context(), `
		INSERT INTO invoices (
			restaurant_id, customer_name, customer_surname, customer_email, customer_dni_cif, customer_phone,
			customer_address_street, customer_address_number, customer_address_postal_code,
			customer_address_city, customer_address_province, customer_address_country,
			amount, iva_rate, iva_amount, total, payment_method, account_image_url, invoice_date, payment_date,
			status, is_reservation, reservation_id, reservation_date,
			reservation_customer_name, reservation_party_size
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.ActiveRestaurantID,
		input.CustomerName, input.CustomerSurname, input.CustomerEmail, input.CustomerDniCif, input.CustomerPhone,
		input.CustomerAddressStreet, input.CustomerAddressNumber, input.CustomerAddressPostalCode,
		input.CustomerAddressCity, input.CustomerAddressProvince, input.CustomerAddressCountry,
		input.Amount, input.IvaRate, input.IvaAmount, input.Total, input.PaymentMethod, input.AccountImageURL, input.InvoiceDate, input.PaymentDate,
		input.Status, input.IsReservation, input.ReservationID, input.ReservationDate,
		input.ReservationCustomerName, input.ReservationPartySize)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creating invoice: "+err.Error())
		return
	}

	id, _ := result.LastInsertId()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"id": id,
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

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE invoices SET
			customer_name = ?, customer_surname = ?, customer_email = ?, customer_dni_cif = ?, customer_phone = ?,
			customer_address_street = ?, customer_address_number = ?, customer_address_postal_code = ?,
			customer_address_city = ?, customer_address_province = ?, customer_address_country = ?,
			amount = ?, iva_rate = ?, iva_amount = ?, total = ?, payment_method = ?, account_image_url = ?, invoice_date = ?, payment_date = ?,
			status = ?, is_reservation = ?, reservation_id = ?, reservation_date = ?,
			reservation_customer_name = ?, reservation_party_size = ?
		WHERE id = ? AND restaurant_id = ?
	`, input.CustomerName, input.CustomerSurname, input.CustomerEmail, input.CustomerDniCif, input.CustomerPhone,
		input.CustomerAddressStreet, input.CustomerAddressNumber, input.CustomerAddressPostalCode,
		input.CustomerAddressCity, input.CustomerAddressProvince, input.CustomerAddressCountry,
		input.Amount, input.IvaRate, input.IvaAmount, input.Total, input.PaymentMethod, input.AccountImageURL, input.InvoiceDate, input.PaymentDate,
		input.Status, input.IsReservation, input.ReservationID, input.ReservationDate,
		input.ReservationCustomerName, input.ReservationPartySize,
		invoiceID, a.ActiveRestaurantID)

	if err != nil {
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

	var reservations []ReservationSearchResult
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
		"success": true,
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

	// Get invoice with restaurant details
	var inv Invoice
	var restaurant Restaurant
	err = s.db.QueryRowContext(r.Context(), `
		SELECT
			i.id, i.restaurant_id, i.invoice_number,
			i.customer_name, i.customer_surname, i.customer_email, i.customer_dni_cif, i.customer_phone,
			i.customer_address_street, i.customer_address_number, i.customer_address_postal_code,
			i.customer_address_city, i.customer_address_province, i.customer_address_country,
			i.amount, i.iva_rate, i.iva_amount, i.total, i.payment_method, i.account_image_url, i.invoice_date, i.payment_date,
			i.status, i.is_reservation, i.reservation_id, i.reservation_date,
			i.reservation_customer_name, i.reservation_party_size, i.pdf_url,
			i.created_at, i.updated_at,
			r.id, r.slug, r.name, r.avatar
		FROM invoices i
		JOIN restaurants r ON i.restaurant_id = r.id
		WHERE i.id = ? AND i.restaurant_id = ?
	`, invoiceID, a.ActiveRestaurantID).Scan(
		&inv.ID, &inv.RestaurantID, &inv.InvoiceNumber,
		&inv.CustomerName, &inv.CustomerSurname, &inv.CustomerEmail, &inv.CustomerDniCif, &inv.CustomerPhone,
		&inv.CustomerAddressStreet, &inv.CustomerAddressNumber, &inv.CustomerAddressPostalCode,
		&inv.CustomerAddressCity, &inv.CustomerAddressProvince, &inv.CustomerAddressCountry,
		&inv.Amount, &inv.IvaRate, &inv.IvaAmount, &inv.Total, &inv.PaymentMethod, &inv.AccountImageURL, &inv.InvoiceDate, &inv.PaymentDate,
		&inv.Status, &inv.IsReservation, &inv.ReservationID, &inv.ReservationDate,
		&inv.ReservationCustomerName, &inv.ReservationPartySize, &inv.PdfURL,
		&inv.CreatedAt, &inv.UpdatedAt,
		&restaurant.ID, &restaurant.Slug, &restaurant.Name, &restaurant.Avatar,
	)

	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "Invoice not found")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error fetching invoice: "+err.Error())
		return
	}

	// Generate PDF and upload to BunnyCDN
	pdfURL, err := s.UpdateInvoicePDF(r.Context(), invoiceID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error generating PDF: "+err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Invoice sent successfully",
		"pdf_url": pdfURL,
	})
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
	if err := s.bunnyPut(r.Context(), objectPath, buffer, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error uploading image: "+err.Error())
		return
	}

	// Get the public URL
	imageURL := s.bunnyPullURL(objectPath)

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
		"url": imageURL,
	})
}

// Helper function to check if date is valid ISO format
func isValidInvoiceISODate(dateStr string) bool {
	_, err := time.Parse("2006-01-02", dateStr)
	return err == nil
}
