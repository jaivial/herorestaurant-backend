package api

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/invoice_*.html
var invoiceTemplateFS embed.FS

var invoiceTemplates = template.Must(template.New("invoices").ParseFS(invoiceTemplateFS, "templates/invoice_*.html"))

// invoiceAccentByTemplate defines the accent color for each design.
var invoiceAccentByTemplate = map[string]string{
	"basic":   "#374151",
	"modern":  "#38b28f",
	"classic": "#7c2d12",
}

type invoiceRenderIssuer struct {
	Name    string
	CIF     string
	Address string
	Phone   string
	Email   string
	LogoURL string
}

type invoiceRenderParty struct {
	Name    string
	DniCif  string
	Email   string
	Phone   string
	Address string
}

type invoiceRenderLine struct {
	Description string
	Quantity    string
	UnitPrice   string
	IVARate     string
	Total       string
}

type invoiceRenderData struct {
	Template       string
	Accent         string
	Issuer         invoiceRenderIssuer
	Customer       invoiceRenderParty
	Number         string
	InvoiceDate    string
	DueDate        string
	PaymentDate    string
	PaymentMethod  string
	StatusLabel    string
	Lines          []invoiceRenderLine
	HasLines       bool
	Subtotal       string
	IVALabel       string
	IVAAmount      string
	HasIVA         bool
	DiscountLabel  string
	DiscountAmount string
	HasDiscount    bool
	DepositLabel   string
	DepositAmount  string
	HasDeposit     bool
	Total          string
	Notes          string
	Year           string
}

func currencySymbol(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "EUR":
		return "€"
	case "USD":
		return "$"
	case "GBP":
		return "£"
	default:
		return strings.ToUpper(strings.TrimSpace(code))
	}
}

// formatMoney renders a value in Spanish style: 1.234,56 €
func formatMoney(v float64, currency string) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := strconv.FormatFloat(round2(v), 'f', 2, 64)
	intPart, decPart := s, "00"
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, decPart = s[:dot], s[dot+1:]
	}
	// group thousands with '.'
	var b strings.Builder
	n := len(intPart)
	for i, c := range intPart {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	sym := currencySymbol(currency)
	sign := ""
	if neg {
		sign = "-"
	}
	return fmt.Sprintf("%s%s,%s %s", sign, b.String(), decPart, sym)
}

func formatInvoiceDate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Accept "2006-01-02" and "2006-01-02T15:04:05Z".
	layouts := []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			return t.Format("02/01/2006")
		}
	}
	if len(v) >= 10 {
		if t, err := time.Parse("2006-01-02", v[:10]); err == nil {
			return t.Format("02/01/2006")
		}
	}
	return v
}

func paymentMethodLabel(m *string) string {
	if m == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(*m)) {
	case "efectivo":
		return "Efectivo"
	case "tarjeta":
		return "Tarjeta"
	case "transferencia":
		return "Transferencia bancaria"
	case "bizum":
		return "Bizum"
	case "cheque":
		return "Cheque"
	default:
		return strings.TrimSpace(*m)
	}
}

func statusLabel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "borrador":
		return "Borrador"
	case "solicitada":
		return "Solicitada"
	case "pendiente":
		return "Pendiente"
	case "enviada":
		return "Enviada"
	default:
		return s
	}
}

// invoicePDFFilename builds the attachment/download filename. It never includes
// the internal bill id: uses the invoice number when present, else "factura.pdf".
func invoicePDFFilename(number string) string {
	n := sanitizeFilename(number)
	if n == "" || n == "factura" {
		return "factura.pdf"
	}
	return "factura-" + n + ".pdf"
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "factura"
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, sep)
}

// buildInvoiceRenderData maps persisted invoice + restaurant data into the
// template view model.
func buildInvoiceRenderData(inv *Invoice, info boRestaurantInfo, branding restaurantBrandingCfg) invoiceRenderData {
	tpl := normalizePdfTemplate(&inv.PdfTemplate)
	accent := invoiceAccentByTemplate[tpl]
	if accent == "" {
		accent = invoiceAccentByTemplate["basic"]
	}

	issuerAddress := info.DireccionFacturacion
	if issuerAddress == "" {
		issuerAddress = info.Direccion
	}
	issuerName := branding.BrandName
	if issuerName == "" {
		issuerName = "Restaurante"
	}

	customerAddress := joinNonEmpty(", ",
		joinNonEmpty(" ", deref(inv.CustomerAddressStreet), deref(inv.CustomerAddressNumber)),
		joinNonEmpty(" ", deref(inv.CustomerAddressPostalCode), deref(inv.CustomerAddressCity)),
		deref(inv.CustomerAddressProvince),
		deref(inv.CustomerAddressCountry),
	)
	customerName := inv.CustomerName
	if s := deref(inv.CustomerSurname); s != "" {
		customerName += " " + s
	}

	// Only show an explicit invoice number; never expose the internal bill id.
	number := ""
	if inv.InvoiceNumber != nil && strings.TrimSpace(*inv.InvoiceNumber) != "" {
		number = strings.TrimSpace(*inv.InvoiceNumber)
	}

	lines := make([]invoiceRenderLine, 0, len(inv.LineItems))
	for _, li := range inv.LineItems {
		lines = append(lines, invoiceRenderLine{
			Description: li.Description,
			Quantity:    strconv.FormatFloat(li.Quantity, 'f', -1, 64),
			UnitPrice:   formatMoney(li.UnitPrice, inv.Currency),
			IVARate:     strconv.FormatFloat(li.IVARate, 'f', -1, 64) + "%",
			Total:       formatMoney(li.Total, inv.Currency),
		})
	}

	subtotal := inv.Amount
	if inv.Subtotal != nil {
		subtotal = *inv.Subtotal
	}
	total := inv.Amount
	if inv.Total != nil {
		total = *inv.Total
	}

	data := invoiceRenderData{
		Template: tpl,
		Accent:   accent,
		Issuer: invoiceRenderIssuer{
			Name:    issuerName,
			CIF:     info.CIF,
			Address: issuerAddress,
			Phone:   info.Telefono,
			Email:   info.Email,
			LogoURL: branding.LogoURL,
		},
		Customer: invoiceRenderParty{
			Name:    customerName,
			DniCif:  deref(inv.CustomerDniCif),
			Email:   inv.CustomerEmail,
			Phone:   deref(inv.CustomerPhone),
			Address: customerAddress,
		},
		Number:        number,
		InvoiceDate:   formatInvoiceDate(inv.InvoiceDate),
		DueDate:       formatInvoiceDate(deref(inv.DueDate)),
		PaymentDate:   formatInvoiceDate(deref(inv.PaymentDate)),
		PaymentMethod: paymentMethodLabel(inv.PaymentMethod),
		StatusLabel:   statusLabel(inv.Status),
		Lines:         lines,
		HasLines:      len(lines) > 0,
		Subtotal:      formatMoney(subtotal, inv.Currency),
		Total:         formatMoney(total, inv.Currency),
		Notes:         deref(inv.InternalNotes),
		Year:          time.Now().Format("2006"),
	}

	if inv.IVAAmount != nil && *inv.IVAAmount != 0 {
		data.HasIVA = true
		rate := ""
		if inv.IVARate != nil {
			rate = " (" + strconv.FormatFloat(*inv.IVARate, 'f', -1, 64) + "%)"
		}
		data.IVALabel = "IVA" + rate
		data.IVAAmount = formatMoney(*inv.IVAAmount, inv.Currency)
	}
	if inv.DiscountAmount != nil && *inv.DiscountAmount != 0 {
		data.HasDiscount = true
		data.DiscountAmount = "-" + formatMoney(*inv.DiscountAmount, inv.Currency)
		data.DiscountLabel = "Descuento"
		if r := deref(inv.DiscountReason); r != "" {
			data.DiscountLabel = "Descuento (" + r + ")"
		}
	}
	if inv.DepositAmount != nil && *inv.DepositAmount != 0 {
		data.HasDeposit = true
		data.DepositAmount = formatMoney(*inv.DepositAmount, inv.Currency)
		data.DepositLabel = "Anticipo"
		if inv.DepositType != nil && strings.EqualFold(*inv.DepositType, "deposit") {
			data.DepositLabel = "Depósito"
		}
	}

	return data
}

// renderInvoiceDocumentHTML renders the full standalone invoice HTML document
// for the selected template (used for the PDF and preview).
func renderInvoiceDocumentHTML(inv *Invoice, info boRestaurantInfo, branding restaurantBrandingCfg) (string, invoiceRenderData, error) {
	data := buildInvoiceRenderData(inv, info, branding)
	name := "invoice_" + data.Template + ".html"
	var buf bytes.Buffer
	if err := invoiceTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", data, err
	}
	return buf.String(), data, nil
}

// renderInvoiceEmailFragment renders the inline-styled invoice summary embedded
// in the email body.
func renderInvoiceEmailFragment(data invoiceRenderData) (string, error) {
	var buf bytes.Buffer
	if err := invoiceTemplates.ExecuteTemplate(&buf, "invoice_email.html", data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
