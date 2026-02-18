package api

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// Color constants for PDF generation
const (
	primaryColor   = "#1a1a1a"
	secondaryColor = "#666666"
	accentColor    = "#c9a961" // Gold accent color
	lightGray      = "#f5f5f5"
)

// InvoicePDFData contains all the data needed to generate an invoice PDF
type InvoicePDFData struct {
	// Invoice details
	InvoiceNumber string
	InvoiceDate   string
	PaymentDate   string
	Status        string

	// Customer details
	CustomerName           string
	CustomerSurname        string
	CustomerEmail          string
	CustomerDniCif         string
	CustomerPhone          string
	CustomerAddressStreet  string
	CustomerAddressNumber  string
	CustomerAddressCity    string
	CustomerAddressProvince string
	CustomerAddressPostalCode string
	CustomerAddressCountry string

	// Amount details
	Amount         float64
	IvaRate        float64
	IvaAmount      float64
	Total          float64
	PaymentMethod  string

	// Reservation details (if applicable)
	IsReservation         bool
	ReservationDate       string
	ReservationCustomerName string
	ReservationPartySize  int

	// Restaurant details
	RestaurantName     string
	RestaurantLogoURL  string
	RestaurantCIF      string
	RestaurantAddress string
	RestaurantPhone   string
	RestaurantEmail   string
}

// GenerateInvoicePDF generates a PDF invoice and returns the PDF bytes
func GenerateInvoicePDF(ctx context.Context, data InvoicePDFData) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.AddPage()

	// Colors - defined but not used (keeping for future color customization)
	_ = primaryColor
	_ = secondaryColor
	_ = accentColor
	_ = lightGray

	// Header - Restaurant Logo and Name
	pdf.SetFont("Helvetica", "B", 24)
	pdf.SetTextColor(0, 0, 0)

	// Restaurant name
	restaurantName := data.RestaurantName
	if restaurantName == "" {
		restaurantName = "Restaurante"
	}
	pdf.Cell(0, 15, restaurantName)
	pdf.Ln(12)

	// Restaurant CIF
	if data.RestaurantCIF != "" {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(102, 102, 102)
		pdf.Cell(0, 5, "CIF: "+data.RestaurantCIF)
		pdf.Ln(6)
	}

	// Restaurant address
	if data.RestaurantAddress != "" {
		pdf.Cell(0, 5, data.RestaurantAddress)
		pdf.Ln(5)
	}

	// Restaurant contact
	contactInfo := []string{}
	if data.RestaurantPhone != "" {
		contactInfo = append(contactInfo, data.RestaurantPhone)
	}
	if data.RestaurantEmail != "" {
		contactInfo = append(contactInfo, data.RestaurantEmail)
	}
	if len(contactInfo) > 0 {
		pdf.Cell(0, 5, strings.Join(contactInfo, " | "))
		pdf.Ln(15)
	}

	// Invoice title and number on the right
	pdf.SetFont("Helvetica", "B", 28)
	pdf.SetTextColor(0, 0, 0)
	pdf.Cell(0, 10, "FACTURA")
	pdf.Ln(8)

	pdf.SetFont("Helvetica", "", 12)
	pdf.SetTextColor(102, 102, 102)
	pdf.Cell(0, 5, "N. Factura: "+data.InvoiceNumber)
	pdf.Ln(5)

	// Format and display dates
	invoiceDate := formatDate(data.InvoiceDate)
	pdf.Cell(0, 5, "Fecha: "+invoiceDate)
	pdf.Ln(5)

	if data.PaymentDate != "" {
		paymentDate := formatDate(data.PaymentDate)
		pdf.Cell(0, 5, "Fecha de pago: "+paymentDate)
		pdf.Ln(5)
	}

	// Horizontal line
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(20, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(10)

	// Customer details section
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(0, 0, 0)
	pdf.Cell(0, 7, "DATOS DEL CLIENTE")
	pdf.Ln(8)

	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(51, 51, 51)

	// Customer name
	customerName := data.CustomerName
	if data.CustomerSurname != "" {
		customerName += " " + data.CustomerSurname
	}
	pdf.Cell(0, 5, customerName)
	pdf.Ln(5)

	// Customer CIF/DNI
	if data.CustomerDniCif != "" {
		pdf.Cell(0, 5, "CIF/DNI: "+data.CustomerDniCif)
		pdf.Ln(5)
	}

	// Customer address
	addressParts := []string{}
	if data.CustomerAddressStreet != "" {
		street := data.CustomerAddressStreet
		if data.CustomerAddressNumber != "" {
			street += ", " + data.CustomerAddressNumber
		}
		addressParts = append(addressParts, street)
	}
	if data.CustomerAddressPostalCode != "" || data.CustomerAddressCity != "" {
		cityLine := ""
		if data.CustomerAddressPostalCode != "" {
			cityLine = data.CustomerAddressPostalCode
		}
		if data.CustomerAddressCity != "" {
			if cityLine != "" {
				cityLine += " "
			}
			cityLine += data.CustomerAddressCity
		}
		if data.CustomerAddressProvince != "" {
			cityLine += " (" + data.CustomerAddressProvince + ")"
		}
		addressParts = append(addressParts, cityLine)
	}
	if data.CustomerAddressCountry != "" {
		addressParts = append(addressParts, data.CustomerAddressCountry)
	}

	if len(addressParts) > 0 {
		pdf.Cell(0, 5, strings.Join(addressParts, ", "))
		pdf.Ln(5)
	}

	// Customer contact
	contactParts := []string{}
	if data.CustomerPhone != "" {
		contactParts = append(contactParts, "Tel: "+data.CustomerPhone)
	}
	if data.CustomerEmail != "" {
		contactParts = append(contactParts, "Email: "+data.CustomerEmail)
	}
	if len(contactParts) > 0 {
		pdf.Cell(0, 5, strings.Join(contactParts, " | "))
		pdf.Ln(5)
	}

	pdf.Ln(10)

	// Reservation details (if applicable)
	if data.IsReservation {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(0, 0, 0)
		pdf.Cell(0, 6, "DATOS DE LA RESERVA")
		pdf.Ln(6)

		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(51, 51, 51)

		if data.ReservationDate != "" {
			pdf.Cell(0, 5, "Fecha de reserva: "+formatDate(data.ReservationDate))
			pdf.Ln(5)
		}
		if data.ReservationCustomerName != "" {
			pdf.Cell(0, 5, "Cliente de reserva: "+data.ReservationCustomerName)
			pdf.Ln(5)
		}
		if data.ReservationPartySize > 0 {
			pdf.Cell(0, 5, fmt.Sprintf("Numero de comensales: %d", data.ReservationPartySize))
			pdf.Ln(5)
		}

		pdf.Ln(5)
	}

	// Invoice items table
	pdf.Ln(5)

	// Table header
	pdf.SetFillColor(245, 245, 245)
	pdf.SetDrawColor(200, 200, 200)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(0, 0, 0)

	// Header row
	pdf.CellFormat(110, 8, "Descripcion", "1", 0, "L", true, 0, "")
	pdf.CellFormat(30, 8, "Base Imponible", "1", 0, "R", true, 0, "")
	pdf.CellFormat(20, 8, "IVA %", "1", 0, "C", true, 0, "")
	pdf.CellFormat(30, 8, "Importe IVA", "1", 0, "R", true, 0, "")
	pdf.CellFormat(0, 8, "Total", "1", 0, "R", true, 0, "")
	pdf.Ln(-8)

	// Data row
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(51, 51, 51)

	// Description based on whether it's a reservation or not
	description := "Servicio de restaurante"
	if data.IsReservation && data.ReservationDate != "" {
		description = fmt.Sprintf("Reserva del %s - %s (%d comensales)",
			formatDate(data.ReservationDate),
			data.ReservationCustomerName,
			data.ReservationPartySize)
	}

	// Calculate amounts
	baseAmount := data.Amount
	ivaRate := data.IvaRate
	ivaAmount := data.IvaAmount
	totalAmount := data.Total

	// Ensure amounts are calculated correctly
	if ivaRate > 0 && baseAmount > 0 && ivaAmount == 0 {
		ivaAmount = baseAmount * ivaRate / 100
		totalAmount = baseAmount + ivaAmount
	}

	pdf.CellFormat(110, 12, description, "1", 0, "L", false, 0, "")
	pdf.CellFormat(30, 12, formatCurrency(baseAmount), "1", 0, "R", false, 0, "")
	pdf.CellFormat(20, 12, fmt.Sprintf("%.1f%%", ivaRate), "1", 0, "C", false, 0, "")
	pdf.CellFormat(30, 12, formatCurrency(ivaAmount), "1", 0, "R", false, 0, "")
	pdf.CellFormat(0, 12, formatCurrency(totalAmount), "1", 0, "R", false, 0, "")
	pdf.Ln(20)

	// Totals section
	pdf.SetDrawColor(200, 200, 200)
	pdf.Line(120, pdf.GetY(), 190, pdf.GetY())
	pdf.Ln(5)

	// Base amount
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(51, 51, 51)
	pdf.CellFormat(120, 6, "Base Imponible", "0", 0, "L", false, 0, "")
	pdf.CellFormat(0, 6, formatCurrency(baseAmount), "0", 0, "R", false, 0, "")
	pdf.Ln(6)

	// IVA
	pdf.CellFormat(120, 6, fmt.Sprintf("IVA (%.1f%%)", ivaRate), "0", 0, "L", false, 0, "")
	pdf.CellFormat(0, 6, formatCurrency(ivaAmount), "0", 0, "R", false, 0, "")
	pdf.Ln(6)

	// Total
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(120, 8, "TOTAL", "0", 0, "L", false, 0, "")
	pdf.CellFormat(0, 8, formatCurrency(totalAmount), "0", 0, "R", false, 0, "")
	pdf.Ln(15)

	// Payment method
	if data.PaymentMethod != "" {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(0, 0, 0)
		pdf.Cell(0, 6, "FORMA DE PAGO")
		pdf.Ln(6)

		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(51, 51, 51)
		paymentMethod := getPaymentMethodLabel(data.PaymentMethod)
		pdf.Cell(0, 5, paymentMethod)
		pdf.Ln(10)
	}

	// Footer
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(153, 153, 153)
	pdf.Cell(0, 5, "Esta factura ha sido generada automaticamente.")
	pdf.Ln(4)

	currentYear := time.Now().Year()
	pdf.Cell(0, 5, fmt.Sprintf("Villa Carmen - %d", currentYear))

	// Generate PDF bytes
	var buf bytes.Buffer
	err := pdf.Output(&buf)
	if err != nil {
		return nil, fmt.Errorf("failed to generate PDF: %w", err)
	}

	return buf.Bytes(), nil
}

// formatDate formats a date string from YYYY-MM-DD to DD/MM/YYYY
func formatDate(dateStr string) string {
	if dateStr == "" {
		return ""
	}

	// Try to parse the date
	parsed, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		// Try parsing as date only if it has time component
		if len(dateStr) > 10 {
			parsed, err = time.Parse("2006-01-02T15:04:05", dateStr)
			if err != nil {
				return dateStr
			}
		} else {
			return dateStr
		}
	}

	return parsed.Format("02/01/2006")
}

// formatCurrency formats a float64 as currency
func formatCurrency(amount float64) string {
	return fmt.Sprintf("%.2f EUR", amount)
}

// getPaymentMethodLabel returns a human-readable payment method label
func getPaymentMethodLabel(method string) string {
	switch strings.ToLower(method) {
	case "efectivo":
		return "Efectivo"
	case "tarjeta":
		return "Tarjeta de credito/debito"
	case "transferencia":
		return "Transferencia bancaria"
	case "bizum":
		return "Bizum"
	case "cheque":
		return "Cheque"
	default:
		return method
	}
}

// LoadInvoiceDataForPDF loads all the data needed to generate an invoice PDF
func (s *Server) LoadInvoiceDataForPDF(ctx context.Context, invoiceID int, restaurantID int) (*InvoicePDFData, error) {
	// Query invoice with restaurant details
	query := `
		SELECT
			i.id, i.restaurant_id, i.invoice_number,
			i.customer_name, i.customer_surname, i.customer_email, i.customer_dni_cif, i.customer_phone,
			i.customer_address_street, i.customer_address_number, i.customer_address_postal_code,
			i.customer_address_city, i.customer_address_province, i.customer_address_country,
			i.amount, i.iva_rate, i.iva_amount, i.total, i.payment_method, i.invoice_date, i.payment_date,
			i.status, i.is_reservation, i.reservation_date,
			i.reservation_customer_name, i.reservation_party_size,
			r.id, r.name, r.avatar, r.cif
		FROM invoices i
		JOIN restaurants r ON i.restaurant_id = r.id
		WHERE i.id = ? AND i.restaurant_id = ?
	`

	var data InvoicePDFData

	err := s.db.QueryRowContext(ctx, query, invoiceID, restaurantID).Scan(
		&data.InvoiceNumber,
		&data.RestaurantName,
		&data.CustomerName,
		&data.CustomerSurname,
		&data.CustomerEmail,
		&data.CustomerDniCif,
		&data.CustomerPhone,
		&data.CustomerAddressStreet,
		&data.CustomerAddressNumber,
		&data.CustomerAddressPostalCode,
		&data.CustomerAddressCity,
		&data.CustomerAddressProvince,
		&data.CustomerAddressCountry,
		&data.Amount,
		&data.IvaRate,
		&data.IvaAmount,
		&data.Total,
		&data.PaymentMethod,
		&data.InvoiceDate,
		&data.PaymentDate,
		&data.Status,
		&data.IsReservation,
		&data.ReservationDate,
		&data.ReservationCustomerName,
		&data.ReservationPartySize,
		&data.RestaurantName,
		&data.RestaurantLogoURL,
		&data.RestaurantCIF,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invoice not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load invoice data: %w", err)
	}

	// Load restaurant branding for contact info
	branding, err := s.loadRestaurantBranding(ctx, restaurantID)
	if err == nil && branding.EmailFromAddress != "" {
		data.RestaurantEmail = branding.EmailFromAddress
	}

	return &data, nil
}

// GenerateInvoicePDF is a standalone function to generate the PDF
func (s *Server) GenerateInvoicePDF(ctx context.Context, data InvoicePDFData) ([]byte, error) {
	return GenerateInvoicePDF(ctx, data)
}

// GenerateAndUploadInvoicePDF generates a PDF for an invoice, uploads it to BunnyCDN, and returns the URL
func (s *Server) GenerateAndUploadInvoicePDF(ctx context.Context, invoiceID int, restaurantID int) (string, error) {
	// Load invoice data
	data, err := s.LoadInvoiceDataForPDF(ctx, invoiceID, restaurantID)
	if err != nil {
		return "", fmt.Errorf("failed to load invoice data: %w", err)
	}

	// Generate invoice number if not exists
	if data.InvoiceNumber == "" {
		invoiceNumber, err := s.generateInvoiceNumber(ctx, restaurantID, data.InvoiceDate)
		if err != nil {
			return "", fmt.Errorf("failed to generate invoice number: %w", err)
		}
		data.InvoiceNumber = invoiceNumber
	}

	// Generate PDF
	pdfBytes, err := s.GenerateInvoicePDF(ctx, *data)
	if err != nil {
		return "", fmt.Errorf("failed to generate PDF: %w", err)
	}

	// Upload to BunnyCDN
	objectPath := fmt.Sprintf("%d/facturas/pdf/%s.pdf", restaurantID, data.InvoiceNumber)
	if err := s.bunnyPut(ctx, objectPath, pdfBytes, "application/pdf"); err != nil {
		return "", fmt.Errorf("failed to upload PDF: %w", err)
	}

	// Get public URL
	pdfURL := s.bunnyPullURL(objectPath)

	return pdfURL, nil
}

// generateInvoiceNumber generates a unique invoice number for the restaurant
func (s *Server) generateInvoiceNumber(ctx context.Context, restaurantID int, invoiceDate string) (string, error) {
	// Parse the date to extract year and month
	var year, month int
	if invoiceDate != "" {
		parsed, err := time.Parse("2006-01-02", invoiceDate)
		if err == nil {
			year = parsed.Year()
			month = int(parsed.Month())
		}
	}

	if year == 0 {
		year = time.Now().Year()
	}
	if month == 0 {
		month = int(time.Now().Month())
	}

	// Get the next invoice number for this restaurant and year
	var nextNum int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(CAST(SUBSTRING_INDEX(invoice_number, '/', -1) AS UNSIGNED)), 0) + 1
		FROM invoices
		WHERE restaurant_id = ?
		AND invoice_number IS NOT NULL
		AND invoice_number LIKE ?
	`, restaurantID, fmt.Sprintf("%%/%d", year)).Scan(&nextNum)

	if err != nil {
		return "", fmt.Errorf("failed to get next invoice number: %w", err)
	}

	// Format: F{restaurant_id}/{year}/{sequential_number}
	// Example: F1/2026/001
	invoiceNumber := fmt.Sprintf("F%d/%d/%03d", restaurantID, year, nextNum)

	return invoiceNumber, nil
}

// getInvoiceNumber retrieves the invoice number for an invoice
func (s *Server) getInvoiceNumber(ctx context.Context, invoiceID int, restaurantID int) (string, error) {
	var invoiceNumber sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT invoice_number FROM invoices WHERE id = ? AND restaurant_id = ?
	`, invoiceID, restaurantID).Scan(&invoiceNumber)

	if err != nil {
		return "", err
	}

	if !invoiceNumber.Valid {
		return "", nil
	}

	return invoiceNumber.String, nil
}

// updateInvoicePDFURL updates the PDF URL for an invoice
func (s *Server) updateInvoicePDFURL(ctx context.Context, invoiceID int, restaurantID int, pdfURL string) error {
	// Get current invoice number
	invoiceNumber, err := s.getInvoiceNumber(ctx, invoiceID, restaurantID)
	if err != nil {
		return fmt.Errorf("failed to get invoice number: %w", err)
	}

	// If invoice number doesn't exist, generate one
	if invoiceNumber == "" {
		// Get invoice date
		var invoiceDate string
		err := s.db.QueryRowContext(ctx, `
			SELECT invoice_date FROM invoices WHERE id = ? AND restaurant_id = ?
		`, invoiceID, restaurantID).Scan(&invoiceDate)

		if err != nil {
			return fmt.Errorf("failed to get invoice date: %w", err)
		}

		invoiceNumber, err = s.generateInvoiceNumber(ctx, restaurantID, invoiceDate)
		if err != nil {
			return fmt.Errorf("failed to generate invoice number: %w", err)
		}

		// Update invoice with the generated number
		_, err = s.db.ExecContext(ctx, `
			UPDATE invoices SET invoice_number = ? WHERE id = ? AND restaurant_id = ?
		`, invoiceNumber, invoiceID, restaurantID)

		if err != nil {
			return fmt.Errorf("failed to update invoice number: %w", err)
		}
	}

	// Update the PDF URL
	_, err = s.db.ExecContext(ctx, `
		UPDATE invoices SET pdf_url = ?, status = 'enviada' WHERE id = ? AND restaurant_id = ?
	`, pdfURL, invoiceID, restaurantID)

	if err != nil {
		return fmt.Errorf("failed to update invoice PDF URL: %w", err)
	}

	return nil
}

// UpdateInvoicePDF generates a new PDF for an existing invoice
func (s *Server) UpdateInvoicePDF(ctx context.Context, invoiceID int, restaurantID int) (string, error) {
	// Generate and upload PDF
	pdfURL, err := s.GenerateAndUploadInvoicePDF(ctx, invoiceID, restaurantID)
	if err != nil {
		return "", err
	}

	// Update the invoice with the PDF URL and generate invoice number if needed
	err = s.updateInvoicePDFURL(ctx, invoiceID, restaurantID, pdfURL)
	if err != nil {
		return "", err
	}

	return pdfURL, nil
}

// GetInvoicePDFURL retrieves the PDF URL for an invoice
func (s *Server) GetInvoicePDFURL(ctx context.Context, invoiceID int, restaurantID int) (string, error) {
	var pdfURL sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT pdf_url FROM invoices WHERE id = ? AND restaurant_id = ?
	`, invoiceID, restaurantID).Scan(&pdfURL)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	if !pdfURL.Valid {
		return "", nil
	}

	return pdfURL.String, nil
}

// ConvertPointerToFloat64 safely converts a pointer to float64
func ConvertPointerToFloat64(ptr *float64) float64 {
	if ptr == nil {
		return 0
	}
	return *ptr
}

// ConvertPointerToString safely converts a pointer to string
func ConvertPointerToString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

// IntToString converts an int to string
func IntToString(i int) string {
	return strconv.Itoa(i)
}
