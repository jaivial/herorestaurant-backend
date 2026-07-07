package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// SMTP sending
// ---------------------------------------------------------------------------

// smtpSend is a package-level var so tests can swap it for a recorder without
// making a real network call. smtpSendReal is the production implementation.
var smtpSend = smtpSendReal

func smtpSendReal(ctx context.Context, host string, port int, username, password, fromName, fromAddr, to, subject, htmlBody, encryption string) error {
	if host == "" || username == "" || password == "" {
		return fmt.Errorf("SMTP no configurado")
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("email destino inválido: %s", to)
	}

	addr := host + ":" + strconv.Itoa(port)
	from := fromAddr
	if fromName != "" {
		from = fromName + " <" + fromAddr + ">"
	}

	// Build SMTP message.
	var buf bytes.Buffer
	buf.WriteString("From: " + from + "\r\n")
	buf.WriteString("To: " + to + "\r\n")
	buf.WriteString("Subject: " + mimeEncodeSubject(subject) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	buf.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(htmlBody)

	auth := smtp.PlainAuth("", username, password, host)
	tlsConfig := &tls.Config{ServerName: host}

	var conn *smtp.Client
	var err error
	switch strings.ToLower(strings.TrimSpace(encryption)) {
	case "ssl":
		// Implicit TLS (typically port 465): dial over TLS, no StartTLS.
		tconn, derr := tls.Dial("tcp", addr, tlsConfig)
		if derr != nil {
			return fmt.Errorf("SMTP TLS Dial: %v", derr)
		}
		conn, err = smtp.NewClient(tconn, host)
		if err != nil {
			tconn.Close()
			return fmt.Errorf("SMTP NewClient: %v", err)
		}
	default:
		// "none" and "tls" both dial plaintext first.
		conn, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("SMTP Dial: %v", err)
		}
		if strings.ToLower(strings.TrimSpace(encryption)) != "none" {
			if err := conn.StartTLS(tlsConfig); err != nil {
				conn.Close()
				return fmt.Errorf("SMTP StartTLS: %v", err)
			}
		}
	}
	defer conn.Close()

	if err := conn.Auth(auth); err != nil {
		return fmt.Errorf("SMTP Auth: %v", err)
	}
	if err := conn.Mail(fromAddr); err != nil {
		return fmt.Errorf("SMTP Mail: %v", err)
	}
	if err := conn.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP Rcpt: %v", err)
	}
	wc, err := conn.Data()
	if err != nil {
		return fmt.Errorf("SMTP Data: %v", err)
	}
	if _, err := wc.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("SMTP Write: %v", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("SMTP Close: %v", err)
	}
	return conn.Quit()
}

func mimeEncodeSubject(s string) string {
	// RFC 2047 encode for non-ASCII.
	if isASCII(s) {
		return s
	}
	return "=?UTF-8?B?" + base64Encode(s) + "?="
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func base64Encode(s string) string {
	var buf bytes.Buffer
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		buf.WriteByte(b[i])
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// Email HTML template builder
// ---------------------------------------------------------------------------

func buildBookingEmailHTML(brandName string, logoURL string, contactPhone string, contactEmail string, contactAddress string, booking map[string]any, bookingID int64, baseURL string) string {
	customerName := anyToString(booking["customer_name"])
	resDate := anyToString(booking["reservation_date"])
	resTime := anyToString(booking["reservation_time"])
	partySize, _ := anyToInt(booking["party_size"])
	highChairs, _ := anyToInt(booking["high_chairs"])
	babyStrollers, _ := anyToInt(booking["baby_strollers"])
	specialMenu := toBool(booking["special_menu"])

	dateDisplay := resDate
	if t, err := time.Parse("2006-01-02", resDate); err == nil {
		dateDisplay = t.Format("02/01/2006")
	}
	timeDisplay := formatHHMM(resTime)

	// Build the dynamic contact block (only render non-empty fields).
	var contactItemsHTML string
	if contactPhone != "" {
		contactItemsHTML += `<li>Teléfono: <a href="tel:+` + digitsOnly(contactPhone) + `" style="color:#097969;text-decoration:none;">` + htmlEscape(contactPhone) + `</a></li>`
	}
	if contactEmail != "" {
		contactItemsHTML += `<li>Email: <a href="mailto:` + htmlEscape(contactEmail) + `" style="color:#097969;text-decoration:none;">` + htmlEscape(contactEmail) + `</a></li>`
	}
	if contactAddress != "" {
		contactItemsHTML += `<li>Dirección: ` + htmlEscape(contactAddress) + `</li>`
	}
	contactBlockHTML := ""
	if contactItemsHTML != "" {
		contactBlockHTML = `<p style="margin-bottom:20px;">Si necesita modificar o cancelar su reserva, por favor contáctenos:</p>
<ul style="margin-bottom:30px;padding-left:20px;">
` + contactItemsHTML + `
</ul>`
	}

	base := strings.TrimRight(baseURL, "/")

	// Build the menu/arroz row.
	menuArrozHTML := ""
	if specialMenu {
		menuTitle := extractFirstJSONArrayItem(anyToString(booking["arroz_type"]))
		if menuTitle == "" {
			menuTitle = "Menú de Grupo"
		}
		menuArrozHTML += tableRow("Menú", menuTitle)
		principales := strings.TrimSpace(anyToString(booking["commentary"]))
		if principales != "" {
			items := splitAndClean(principales)
			listHTML := "<ul style=\"margin:5px 0 0 0;padding-left:20px;\">"
			for _, item := range items {
				listHTML += "<li>" + htmlEscape(item) + "</li>"
			}
			listHTML += "</ul>"
			menuArrozHTML += tableRow("Principales", listHTML)
		}
	} else {
		arrozText := formatArrozEmail(booking)
		menuArrozHTML = tableRow("Arroz", htmlEscape(arrozText))
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Confirmación de Reserva - %s</title>
</head>
<body style="margin:0;padding:0;font-family:Arial,sans-serif;line-height:1.6;background-color:#f4f4f4;">
<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="max-width:600px;margin:0 auto;background-color:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 2px 4px rgba(0,0,0,0.1);">
<tr>
<td style="padding:30px 20px;text-align:center;background-color:#097969;">
<img src="%s" alt="%s" style="max-width:200px;height:auto;">
</td>
</tr>
<tr>
<td style="padding:30px 20px;">
<h1 style="color:#097969;margin-bottom:20px;text-align:center;">Confirmación de Reserva</h1>
<p style="margin-bottom:20px;">Estimado/a <strong>%s</strong>,</p>
<p style="margin-bottom:20px;">Gracias por elegir %s. Le confirmamos que su reserva ha sido registrada con éxito con los siguientes detalles:</p>
<table role="presentation" style="width:100%%;margin-bottom:30px;border-collapse:collapse;">
%s
%s
%s
%s
%s
</table>
` + contactBlockHTML + `
<div style="background-color:#f8f9fa;border-radius:8px;padding:20px;margin-bottom:30px;border:1px solid #e9ecef;text-align:center;">
<p style="margin:0 0 15px 0;font-size:14px;color:#666;line-height:1.6;">Al hacer esta reserva, usted ha confirmado y aceptado las condiciones de reserva y políticas del restaurante.</p>
<a href="%s/booking-policies" style="display:inline-block;padding:10px 25px;background-color:#097969;color:white;text-decoration:none;border-radius:5px;font-weight:bold;font-size:14px;">CONDICIONES</a>
</div>
<p style="margin-bottom:20px;">¡Esperamos darle la bienvenida pronto!</p>
<hr style="border:none;border-top:1px solid #eee;margin:30px 0;">
<div style="text-align:center;margin-bottom:30px;">
<p style="margin-bottom:15px;">Si desea cancelar su reserva puede hacerlo haciendo click aquí:</p>
<a href="%s/cancel?id=%d" style="display:inline-block;padding:10px 20px;background-color:#dc3545;color:white;text-decoration:none;border-radius:5px;font-weight:bold;">CANCELAR</a>
</div>
<p style="font-size:12px;color:#666;text-align:center;">Este es un email automático, por favor no responda a este mensaje.<br>&copy; 2024 %s. Todos los derechos reservados.</p>
</td>
</tr>
</table>
</body>
</html>`,
		htmlEscape(brandName),
		htmlEscape(logoURL),
		htmlEscape(brandName),
		htmlEscape(customerName),
		htmlEscape(brandName),
		tableRow("Fecha", htmlEscape(dateDisplay)),
		tableRow("Hora", htmlEscape(timeDisplay)),
		tableRow("Número de personas", strconv.Itoa(partySize)),
		menuArrozHTML,
		tableRow("Tronas", strconv.Itoa(highChairs))+tableRow("Carritos", strconv.Itoa(babyStrollers)),
		base,
		base,
		bookingID,
		htmlEscape(brandName),
	)
}

func tableRow(label, value string) string {
	return fmt.Sprintf(`<tr><td style="padding:10px;background-color:#f8f8f8;border-bottom:1px solid #eee;"><strong>%s</strong></td><td style="padding:10px;border-bottom:1px solid #eee;">%s</td></tr>`, label, value)
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func formatArrozEmail(booking map[string]any) string {
	toggleArroz := strings.TrimSpace(anyToString(booking["toggleArroz"]))
	if toggleArroz != "true" {
		return "No arroz"
	}
	types := parseJSONStringArray(anyToString(booking["arroz_type"]))
	servs := parseJSONIntArray(anyToString(booking["arroz_servings"]))
	if len(types) == 0 || len(servs) == 0 {
		return "No arroz"
	}
	parts := make([]string, 0, len(types))
	n := min(len(types), len(servs))
	for i := 0; i < n; i++ {
		t := strings.TrimSpace(types[i])
		s := servs[i]
		if t == "" || s <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s x %d", t, s))
	}
	if len(parts) == 0 {
		return "No arroz"
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// High-level: send confirmation emails for a booking
// ---------------------------------------------------------------------------

// resolveSMTPConfigForRestaurant loads the per-restaurant email provider config
// from email_provider_config (reusing the backoffice loader).
func resolveSMTPConfigForRestaurant(ctx context.Context, s *Server, restaurantID int) (boEmailProviderConfig, error) {
	return s.loadEmailProviderConfig(ctx, restaurantID)
}

// sendViaConfig sends one HTML email using the provider settings from the DB.
func sendViaConfig(ctx context.Context, cfg boEmailProviderConfig, fromName, fromAddr, to, subject, htmlBody string) error {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "gmail":
		return smtpSend(ctx, "smtp.gmail.com", 587, cfg.GmailFromEmail, cfg.GmailAppPassword, fromName, fromAddr, to, subject, htmlBody, "tls")
	default: // "smtp"
		port := cfg.SMTPPort
		if port <= 0 {
			port = 587
		}
		enc := cfg.SMTEncryption
		if enc == "" {
			enc = "tls"
		}
		return smtpSend(ctx, cfg.SMTPHost, port, cfg.SMTPUsername, cfg.SMTPPassword, fromName, fromAddr, to, subject, htmlBody, enc)
	}
}

func sendBookingConfirmationEmails(ctx context.Context, s *Server, restaurantID int, booking map[string]any, bookingID int64) (customerSent bool, restaurantSent bool, err error) {
	cfg, cfgErr := resolveSMTPConfigForRestaurant(ctx, s, restaurantID)
	if cfgErr != nil {
		return false, false, fmt.Errorf("cargando config de email: %w", cfgErr)
	}
	if cfg.ID == 0 || !cfg.IsActive {
		return false, false, fmt.Errorf("email provider not configured for restaurant %d", restaurantID)
	}

	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	fromName := branding.EmailFromName
	if fromName == "" {
		fromName = branding.BrandName
	}
	if fromName == "" {
		fromName = "Restaurante"
	}

	fromAddr := branding.EmailFromAddress
	if fromAddr == "" {
		if strings.EqualFold(strings.TrimSpace(cfg.Provider), "gmail") {
			fromAddr = cfg.GmailFromEmail
		} else {
			fromAddr = cfg.SMTPFromEmail
		}
	}
	if fromAddr == "" {
		return false, false, fmt.Errorf("email provider not configured for restaurant %d (falta email de origen)", restaurantID)
	}

	baseURL := publicBaseURLFromContext(ctx, s, restaurantID)
	logoURL := branding.LogoURL
	if logoURL == "" {
		logoURL = baseURL + "/media/logos/logo-negro.png"
	}

	brandName := branding.BrandName
	if brandName == "" {
		brandName = "Restaurante"
	}

	info, _ := s.loadRestaurantInfo(ctx, restaurantID)

	subject := "Confirmación de Reserva - " + brandName
	html := buildBookingEmailHTML(brandName, logoURL, info.Telefono, info.Email, info.Direccion, booking, bookingID, baseURL)

	// Send to customer.
	customerEmail := strings.TrimSpace(anyToString(booking["contact_email"]))
	if customerEmail != "" && customerEmail != fromAddr {
		if e := sendViaConfig(ctx, cfg, fromName, fromAddr, customerEmail, subject, html); e != nil {
			log.Printf("Failed to send booking email to customer %s: %v", customerEmail, e)
		} else {
			customerSent = true
			log.Printf("Booking confirmation email sent to customer %s for booking #%d", customerEmail, bookingID)
		}
	}

	// Send to restaurant (required).
	if e := sendViaConfig(ctx, cfg, fromName, fromAddr, fromAddr, subject, html); e != nil {
		log.Printf("Failed to send booking email to restaurant %s: %v", fromAddr, e)
		return customerSent, false, fmt.Errorf("error enviando email al restaurante: %v", e)
	}
	restaurantSent = true
	log.Printf("Booking confirmation email sent to restaurant %s for booking #%d", fromAddr, bookingID)

	return customerSent, restaurantSent, nil
}
