package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

// Per-restaurant WhatsApp booking notification settings.
// Coordination id shared with the backoffice panel: bkg-wa-notif.
const (
	bookingNotifCoordinationID  = "bkg-wa-notif"
	bookingNotifMinDaysBefore   = 1
	bookingNotifMaxDaysBefore   = 14
	bookingNotifDefaultDaysBefo = 2
)

type bookingNotificationSettings struct {
	SendConfirmation         bool `json:"sendConfirmation"`
	SendReconfirmation       bool `json:"sendReconfirmation"`
	ReconfirmationDaysBefore int  `json:"reconfirmationDaysBefore"`
}

func clampReconfirmationDays(days int) int {
	if days < bookingNotifMinDaysBefore {
		return bookingNotifDefaultDaysBefo
	}
	if days > bookingNotifMaxDaysBefore {
		return bookingNotifMaxDaysBefore
	}
	return days
}

// loadBookingNotificationSettings returns the stored settings, or the safe
// defaults (confirmation on, reconfirmation off) when no row exists so
// behaviour is unchanged for restaurants that never opened the panel.
func (s *Server) loadBookingNotificationSettings(ctx context.Context, restaurantID int) (bookingNotificationSettings, error) {
	out := bookingNotificationSettings{SendConfirmation: true, ReconfirmationDaysBefore: bookingNotifDefaultDaysBefo}
	err := s.db.QueryRowContext(ctx, `
		SELECT send_confirmation, send_reconfirmation, reconfirmation_days_before
		FROM booking_notification_settings
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&out.SendConfirmation, &out.SendReconfirmation, &out.ReconfirmationDaysBefore)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.ReconfirmationDaysBefore = clampReconfirmationDays(out.ReconfirmationDaysBefore)
	return out, nil
}

func (s *Server) saveBookingNotificationSettings(ctx context.Context, restaurantID int, in bookingNotificationSettings) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO booking_notification_settings
			(restaurant_id, send_confirmation, send_reconfirmation, reconfirmation_days_before)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			send_confirmation = VALUES(send_confirmation),
			send_reconfirmation = VALUES(send_reconfirmation),
			reconfirmation_days_before = VALUES(reconfirmation_days_before)
	`, restaurantID, in.SendConfirmation, in.SendReconfirmation, in.ReconfirmationDaysBefore)
	return err
}

func (s *Server) handleBOBookingNotificationsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	settings, err := s.loadBookingNotificationSettings(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando notificaciones de reserva")
		return
	}
	log.Printf("%s.settings.get restaurant=%d confirmation=%t reconfirmation=%t days=%d",
		bookingNotifCoordinationID, a.ActiveRestaurantID, settings.SendConfirmation, settings.SendReconfirmation, settings.ReconfirmationDaysBefore)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "settings": settings})
}

func (s *Server) handleBOBookingNotificationsPut(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var input bookingNotificationSettings
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	input.ReconfirmationDaysBefore = clampReconfirmationDays(input.ReconfirmationDaysBefore)
	if err := s.saveBookingNotificationSettings(r.Context(), a.ActiveRestaurantID, input); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando notificaciones de reserva")
		return
	}
	log.Printf("%s.settings.save restaurant=%d confirmation=%t reconfirmation=%t days=%d",
		bookingNotifCoordinationID, a.ActiveRestaurantID, input.SendConfirmation, input.SendReconfirmation, input.ReconfirmationDaysBefore)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "settings": input})
}
