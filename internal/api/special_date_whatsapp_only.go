package api

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"strings"
)

// Coordination id: special_date_whatsapp_only_v1
//
// A special date flagged whatsapp_only is booked only through the restaurant's
// WhatsApp:
//   - backoffice /app/reservas/especial switch -> special_dates.whatsapp_only
//   - public GET /reservations/special-dates, /reservations/special-date and
//     the month availability marker carry whatsapp_only + whatsapp_phone
//   - preactvillacarmen /reservas shows the WhatsApp notice instead of the
//     time / party selectors for that date
//   - POST /bookings/front and /bookings/front/checkout refuse the date
//     (error_code SPECIAL_DATE_WHATSAPP_ONLY)
//   - the WhatsApp bot never sends the web link for it and forwards the
//     booking request to the management group.

const specialDateWhatsAppOnlyErrorCode = "SPECIAL_DATE_WHATSAPP_ONLY"

// specialDateWhatsAppOnlyCheckpoint is the named observational point of the flow.
func specialDateWhatsAppOnlyCheckpoint(format string, args ...any) {
	log.Printf("[special_date_whatsapp_only_v1] "+format, args...)
}

// specialDateIsWhatsAppOnly reports whether dateISO is an active special date
// that can only be booked through WhatsApp.
func (s *Server) specialDateIsWhatsAppOnly(ctx context.Context, restaurantID int, dateISO string) (bool, error) {
	var flag int
	err := s.db.QueryRowContext(ctx, `
		SELECT whatsapp_only FROM special_dates
		WHERE restaurant_id = ? AND date = ? AND is_active = 1
		LIMIT 1`, restaurantID, dateISO).Scan(&flag)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return flag != 0, nil
}

// restaurantWhatsAppPhone is the WhatsApp number customers are sent to: the
// management phone the team answers (backoffice Configuración > Contacto,
// "Teléfono de gestión", the number the website already links to WhatsApp),
// else the line connected in Configuración > WhatsApp, else the public phone.
// Digits only, empty when none.
func (s *Server) restaurantWhatsAppPhone(ctx context.Context, restaurantID int) string {
	source, phone := "management_phone", digitsOnly(s.botManagementPhone(ctx, restaurantID))
	if phone == "" {
		var connected sql.NullString
		err := s.db.QueryRowContext(ctx, `
			SELECT connected_phone FROM restaurant_uazapi_instances
			WHERE restaurant_id = ? AND is_active = 1 AND status = 'connected'
			LIMIT 1`, restaurantID).Scan(&connected)
		if err != nil && err != sql.ErrNoRows && !isSQLSchemaError(err) {
			specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d phone_lookup_error=%v", restaurantID, err)
		}
		source, phone = "whatsapp_connection", digitsOnly(connected.String)
	}
	if phone == "" {
		source, phone = "public_phone", digitsOnly(s.botRestaurantPhone(ctx, restaurantID))
	}
	specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d phone_source=%s found=%t", restaurantID, source, phone != "")
	return phone
}

// whatsAppOnlyBookingError is the answer the public booking endpoints give
// for a WhatsApp-only date, or nil when the date is bookable online. A failed
// lookup refuses nothing (the rest of the validation still runs).
func (s *Server) whatsAppOnlyBookingError(ctx context.Context, restaurantID int, dateISO string) *frontBookingError {
	only, err := s.specialDateIsWhatsAppOnly(ctx, restaurantID, strings.TrimSpace(dateISO))
	if err != nil {
		specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d date=%s lookup_error=%v", restaurantID, dateISO, err)
		return nil
	}
	if !only {
		return nil
	}
	specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d date=%s booking_refused=true", restaurantID, dateISO)
	return &frontBookingError{Status: http.StatusConflict, Body: map[string]any{
		"success":        false,
		"message":        "Las reservas para esta fecha solo se hacen por WhatsApp.",
		"error_code":     specialDateWhatsAppOnlyErrorCode,
		"whatsapp_phone": s.restaurantWhatsAppPhone(ctx, restaurantID),
	}}
}
