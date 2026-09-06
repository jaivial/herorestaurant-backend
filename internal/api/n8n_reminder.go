package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

var n8nLimiter fixedWindowLimiter

func validateInternalAPIToken(r *http.Request) bool {
	received := strings.TrimSpace(r.Header.Get("X-Api-Token"))
	expected := strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN"))
	if expected == "" {
		// Mirror legacy PHP behavior: deny by default if not configured.
		return false
	}
	// Constant-time comparison (best-effort).
	return subtle.ConstantTimeCompare([]byte(received), []byte(expected)) == 1
}

func appendReminderLog(line string) {
	logPath := filepath.Join("logs", "n8n_reminder_log.txt")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = io.Copy(f, bytes.NewReader([]byte(line)))
}

func formatHHMM(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ":")
	if len(parts) >= 2 {
		return parts[0] + ":" + parts[1]
	}
	if len(raw) >= 5 {
		return raw[:5]
	}
	return raw
}

func normalizePhoneForReminder(countryCodeRaw string, phoneRaw string) (withCountryCode string, ok bool) {
	cc := onlyDigits(countryCodeRaw)
	phone := onlyDigits(phoneRaw)
	if cc == "" {
		cc = "34"
	}
	if len(cc) < 1 || len(cc) > 4 {
		return "", false
	}
	if phone == "" {
		return "", false
	}

	// If phone already looks like E.164 digits (contains the country code), do not double-prefix.
	if len(phone) >= 8 && len(phone) <= 15 && strings.HasPrefix(phone, cc) && len(phone) > 9 {
		return phone, true
	}

	// Enforce E.164 max length (15 digits). Keep permissive min length.
	if len(phone) < 6 || len(phone) > 15 {
		return "", false
	}
	if len(cc)+len(phone) > 15 {
		return "", false
	}
	return cc + phone, true
}

func needsRiceReminder(arrozType sql.NullString) bool {
	if !arrozType.Valid {
		return true
	}
	v := strings.TrimSpace(arrozType.String)
	if v == "" {
		return true
	}
	if v == "0" {
		return true
	}
	return strings.EqualFold(v, "null")
}

// bookingReminderExtras carries the optional booking detail rendered in the
// reminder. Arroz is always shown (as "No" when absent); tronas and carritos are
// always shown including 0, mirroring the confirmation message.
type bookingReminderExtras struct {
	ArrozLine     string
	HighChairs    int
	BabyStrollers int
}

func buildBookingReminderMessage(customerName, brandName, dateDisplay, timeDisplay string, partySize int, floorDisplay, salonDisplay string, extras bookingReminderExtras) string {
	msg := "Hola " + customerName + ",\n\n" +
		"Le recordamos su reserva en " + brandName + ":\n\n" +
		"📅 Fecha: " + dateDisplay + "\n" +
		"🕐 Hora: " + timeDisplay + "\n" +
		"👥 Personas: " + strconv.Itoa(partySize) + "\n"
	if floorDisplay != "" {
		msg += "📍 Planta: " + floorDisplay + "\n"
	}
	if salonDisplay != "" {
		msg += "🚪 Salón: " + salonDisplay + "\n"
	}
	arrozLine := strings.TrimSpace(extras.ArrozLine)
	if arrozLine == "" {
		arrozLine = "🍚 Arroz: No"
	}
	msg += arrozLine + "\n"
	msg += "👶 Tronas: " + strconv.Itoa(extras.HighChairs) + "\n"
	msg += "🍼 Carros de bebé: " + strconv.Itoa(extras.BabyStrollers) + "\n"
	return msg + "\nPor favor, confirme su asistencia haciendo clic en el botón de abajo:"
}

// bookingReminderArrozLine renders the arroz summary as a single unstarred line
// for the reminder, reusing the confirmation formatter so both stay in sync.
func bookingReminderArrozLine(arrozType, arrozServings sql.NullString) string {
	booking := map[string]any{
		"toggleArroz":    "true",
		"arroz_type":     arrozType.String,
		"arroz_servings": arrozServings.String,
	}
	line := strings.TrimRight(formatArrozWhatsApp(booking), "\n")
	return strings.ReplaceAll(line, "*", "")
}

func (s *Server) handleN8nReminder(w http.ResponseWriter, r *http.Request) {
	if !validateInternalAPIToken(r) {
		log.Printf("UNAUTHORIZED: n8nReminder.php access attempt from %s", clientIP(r))
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{
			"success":    false,
			"message":    "Unauthorized access.",
			"error_code": "SECURITY_BLOCK",
		})
		return
	}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success":    false,
			"message":    "Unknown restaurant.",
			"error_code": "UNKNOWN_RESTAURANT",
		})
		return
	}

	ip := clientIP(r)
	// Legacy rate limiting: 30 per hour per IP for retries.
	if !n8nLimiter.allow("ip:"+ip+":n8n_reminder", 30, time.Hour) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
			"success":    false,
			"message":    "Too many requests. Please wait before trying again.",
			"error_code": "SECURITY_BLOCK",
		})
		return
	}

	startedAt := time.Now()
	ts := startedAt.Format("2006-01-02 15:04:05")
	appendReminderLog("\n=== Reminder job started at: " + ts + " ===\n")

	results := map[string]any{
		"success":           false,
		"total":             0,
		"confirmation_sent": 0,
		"rice_sent":         0,
		"failed":            0,
		"details":           []any{},
	}

	// Date range: now to +48h (same logic as PHP).
	end := startedAt.Add(48 * time.Hour)
	currentDate := startedAt.Format("2006-01-02")
	currentTime := startedAt.Format("15:04:05")
	endDate := end.Format("2006-01-02")
	endTime := end.Format("15:04:05")
	appendReminderLog(ts + " - Checking bookings from " + currentDate + " " + currentTime + " to " + endDate + " " + endTime + "\n")

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT b.id, b.customer_name, b.contact_phone_country_code, b.contact_phone,
		       DATE_FORMAT(b.reservation_date, '%Y-%m-%d') AS reservation_date,
		       TIME_FORMAT(b.reservation_time, '%H:%i:%s') AS reservation_time,
		       b.party_size, b.arroz_type, b.arroz_servings, b.highChairs, b.babyStrollers,
		       b.preferred_floor_number, sal.name
		FROM bookings b
		LEFT JOIN restaurant_salons sal ON sal.id = b.preferred_salon_id AND sal.restaurant_id = b.restaurant_id
		WHERE b.restaurant_id = ?
		  AND (b.reminder_sent = 0 OR b.reminder_sent IS NULL)
		  AND (b.status = 'pending' OR b.status = 'confirmed' OR b.status IS NULL OR b.status = '')
		  AND (
		    (b.reservation_date > ? AND b.reservation_date < ?)
		    OR (b.reservation_date = ? AND b.reservation_time >= ?)
		    OR (b.reservation_date = ? AND b.reservation_time <= ?)
		  )
		ORDER BY b.reservation_date, b.reservation_time
	`, restaurantID, currentDate, endDate, currentDate, currentTime, endDate, endTime)
	if err != nil {
		results["error"] = err.Error()
		appendReminderLog(ts + " - ERROR: " + err.Error() + "\n")
		httpx.WriteJSON(w, http.StatusOK, results)
		return
	}
	defer rows.Close()

	type rowBooking struct {
		ID              int
		CustomerName    string
		ContactPhoneCC  sql.NullString
		ContactPhone    sql.NullString
		ReservationDate string
		ReservationTime string
		PartySize       int
		ArrozType       sql.NullString
		ArrozServings   sql.NullString
		HighChairs      sql.NullInt64
		BabyStrollers   sql.NullInt64
		PreferredFloor  sql.NullInt64
		SalonName       sql.NullString
	}

	var bookings []rowBooking
	for rows.Next() {
		var b rowBooking
		if err := rows.Scan(&b.ID, &b.CustomerName, &b.ContactPhoneCC, &b.ContactPhone, &b.ReservationDate, &b.ReservationTime,
			&b.PartySize, &b.ArrozType, &b.ArrozServings, &b.HighChairs, &b.BabyStrollers, &b.PreferredFloor, &b.SalonName); err != nil {
			results["error"] = err.Error()
			appendReminderLog(ts + " - ERROR: " + err.Error() + "\n")
			httpx.WriteJSON(w, http.StatusOK, results)
			return
		}
		bookings = append(bookings, b)
	}
	results["total"] = len(bookings)
	appendReminderLog(ts + " - Found " + strconv.Itoa(len(bookings)) + " bookings needing reminders\n")

	// Resolve the public base URL for booking links. Prefer the restaurant's
	// configured website (ConfigContacto → restaurant_info.website) so links
	// point to its own domain, then fall back to env var / request host.
	baseURL := ""
	if info, infoErr := s.loadRestaurantInfo(r.Context(), restaurantID); infoErr == nil && info.Website != "" {
		if normalized, normalizeErr := normalizeRestaurantWebsiteURL(info.Website); normalizeErr == nil && normalized != "" {
			baseURL = normalized
		}
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	}
	if baseURL == "" {
		if host := normalizedTenantHost(r); host != "" {
			baseURL = "https://" + host
		}
	}
	if baseURL == "" {
		results["error"] = "PUBLIC_BASE_URL not configured and host missing"
		appendReminderLog(ts + " - ERROR: missing base URL\n")
		httpx.WriteJSON(w, http.StatusOK, results)
		return
	}

	branding, _ := s.loadRestaurantBranding(r.Context(), restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Restaurante"
	}

	gw, gwOK := s.botGatewayFor(r.Context(), restaurantID)
	if !gwOK {
		results["error"] = "WhatsApp not configured"
		appendReminderLog(ts + " - ERROR: WhatsApp not configured\n")
		httpx.WriteJSON(w, http.StatusOK, results)
		return
	}

	sendMenu := func(ctx context.Context, phone string, text string, choices []string) (bool, string) {
		if err := s.sendWhatsAppMenuTracked(ctx, restaurantID, gw, phone, text, choices, "booking_reconfirmation"); err != nil {
			return false, err.Error()
		}
		return true, ""
	}

	for _, booking := range bookings {
		bookingID := booking.ID
		customerName := booking.CustomerName
		ccRaw := ""
		if booking.ContactPhoneCC.Valid {
			ccRaw = booking.ContactPhoneCC.String
		}
		phoneRaw := ""
		if booking.ContactPhone.Valid {
			phoneRaw = booking.ContactPhone.String
		}
		phoneWithPrefix, ok := normalizePhoneForReminder(ccRaw, phoneRaw)
		if !ok {
			appendReminderLog(ts + " - SKIPPED: Booking #" + strconv.Itoa(bookingID) + " - Invalid phone: " + ccRaw + " " + phoneRaw + "\n")
			results["failed"] = results["failed"].(int) + 1
			results["details"] = append(results["details"].([]any), map[string]any{
				"id":       bookingID,
				"customer": customerName,
				"phone":    ccRaw + " " + phoneRaw,
				"error":    "Invalid phone number",
			})
			continue
		}

		appendReminderLog(ts + " - Processing: Booking #" + strconv.Itoa(bookingID) + " - " + customerName + " - " + phoneWithPrefix + "\n")

		bookingDateDisplay := booking.ReservationDate
		if t, err := time.Parse("2006-01-02", booking.ReservationDate); err == nil {
			bookingDateDisplay = t.Format("02/01/2006")
		}
		bookingTimeDisplay := formatHHMM(booking.ReservationTime)
		partySize := booking.PartySize

		bookingDetail := map[string]any{
			"id":                bookingID,
			"customer":          customerName,
			"phone":             phoneWithPrefix,
			"confirmation_sent": false,
			"rice_sent":         false,
		}

		floorDisplay := ""
		if booking.PreferredFloor.Valid && booking.PreferredFloor.Int64 >= 0 {
			floorDisplay = "Planta " + strconv.FormatInt(booking.PreferredFloor.Int64, 10)
		}
		extras := bookingReminderExtras{
			ArrozLine:     bookingReminderArrozLine(booking.ArrozType, booking.ArrozServings),
			HighChairs:    int(booking.HighChairs.Int64),
			BabyStrollers: int(booking.BabyStrollers.Int64),
		}
		reminder := buildBookingReminderPayload(brandName, customerName, bookingDateDisplay, bookingTimeDisplay, partySize, floorDisplay, strings.TrimSpace(booking.SalonName.String), extras, int64(bookingID), baseURL)

		confirmOK, confirmErr := sendMenu(r.Context(), phoneWithPrefix, reminder.Text, reminder.Choices)
		if confirmOK {
			results["confirmation_sent"] = results["confirmation_sent"].(int) + 1
			bookingDetail["confirmation_sent"] = true
			appendReminderLog(ts + " - ✅ Confirmation link sent to booking #" + strconv.Itoa(bookingID) + "\n")
		} else {
			results["failed"] = results["failed"].(int) + 1
			bookingDetail["confirmation_error"] = confirmErr
			appendReminderLog(ts + " - ❌ Confirmation link failed for booking #" + strconv.Itoa(bookingID) + " - " + confirmErr + "\n")
		}

		needsRice := needsRiceReminder(booking.ArrozType)
		riceOK := false
		if needsRice {
			riceURL := baseURL + "/update-rice?id=" + strconv.Itoa(bookingID)
			riceMessage := "¿Le gustaría reservar arroz para su comida?\n\n" +
				"Tenemos una gran variedad de arroces disponibles.\n\n" +
				"Haga clic en el botón de abajo para ver el menú y hacer su reserva:"
			riceButtons := []string{
				"🍚 Reservar Arroz|" + riceURL,
			}
			ok, riceErr := sendMenu(r.Context(), phoneWithPrefix, riceMessage, riceButtons)
			riceOK = ok
			if ok {
				results["rice_sent"] = results["rice_sent"].(int) + 1
				bookingDetail["rice_sent"] = true
				appendReminderLog(ts + " - ✅ Rice booking link sent to booking #" + strconv.Itoa(bookingID) + "\n")
			} else {
				bookingDetail["rice_error"] = riceErr
				appendReminderLog(ts + " - ❌ Rice booking link failed for booking #" + strconv.Itoa(bookingID) + " - " + riceErr + "\n")
			}
		} else {
			arrozVal := ""
			if booking.ArrozType.Valid {
				arrozVal = booking.ArrozType.String
			}
			appendReminderLog(ts + " - ℹ️ Booking #" + strconv.Itoa(bookingID) + " already has arroz: " + arrozVal + " - Rice link not sent\n")
			bookingDetail["rice_sent"] = "not_needed"
		}

		// Mark reminder_sent and update conversation state if at least one outbound message was sent.
		if confirmOK || (needsRice && riceOK) {
			if _, err := s.db.ExecContext(r.Context(), "UPDATE bookings SET reminder_sent = 1 WHERE restaurant_id = ? AND id = ?", restaurantID, bookingID); err == nil {
				appendReminderLog(ts + " - ✅ Marked booking #" + strconv.Itoa(bookingID) + " as reminder_sent\n")
			} else {
				appendReminderLog(ts + " - ⚠️ Failed to mark reminder_sent for booking #" + strconv.Itoa(bookingID) + ": " + err.Error() + "\n")
			}

			ctxData, _ := json.Marshal(map[string]any{
				"booking_id":         bookingID,
				"customer_name":      customerName,
				"booking_date":       bookingDateDisplay,
				"booking_time":       bookingTimeDisplay,
				"party_size":         partySize,
				"reminder_type":      "confirmation",
				"rice_reminder_sent": needsRice,
				"sent_at":            time.Now().Format("2006-01-02 15:04:05"),
			})
			expiresAt := time.Now().Add(48 * time.Hour).Format("2006-01-02 15:04:05")

			var existingID int
			err := s.db.QueryRowContext(r.Context(), "SELECT id FROM conversation_states WHERE restaurant_id = ? AND sender_number = ? LIMIT 1", restaurantID, phoneWithPrefix).Scan(&existingID)
			if err == nil {
				_, err = s.db.ExecContext(r.Context(), `
					UPDATE conversation_states
					SET conversation_state = ?,
					    context_data = ?,
					    expires_at = ?,
					    updated_at = NOW()
					WHERE restaurant_id = ?
					  AND sender_number = ?
				`, "reminder_sent", string(ctxData), expiresAt, restaurantID, phoneWithPrefix)
			} else if err == sql.ErrNoRows {
				_, err = s.db.ExecContext(r.Context(), `
					INSERT INTO conversation_states (restaurant_id, sender_number, conversation_state, context_data, expires_at)
					VALUES (?, ?, ?, ?, ?)
				`, restaurantID, phoneWithPrefix, "reminder_sent", string(ctxData), expiresAt)
			}
			if err != nil {
				appendReminderLog(ts + " - ⚠️ Failed to update conversation_state: " + err.Error() + "\n")
			} else {
				appendReminderLog(ts + " - ✅ Updated conversation_state for " + phoneWithPrefix + "\n")
			}
		}

		results["details"] = append(results["details"].([]any), bookingDetail)
	}

	results["success"] = results["confirmation_sent"].(int) > 0 || results["total"].(int) == 0

	summary := "Total: " + strconv.Itoa(results["total"].(int)) +
		", Confirmation sent: " + strconv.Itoa(results["confirmation_sent"].(int)) +
		", Rice sent: " + strconv.Itoa(results["rice_sent"].(int)) +
		", Failed: " + strconv.Itoa(results["failed"].(int))
	appendReminderLog(ts + " - SUMMARY: " + summary + "\n")
	appendReminderLog("=== Reminder job completed at: " + time.Now().Format("2006-01-02 15:04:05") + " ===\n\n")

	httpx.WriteJSON(w, http.StatusOK, results)
}
