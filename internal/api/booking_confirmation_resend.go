package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Reenvío del WhatsApp de confirmación de una reserva.
// Coordination id: booking_confirmation_resend_v1
//
// El bot de los grupos privilegiados crea reservas normales SIN avisar al
// cliente (booking_propose_create, igual que el panel antiguo) y las
// especiales avisan una sola vez en el momento de crear; si el WhatsApp falla,
// se cambia el teléfono después o el cliente lo pierde, no había forma de
// reenviarlo: ni endpoint, ni Forky, ni el bot. Este núcleo cierra ese hueco:
//
//   - POST /api/admin/bookings/{id}/resend-confirmation (handleBOBookingResendConfirmation)
//   - Forky: booking_confirmation_resend (token de confirmación de un uso)
//   - bot de grupos/contactos privilegiados: booking_propose_resend_confirmation
//     (propuesta + "OK <código>"), que reenvía este mismo núcleo.
//
// Qué envía: el texto de confirmación estándar (buildBookingWhatsAppMessage)
// al teléfono ACTUAL de la reserva (la fila en DB es la fuente de verdad, no
// la memoria del turno que la creó); si es de fecha especial, el QR de la
// reserva (regenerado y re-almacenado con ensureBookingQR); y si es prereserva
// de fecha festiva, el PDF de comprobante del adelanto REGENERADO desde el
// snapshot guardado (special_json: adelantos_paid) y re-almacenado en
// bookings.receipt_url. El interruptor de confirmaciones del restaurante
// (bkg-wa-notif) NO aplica: un reenvío explícito del operador es su decisión.
// =============================================================================

const bookingResendCoordinationID = "booking_confirmation_resend_v1"

// bookingResendLoad reads the booking row (the DB, not the creating turn, is
// the source of truth) and refuses what must not be re-sent.
func (s *Server) bookingResendLoad(ctx context.Context, rid int, bookingID int64) (map[string]any, error) {
	if bookingID <= 0 {
		return nil, errors.New("booking_id inválido")
	}
	row, err := s.boFetchBookingByID(ctx, rid, int(bookingID))
	if err != nil || row == nil {
		return nil, errors.New("no hay ninguna reserva con ese id")
	}
	if anyToString(row["status"]) == "cancelled" {
		return nil, errors.New("la reserva está cancelada: no se reenvía la confirmación")
	}
	return row, nil
}

// bookingResendPhone resolves the destination the confirmation goes to.
func bookingResendPhone(row map[string]any) (string, string, bool) {
	cc := anyToString(row["contact_phone_country_code"])
	phone := anyToString(row["contact_phone"])
	_, _, clean, ok := normalizePhoneParts(cc, phone)
	if !ok || clean == "" {
		return cc, phone, false
	}
	return cc, clean, true
}

// bookingResendNotificationData builds the confirmation payload from the DB
// row: the same keys boBookingToNotificationData writes (toggleArroz and
// extras included), plus the special block of special-date bookings.
func (s *Server) bookingResendNotificationData(ctx context.Context, rid int, bookingID int64, row map[string]any) map[string]any {
	arrozType := anyToString(row["arroz_type"])
	specialMenu := toBool(row["special_menu"])
	toggleArroz := "false"
	if arrozType != "" && !specialMenu {
		toggleArroz = "true"
	}
	data := map[string]any{
		"booking_id":                 bookingID,
		"reservation_date":           anyToString(row["reservation_date"]),
		"reservation_time":           anyToString(row["reservation_time"]),
		"party_size":                 row["party_size"],
		"children":                   row["children"],
		"customer_name":              anyToString(row["customer_name"]),
		"contact_phone":              anyToString(row["contact_phone"]),
		"contact_phone_country_code": anyToString(row["contact_phone_country_code"]),
		"contact_email":              anyToString(row["contact_email"]),
		"commentary":                 anyToString(row["commentary"]),
		"arroz_type":                 arrozType,
		"arroz_servings":             anyToString(row["arroz_servings"]),
		"babyStrollers":              row["babyStrollers"],
		"high_chairs":                row["high_chairs"],
		"toggleArroz":                toggleArroz,
		"special_menu":               specialMenu,
		"menu_de_grupo_id":           row["menu_de_grupo_id"],
		"principales_json":           anyToString(row["principales_json"]),
		"preferred_floor_number":     row["preferred_floor_number"],
		"preferred_salon_id":         row["preferred_salon_id"],
		"is_special_booking":         toBool(row["is_special_booking"]),
		"is_prereserva":              toBool(row["is_prereserva"]),
		"special_json":               anyToString(row["special_json"]),
		"special":                    row["special"],
	}
	if names := bookingExtraNames(anyToString(row["extras_json"])); len(names) > 0 {
		data["extras"] = names
	}
	s.enrichBookingLocationForNotifications(ctx, rid, data)
	return data
}

// bookingResendReceiptTotal sums the adelantos already paid of a snapshot.
func bookingResendReceiptTotal(row map[string]any) (float64, []string) {
	var snap specialBookingSnapshot
	if err := json.Unmarshal([]byte(anyToString(row["special_json"])), &snap); err != nil {
		return 0, nil
	}
	total := 0.0
	methods := []string{}
	seen := map[string]bool{}
	for _, p := range snap.AdelantosPaid {
		if p.Amount <= 0 {
			continue
		}
		total += p.Amount
		if m := strings.TrimSpace(p.Method); m != "" && !seen[m] {
			seen[m] = true
			methods = append(methods, m)
		}
	}
	return total, methods
}

// bookingResendReceipt rebuilds the adelanto payment receipt of a festive
// prereserva from the stored snapshot (adelantos_paid), uploads it (Bunny) and
// records its URL on the booking. One stable object per booking
// ("reserva-<id>"): every regeneration overwrites it, so the URL is
// idempotent. Returns nil when there is nothing paid to certify.
func (s *Server) bookingResendReceipt(ctx context.Context, rid int, bookingID int64, row map[string]any, qr *bookingQR) *bookingReceipt {
	total, methods := bookingResendReceiptTotal(row)
	if total <= 0 {
		return nil
	}
	var snap specialBookingSnapshot
	_ = json.Unmarshal([]byte(anyToString(row["special_json"])), &snap)
	paymentRef := "adelanto registrado por el restaurante"
	if len(methods) > 0 {
		paymentRef = strings.Join(methods, " + ") + " (registrado por el restaurante)"
	}
	paidAt := time.Now()
	if t, err := time.Parse("2006-01-02 15:04:05", anyToString(row["added_date"])); err == nil {
		paidAt = t
	}
	cc, phone, _ := bookingResendPhone(row)
	party, _ := anyToInt(row["party_size"])
	branding, _ := s.loadRestaurantBranding(ctx, rid)
	in := receiptInput{
		Reference:    fmt.Sprintf("reserva-%d", bookingID),
		PaymentRef:   paymentRef,
		PaidAt:       paidAt,
		BrandName:    firstNonEmpty(branding.BrandName, "Restaurante"),
		Address:      branding.Address,
		Phone:        branding.Phone,
		Email:        branding.Email,
		Customer:     anyToString(row["customer_name"]),
		CustomerMail: anyToString(row["contact_email"]),
		CustomerTel:  maskPhone("+" + cc + phone),
		Date:         anyToString(row["reservation_date"]),
		Time:         strings.TrimSuffix(anyToString(row["reservation_time"]), ":00"),
		PartySize:    party,
		DateTitle:    snap.Title,
		BookingID:    bookingID,
		Total:        total,
		Currency:     "EUR",
	}
	if qr != nil {
		in.QRPNG = qr.PNG
	}
	for _, p := range snap.AdelantosPaid {
		if p.Amount > 0 {
			in.Lines = append(in.Lines, receiptLine{Label: strings.TrimSpace(p.Method), Count: 1, Amount: p.Amount})
		}
	}
	pdf, err := buildReceiptPDF(in)
	if err != nil {
		log.Printf("[%s] receipt_build_failed restaurant=%d booking=%d err=%v", bookingResendCoordinationID, rid, bookingID, err)
		return nil
	}
	receipt := s.storeReceipt(ctx, rid, fmt.Sprintf("reserva-%d", bookingID), pdf)
	s.setBookingReceiptURL(ctx, rid, bookingID, receipt.URL)
	return receipt
}

// bookingResendConfirmationApply sends the confirmation (and its QR / receipt
// attachments) to the customer's CURRENT phone. Shared by the panel endpoint,
// the Forky tool and the bot proposal: one core, three surfaces.
func (s *Server) bookingResendConfirmationApply(ctx context.Context, rid int, bookingID int64) (map[string]any, error) {
	row, err := s.bookingResendLoad(ctx, rid, bookingID)
	if err != nil {
		return nil, err
	}
	cc, phone, okPhone := bookingResendPhone(row)
	if !okPhone {
		return nil, errors.New("la reserva no tiene un teléfono de contacto válido")
	}
	data := s.bookingResendNotificationData(ctx, rid, bookingID, row)
	isSpecial := toBool(row["is_special_booking"])
	var qr *bookingQR
	if isSpecial {
		if qr = s.ensureBookingQR(ctx, rid, bookingID, anyToString(row["reservation_date"])); qr != nil {
			data[bookingQRKey] = qr
		}
	}
	var receipt *bookingReceipt
	if isSpecial && toBool(row["is_prereserva"]) {
		if receipt = s.bookingResendReceipt(ctx, rid, bookingID, row, qr); receipt != nil {
			data[bookingReceiptKey] = receipt
		}
	}
	sendErr := sendBookingWhatsAppToCustomerOpts(ctx, s, rid, data, bookingID, true)
	out := map[string]any{
		"booking_id":    bookingID,
		"phone":         "+" + cc + " " + phone,
		"whatsapp_sent": sendErr == nil,
		"qr_sent":       qr != nil && qr.URL != "",
		"receipt_sent":  receipt != nil && receipt.URL != "",
	}
	if qr != nil && qr.URL != "" {
		out["qr_url"] = qr.URL
	}
	if receipt != nil && receipt.URL != "" {
		out["receipt_url"] = receipt.URL
	}
	botCheckpoint(bookingResendCoordinationID, "restaurant_id=%d booking_id=%d sent=%t qr=%t receipt=%t err=%v",
		rid, bookingID, sendErr == nil, toBool(out["qr_sent"]), toBool(out["receipt_sent"]), sendErr)
	if sendErr != nil {
		out["message"] = "El WhatsApp no se pudo entregar ahora (" + sendErr.Error() + "); ha quedado en cola para reintentar cuando el proveedor responda."
	}
	return out, nil
}

// bookingResendConfirmationPreview describes exactly what the resend sends, so
// the human confirmation (Forky token, bot "OK <código>") covers the external
// messages explicitly. The row travels back for the after-hash binding.
func (s *Server) bookingResendConfirmationPreview(ctx context.Context, rid int, bookingID int64) (string, map[string]any, error) {
	row, err := s.bookingResendLoad(ctx, rid, bookingID)
	if err != nil {
		return "", nil, err
	}
	cc, phone, okPhone := bookingResendPhone(row)
	if !okPhone {
		return "", nil, errors.New("la reserva no tiene un teléfono de contacto válido")
	}
	isSpecial := toBool(row["is_special_booking"])
	party, _ := anyToInt(row["party_size"])
	text := fmt.Sprintf("REENVIAR el WhatsApp de confirmación de la reserva #%d (%s a las %s, %d comensales, a nombre de %s) al teléfono actual +%s %s",
		bookingID, botFormatISODateES(anyToString(row["reservation_date"])), botClipTime(anyToString(row["reservation_time"])), party,
		anyToString(row["customer_name"]), cc, phone)
	var parts []string
	if isSpecial {
		parts = append(parts, "el QR de la reserva")
	}
	if isSpecial && toBool(row["is_prereserva"]) {
		if total, _ := bookingResendReceiptTotal(row); total > 0 {
			parts = append(parts, fmt.Sprintf("el comprobante de pago del adelanto REGENERADO (%s abonados)", botEUR(total)))
		}
	}
	if len(parts) > 0 {
		text += ", con " + strings.Join(parts, " y ")
	}
	text += ". La reserva no cambia: solo se avisa al cliente."
	return text, row, nil
}

// bookingResendAfterKey binds the preview to the exact resolved send (status,
// phone, date, party, special snapshot), so a proposal gone stale (phone
// changed after the preview) is rejected, never silently re-sent.
func bookingResendAfterKey(rid int, bookingID int64, row map[string]any) string {
	return botSha("bookingresendv1|" + botCanon(map[string]any{
		"rid": rid, "id": bookingID,
		"status":   anyToString(row["status"]),
		"date":     anyToString(row["reservation_date"]),
		"time":     anyToString(row["reservation_time"]),
		"party":    row["party_size"],
		"name":     anyToString(row["customer_name"]),
		"cc":       anyToString(row["contact_phone_country_code"]),
		"phone":    anyToString(row["contact_phone"]),
		"special":  anyToString(row["special_json"]),
		"preres":   toBool(row["is_prereserva"]),
		"specialb": toBool(row["is_special_booking"]),
	}))
}

// handleBOBookingResendConfirmation re-sends the confirmation WhatsApp of a
// booking to its current phone. Forky and the privileged bot replay this
// handler through assistantCallHandler, so panel, assistant and bot share the
// same core and the same audit path.
// POST /api/admin/bookings/{id}/resend-confirmation
func (s *Server) handleBOBookingResendConfirmation(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid booking id"})
		return
	}
	out, aerr := s.bookingResendConfirmationApply(r.Context(), a.ActiveRestaurantID, id)
	if aerr != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": aerr.Error()})
		return
	}
	// Honest success: only a delivered WhatsApp is a success. A send that
	// failed (gateway down, phone not on WhatsApp) stays queued in the outbox
	// and travels as success=false + message.
	out["success"] = toBool(out["whatsapp_sent"])
	httpx.WriteJSON(w, http.StatusOK, out)
}
