package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
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
// invoiceStatusPillColor picks the status pill color from the Spanish label.
func invoiceStatusPillColor(label string, defR, defG, defB int) (int, int, int) {
	l := strings.ToLower(label)
	switch {
	case strings.Contains(l, "pagad"):
		return 22, 163, 74
	case strings.Contains(l, "pendient"):
		return 217, 119, 6
	case strings.Contains(l, "vencid"), strings.Contains(l, "anulad"):
		return 220, 38, 38
	case strings.Contains(l, "borrador"):
		return 107, 114, 128
	}
	return defR, defG, defB
}

// registerInvoiceLogo fetches the issuer logo and registers it with the PDF.
// Returns the width (mm) the logo occupies at 12mm height, or false on any
// failure - the logo is always best-effort.
func registerInvoiceLogo(pdf *fpdf.Fpdf, url string) (float64, bool) {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url) // #nosec G107 - branding asset URL from restaurant config
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	imgType := ""
	ct := resp.Header.Get("Content-Type")
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(ct, "png"), strings.HasSuffix(lower, ".png"):
		imgType = "PNG"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"), strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		imgType = "JPG"
	default:
		return 0, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil || len(data) == 0 {
		return 0, false
	}
	info := pdf.RegisterImageOptionsReader(url, fpdf.ImageOptions{ImageType: imgType, ReadDpi: true}, bytes.NewReader(data))
	if info == nil || info.Height() == 0 {
		return 0, false
	}
	return 12 * info.Width() / info.Height(), true
}

// invoiceFallbackPDF renders the invoice natively with fpdf when no Chrome is
// available: accent band, branded header with optional logo, meta card with
// status pill, zebra items table, boxed totals and a footer on every page.
func invoiceFallbackPDF(d invoiceRenderData) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("") // cp1252 covers Spanish + €
	t := func(s string) string { return tr(s) }

	accentR, accentG, accentB := hexToRGB(d.Accent)
	inkR, inkG, inkB := 17, 24, 39
	bodyR, bodyG, bodyB := 55, 65, 81
	mutR, mutG, mutB := 107, 114, 128
	lnR, lnG, lnB := 229, 231, 235
	cardR, cardG, cardB := 243, 244, 246
	zebR, zebG, zebB := 249, 250, 251

	const (
		marginL = 16.0
		marginR = 16.0
		pageW   = 210.0
	)
	contentW := pageW - marginL - marginR
	right := pageW - marginR
	issuer := d.Issuer

	pdf.SetMargins(marginL, 14, marginR)
	pdf.SetAutoPageBreak(true, 24)
	pdf.AliasNbPages("")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-18)
		pdf.SetDrawColor(accentR, accentG, accentB)
		pdf.SetLineWidth(0.4)
		y := pdf.GetY()
		pdf.Line(marginL, y, right, y)
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "", 8)
		pdf.SetTextColor(mutR, mutG, mutB)
		pdf.SetX(marginL)
		pdf.CellFormat(120, 4, t(joinNonEmpty("  ·  ", issuer.Name, issuer.Email)), "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 4, t("Gracias por su confianza"), "", 1, "R", false, 0, "")
	})
	pdf.AddPage()

	// Top accent band.
	pdf.SetFillColor(accentR, accentG, accentB)
	pdf.Rect(0, 0, pageW, 4, "F")

	headerY := 12.0

	// Logo (best-effort) + issuer block on the left.
	logoW := 0.0
	if u := strings.TrimSpace(issuer.LogoURL); strings.HasPrefix(u, "http") {
		if w, ok := registerInvoiceLogo(pdf, u); ok {
			pdf.ImageOptions(u, marginL, headerY+1, w, 12, false, fpdf.ImageOptions{ReadDpi: true}, 0, "")
			logoW = w + 4
		}
	}
	textX := marginL + logoW
	pdf.SetXY(textX, headerY)
	pdf.SetFont("Helvetica", "B", 17)
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.CellFormat(96-logoW, 7, t(issuer.Name), "", 2, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.SetTextColor(mutR, mutG, mutB)
	issuerLines := []string{}
	if issuer.CIF != "" {
		issuerLines = append(issuerLines, "CIF/NIF: "+issuer.CIF)
	}
	if issuer.Address != "" {
		issuerLines = append(issuerLines, issuer.Address)
	}
	contact := joinNonEmpty("  ·  ", nonEmpty("Tel: ", issuer.Phone), issuer.Email)
	if contact != "" {
		issuerLines = append(issuerLines, contact)
	}
	for _, l := range issuerLines {
		pdf.SetX(textX)
		pdf.CellFormat(96-logoW, 4.4, t(l), "", 2, "L", false, 0, "")
	}
	leftEnd := pdf.GetY()

	// FACTURA wordmark top-right.
	pdf.SetXY(112, headerY)
	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(accentR, accentG, accentB)
	pdf.CellFormat(right-112, 9, "FACTURA", "", 0, "R", false, 0, "")

	// Meta card: number, reference, dates + status pill.
	cardX := 118.0
	cardW := right - cardX
	cardY := headerY + 13
	metaRows := [][2]string{}
	if d.Number != "" {
		metaRows = append(metaRows, [2]string{"Nº de factura", d.Number})
		metaRows = append(metaRows, [2]string{"Referencia", d.Number})
	}
	if d.InvoiceDate != "" {
		metaRows = append(metaRows, [2]string{"Fecha", d.InvoiceDate})
	}
	if d.DueDate != "" {
		metaRows = append(metaRows, [2]string{"Vencimiento", d.DueDate})
	}
	rowH := 5.4
	cardH := 6 + float64(len(metaRows))*rowH + 4 + 7 + 4
	pdf.SetFillColor(cardR, cardG, cardB)
	pdf.RoundedRect(cardX, cardY, cardW, cardH, 2.5, "1234", "F")
	my := cardY + 5
	for _, row := range metaRows {
		pdf.SetXY(cardX+5, my)
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.SetTextColor(mutR, mutG, mutB)
		pdf.CellFormat(cardW/2-5, 4.5, t(row[0]), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 8.5)
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.CellFormat(cardW/2, 4.5, t(row[1]), "", 1, "R", false, 0, "")
		my += rowH
	}
	// Status pill.
	pillR, pillG, pillB := invoiceStatusPillColor(d.StatusLabel, accentR, accentG, accentB)
	pdf.SetFont("Helvetica", "B", 8)
	pillText := strings.ToUpper(d.StatusLabel)
	pillW := pdf.GetStringWidth(t(pillText)) + 9
	py := my + 2.5
	pdf.SetFillColor(pillR, pillG, pillB)
	pdf.RoundedRect(right-5-pillW, py, pillW, 6, 3, "1234", "F")
	pdf.SetXY(right-5-pillW, py+1.1)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(pillW, 4, t(pillText), "", 0, "C", false, 0, "")

	// Content starts below the taller of both columns.
	y := leftEnd
	if cardY+cardH > y {
		y = cardY + cardH
	}
	y += 7
	pdf.SetY(y)

	// FACTURAR A with accent side bar.
	barY := y
	pdf.SetXY(marginL+4, barY)
	pdf.SetFont("Helvetica", "B", 8.5)
	pdf.SetTextColor(accentR, accentG, accentB)
	pdf.CellFormat(0, 4.5, "FACTURAR A", "", 2, "L", false, 0, "")
	pdf.SetX(marginL + 4)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(inkR, inkG, inkB)
	pdf.CellFormat(0, 6, t(d.Customer.Name), "", 2, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 8.5)
	pdf.SetTextColor(mutR, mutG, mutB)
	for _, l := range []string{
		nonEmpty("DNI/CIF: ", d.Customer.DniCif),
		d.Customer.Address,
		d.Customer.Email,
		nonEmpty("Tel: ", d.Customer.Phone),
	} {
		if l != "" {
			pdf.SetX(marginL + 4)
			pdf.CellFormat(0, 4.4, t(l), "", 2, "L", false, 0, "")
		}
	}
	pdf.SetFillColor(accentR, accentG, accentB)
	pdf.Rect(marginL, barY, 1.1, pdf.GetY()-barY, "F")
	pdf.Ln(6)

	// Line items table.
	if d.HasLines {
		pdf.SetFillColor(accentR, accentG, accentB)
		pdf.SetTextColor(255, 255, 255)
		pdf.SetFont("Helvetica", "B", 8.5)
		pdf.CellFormat(82, 9, t("  Descripción"), "", 0, "L", true, 0, "")
		pdf.CellFormat(16, 9, t("Cant."), "", 0, "R", true, 0, "")
		pdf.CellFormat(30, 9, t("Precio"), "", 0, "R", true, 0, "")
		pdf.CellFormat(16, 9, t("IVA"), "", 0, "R", true, 0, "")
		pdf.CellFormat(34, 9, t("Total  "), "", 1, "R", true, 0, "")

		pdf.SetDrawColor(lnR, lnG, lnB)
		pdf.SetLineWidth(0.2)
		pdf.SetFont("Helvetica", "", 9)
		for i, li := range d.Lines {
			fill := i%2 == 1
			if fill {
				pdf.SetFillColor(zebR, zebG, zebB)
			}
			pdf.SetTextColor(bodyR, bodyG, bodyB)
			pdf.CellFormat(82, 8, t("  "+truncateText(li.Description, 58)), "B", 0, "L", fill, 0, "")
			pdf.CellFormat(16, 8, t(li.Quantity), "B", 0, "R", fill, 0, "")
			pdf.CellFormat(30, 8, t(li.UnitPrice), "B", 0, "R", fill, 0, "")
			pdf.CellFormat(16, 8, t(li.IVARate), "B", 0, "R", fill, 0, "")
			pdf.SetTextColor(inkR, inkG, inkB)
			pdf.CellFormat(34, 8, t(li.Total+"  "), "B", 1, "R", fill, 0, "")
		}
		pdf.Ln(4)
	}

	// Keep totals + payment box together.
	if pdf.GetY() > 232 {
		pdf.AddPage()
	}

	// Totals block, right aligned.
	totalsRow := func(label, val string) {
		pdf.SetX(118)
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(mutR, mutG, mutB)
		pdf.CellFormat(38, 5.6, t(label), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(bodyR, bodyG, bodyB)
		pdf.CellFormat(38, 5.6, t(val), "", 1, "R", false, 0, "")
	}
	totalsRow("Base imponible", d.Subtotal)
	if d.HasIVA {
		totalsRow(d.IVALabel, d.IVAAmount)
	}
	if d.HasDiscount {
		totalsRow(d.DiscountLabel, d.DiscountAmount)
	}
	if d.HasDeposit {
		totalsRow(d.DepositLabel, d.DepositAmount)
	}
	// Total band.
	ty := pdf.GetY() + 1.5
	pdf.SetFillColor(accentR, accentG, accentB)
	pdf.RoundedRect(112, ty, right-112, 10, 2, "1234", "F")
	pdf.SetXY(118, ty+2)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(38, 6, "Total", "", 0, "L", false, 0, "")
	pdf.CellFormat(32, 6, t(d.Total), "", 1, "R", false, 0, "")
	pdf.SetY(ty + 10)

	// Payment method + notes box.
	if d.PaymentMethod != "" || d.Notes != "" {
		boxLines := []string{}
		if d.PaymentMethod != "" {
			boxLines = append(boxLines, "Forma de pago: "+d.PaymentMethod)
		}
		notesW := contentW - 10
		if d.Notes != "" {
			for _, nl := range pdf.SplitLines([]byte(t(d.Notes)), notesW) {
				boxLines = append(boxLines, string(nl))
			}
		}
		boxH := 5 + float64(len(boxLines))*4.6 + 4
		by := pdf.GetY() + 5
		pdf.SetFillColor(zebR, zebG, zebB)
		pdf.RoundedRect(marginL, by, contentW, boxH, 2, "1234", "F")
		pdf.SetXY(marginL+5, by+4)
		pdf.SetFont("Helvetica", "", 8.5)
		pdf.SetTextColor(bodyR, bodyG, bodyB)
		for _, l := range boxLines {
			pdf.SetX(marginL + 5)
			pdf.CellFormat(contentW-10, 4.6, l, "", 2, "L", false, 0, "")
		}
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
