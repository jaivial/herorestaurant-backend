package api

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// =============================================================================
// Payment receipt of a Stripe-paid prereserva adelanto.
//
// A one-page PDF (restaurant, customer, prereserva, paid lines, total, payment
// reference) uploaded to the restaurant's BunnyCDN; the public URL goes in the
// success page, the WhatsApp document and the email attachment.
// Coordination id: stripe_prereserva_adelanto_v1
// =============================================================================

const bookingReceiptKey = "__payment_receipt"

type bookingReceipt struct {
	Filename string
	URL      string
	PDF      []byte
}

func (r *bookingReceipt) emailAttachment() emailAttachment {
	return emailAttachment{Filename: r.Filename, ContentType: "application/pdf", Data: r.PDF}
}

type receiptLine struct {
	Label  string
	Count  int
	Amount float64 // total for the line
}

type receiptInput struct {
	Reference    string // checkout public id
	PaymentRef   string // Stripe payment intent / demo id
	PaidAt       time.Time
	Demo         bool
	BrandName    string
	Address      string
	Phone        string
	Email        string
	Customer     string
	CustomerMail string
	CustomerTel  string
	Date         string
	Time         string
	PartySize    int
	DateTitle    string
	BookingID    int64
	Lines        []receiptLine
	Total        float64
	Currency     string
}

func buildReceiptPDF(in receiptInput) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(18, 18, 18)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(0, 10, tr(in.BrandName), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	for _, v := range []string{in.Address, strings.TrimSpace(in.Phone + "  " + in.Email)} {
		if strings.TrimSpace(v) != "" {
			pdf.CellFormat(0, 5, tr(v), "", 1, "L", false, 0, "")
		}
	}
	pdf.Ln(6)

	pdf.SetFont("Helvetica", "B", 14)
	title := "Comprobante de pago del adelanto"
	if in.Demo {
		title += " (DEMO - sin cargo real)"
	}
	pdf.CellFormat(0, 8, tr(title), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	kv := func(k, v string) {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(48, 6, tr(k), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.CellFormat(0, 6, tr(v), "", 1, "L", false, 0, "")
	}
	kv("Referencia", in.Reference)
	kv("Pago", in.PaymentRef)
	kv("Fecha del pago", in.PaidAt.Format("02/01/2006 15:04"))
	if in.BookingID > 0 {
		kv("Prereserva nº", fmt.Sprintf("%d", in.BookingID))
	}
	pdf.Ln(4)
	kv("Cliente", in.Customer)
	kv("Email", in.CustomerMail)
	kv("Teléfono", in.CustomerTel)
	pdf.Ln(4)
	if in.DateTitle != "" {
		kv("Fecha especial", in.DateTitle)
	}
	kv("Día y hora", strings.TrimSpace(in.Date+" "+in.Time))
	kv("Comensales", fmt.Sprintf("%d", in.PartySize))
	pdf.Ln(6)

	pdf.SetFillColor(240, 240, 240)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(110, 8, tr("Concepto"), "B", 0, "L", true, 0, "")
	pdf.CellFormat(24, 8, tr("Uds."), "B", 0, "R", true, 0, "")
	pdf.CellFormat(0, 8, tr("Importe"), "B", 1, "R", true, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	for _, l := range in.Lines {
		pdf.CellFormat(110, 7, tr("Adelanto · "+l.Label), "", 0, "L", false, 0, "")
		pdf.CellFormat(24, 7, fmt.Sprintf("%d", l.Count), "", 0, "R", false, 0, "")
		pdf.CellFormat(0, 7, tr(formatMoney(l.Amount, in.Currency)), "", 1, "R", false, 0, "")
	}
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(134, 9, tr("Total pagado"), "T", 0, "R", false, 0, "")
	pdf.CellFormat(0, 9, tr(formatMoney(in.Total, in.Currency)), "T", 1, "R", false, 0, "")
	pdf.Ln(8)
	pdf.SetFont("Helvetica", "", 8)
	pdf.MultiCell(0, 4, tr("Este documento acredita el pago del adelanto de la prereserva indicada. El importe se descontará de la cuenta final del día de la reserva."), "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// storeReceipt uploads the PDF to BunnyCDN and returns it with its public URL.
// Without Bunny the receipt still travels as the email attachment.
func (s *Server) storeReceipt(ctx context.Context, restaurantID int, publicID string, pdfBytes []byte) *bookingReceipt {
	name := "comprobante-prereserva-" + publicID + ".pdf"
	out := &bookingReceipt{Filename: name, PDF: pdfBytes}
	if !s.bunnyConfigured(ctx, restaurantID) {
		return out
	}
	objectPath := fmt.Sprintf("%d/receipts/prereservas/%s", restaurantID, name)
	if err := s.bunnyPut(ctx, restaurantID, objectPath, pdfBytes, "application/pdf"); err != nil {
		logStripeFlow("receipt_upload_failed", restaurantID, publicID, err.Error())
		return out
	}
	out.URL = s.bunnyPullURL(ctx, restaurantID, objectPath)
	return out
}
