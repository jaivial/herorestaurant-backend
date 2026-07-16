package api

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-pdf/fpdf"
)

// chromeExecPath returns the path to a Chrome/Chromium binary, or "" if none.
func chromeExecPath() string {
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// generateInvoicePDF renders the invoice document to PDF bytes. It prefers
// headless Chrome (pixel-identical to the HTML template); if Chrome is not
// available or fails, it falls back to a native Go PDF renderer so a PDF is
// always produced.
func (s *Server) generateInvoicePDF(ctx context.Context, inv *Invoice, info boRestaurantInfo, branding restaurantBrandingCfg) ([]byte, error) {
	htmlStr, data, err := renderInvoiceDocumentHTML(inv, info, branding)
	if err != nil {
		return nil, err
	}

	if chromeExecPath() != "" {
		if pdf, cerr := htmlToPDFChrome(ctx, htmlStr); cerr == nil && len(pdf) > 0 {
			return pdf, nil
		} else if cerr != nil {
			log.Printf("[invoice-pdf] chromedp failed, using native fallback: %v", cerr)
		}
	} else {
		log.Printf("[invoice-pdf] chrome not found, using native fallback")
	}

	return invoiceFallbackPDF(data)
}

// htmlToPDFChrome renders an HTML string to a PDF via headless Chrome.
func htmlToPDFChrome(ctx context.Context, html string) ([]byte, error) {
	execPath := chromeExecPath()
	if execPath == "" {
		return nil, errors.New("chrome not available")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.NoSandbox,
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()

	timeoutCtx, cancelTimeout := context.WithTimeout(taskCtx, 30*time.Second)
	defer cancelTimeout()

	dataURL := "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(html))

	// Wait until every <img> (e.g. the CDN logo) has finished loading, so the
	// logo is present in the printed PDF. Bounded so a slow/broken image can't
	// hang the render.
	const waitImagesJS = `new Promise((resolve)=>{
		const imgs = Array.from(document.images);
		let pending = imgs.filter(i=>!i.complete).length;
		if (pending === 0) return resolve(true);
		const done = () => { if (--pending <= 0) resolve(true); };
		imgs.forEach(i => { if (!i.complete) { i.addEventListener('load', done); i.addEventListener('error', done); } });
		setTimeout(() => resolve(true), 4000);
	})`

	var buf []byte
	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(dataURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, exp, e := runtime.Evaluate(waitImagesJS).WithAwaitPromise(true).Do(ctx)
			if e != nil {
				return e
			}
			if exp != nil {
				return exp
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var e error
			buf, _, e = page.PrintToPDF().
				WithPrintBackground(true).
				WithPreferCSSPageSize(true).
				Do(ctx)
			return e
		}),
	)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// invoiceFallbackPDF renders a clean invoice PDF using a pure-Go engine (no
// external Chrome dependency). Layout is a single professional design.
func invoiceFallbackPDF(d invoiceRenderData) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("") // cp1252 covers Spanish + €
	t := func(s string) string { return tr(s) }
	pdf.SetMargins(18, 18, 18)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()

	accentR, accentG, accentB := hexToRGB(d.Accent)

	// Header: issuer (left) + FACTURA (right)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.SetTextColor(17, 24, 39)
	pdf.CellFormat(110, 8, t(d.Issuer.Name), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(accentR, accentG, accentB)
	pdf.CellFormat(64, 8, "FACTURA", "", 1, "R", false, 0, "")

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(107, 114, 128)
	issuerLines := []string{}
	if d.Issuer.CIF != "" {
		issuerLines = append(issuerLines, "CIF/NIF: "+d.Issuer.CIF)
	}
	if d.Issuer.Address != "" {
		issuerLines = append(issuerLines, d.Issuer.Address)
	}
	if d.Issuer.Phone != "" {
		issuerLines = append(issuerLines, "Tel: "+d.Issuer.Phone)
	}
	if d.Issuer.Email != "" {
		issuerLines = append(issuerLines, d.Issuer.Email)
	}
	startY := pdf.GetY()
	for _, l := range issuerLines {
		pdf.CellFormat(110, 5, t(l), "", 2, "L", false, 0, "")
	}
	// right-side meta
	pdf.SetXY(122, startY)
	meta := []string{"Nº " + d.Number}
	if d.InvoiceDate != "" {
		meta = append(meta, "Fecha: "+d.InvoiceDate)
	}
	if d.DueDate != "" {
		meta = append(meta, "Vencimiento: "+d.DueDate)
	}
	meta = append(meta, "Estado: "+d.StatusLabel)
	for _, m := range meta {
		pdf.SetX(122)
		pdf.CellFormat(70, 5, t(m), "", 2, "R", false, 0, "")
	}

	pdf.Ln(6)
	pdf.SetDrawColor(accentR, accentG, accentB)
	pdf.SetLineWidth(0.5)
	y := pdf.GetY()
	pdf.Line(18, y, 192, y)
	pdf.Ln(6)

	// Customer
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(accentR, accentG, accentB)
	pdf.CellFormat(0, 6, t("FACTURAR A"), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(17, 24, 39)
	pdf.CellFormat(0, 6, t(d.Customer.Name), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(107, 114, 128)
	for _, l := range []string{
		nonEmpty("DNI/CIF: ", d.Customer.DniCif),
		d.Customer.Address,
		d.Customer.Email,
		nonEmpty("Tel: ", d.Customer.Phone),
	} {
		if l != "" {
			pdf.CellFormat(0, 5, t(l), "", 1, "L", false, 0, "")
		}
	}
	pdf.Ln(4)

	// Line items table
	if d.HasLines {
		pdf.SetFillColor(accentR, accentG, accentB)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetFont("Helvetica", "B", 9)
		pdf.CellFormat(86, 8, t("Descripción"), "", 0, "L", true, 0, "")
		pdf.CellFormat(20, 8, t("Cant."), "", 0, "R", true, 0, "")
		pdf.CellFormat(30, 8, t("Precio"), "", 0, "R", true, 0, "")
		pdf.CellFormat(18, 8, t("IVA"), "", 0, "R", true, 0, "")
		pdf.CellFormat(30, 8, t("Total"), "", 1, "R", true, 0, "")

		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(55, 65, 81)
		for _, li := range d.Lines {
			pdf.CellFormat(86, 7, t(truncateText(li.Description, 60)), "B", 0, "L", false, 0, "")
			pdf.CellFormat(20, 7, t(li.Quantity), "B", 0, "R", false, 0, "")
			pdf.CellFormat(30, 7, t(li.UnitPrice), "B", 0, "R", false, 0, "")
			pdf.CellFormat(18, 7, t(li.IVARate), "B", 0, "R", false, 0, "")
			pdf.CellFormat(30, 7, t(li.Total), "B", 1, "R", false, 0, "")
		}
		pdf.Ln(3)
	}

	// Totals (right aligned block)
	totalsRow := func(label, val string, bold bool) {
		pdf.SetX(112)
		if bold {
			pdf.SetFont("Helvetica", "B", 12)
			pdf.SetTextColor(accentR, accentG, accentB)
		} else {
			pdf.SetFont("Helvetica", "", 9)
			pdf.SetTextColor(107, 114, 128)
		}
		pdf.CellFormat(45, 6, t(label), "", 0, "L", false, 0, "")
		if bold {
			pdf.SetTextColor(accentR, accentG, accentB)
		} else {
			pdf.SetTextColor(55, 65, 81)
		}
		pdf.CellFormat(35, 6, t(val), "", 1, "R", false, 0, "")
	}
	totalsRow("Base imponible", d.Subtotal, false)
	if d.HasIVA {
		totalsRow(d.IVALabel, d.IVAAmount, false)
	}
	if d.HasDiscount {
		totalsRow(d.DiscountLabel, d.DiscountAmount, false)
	}
	if d.HasDeposit {
		totalsRow(d.DepositLabel, d.DepositAmount, false)
	}
	yy := pdf.GetY()
	pdf.Line(112, yy, 192, yy)
	pdf.Ln(1)
	totalsRow("Total", d.Total, true)

	if d.PaymentMethod != "" {
		pdf.Ln(6)
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(107, 114, 128)
		pdf.CellFormat(0, 5, t("Forma de pago: "+d.PaymentMethod), "", 1, "L", false, 0, "")
	}
	if d.Notes != "" {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "I", 9)
		pdf.MultiCell(0, 5, t(d.Notes), "", "L", false)
	}

	var out strings.Builder
	if err := pdf.Output(stringWriter{&out}); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

func hexToRGB(hex string) (int, int, int) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return 55, 65, 81
	}
	parse := func(s string) int {
		v, err := strconv.ParseInt(s, 16, 0)
		if err != nil {
			return 0
		}
		return int(v)
	}
	return parse(hex[0:2]), parse(hex[2:4]), parse(hex[4:6])
}

func nonEmpty(prefix, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return prefix + v
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
