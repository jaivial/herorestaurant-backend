package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func isNormallyClosedDay(dateStr string) bool {
	t, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		return false
	}
	switch t.Weekday() {
	case time.Monday, time.Tuesday, time.Wednesday:
		return true
	default:
		return false
	}
}

func isDateClosed(dateStr string, closedDays map[string]bool, openedDays map[string]bool) bool {
	if openedDays[dateStr] {
		return false
	}
	if closedDays[dateStr] {
		return true
	}
	return isNormallyClosedDay(dateStr)
}

func dateTooFarInFuture(dateStr string) (isTooFar bool, daysUntil int) {
	today := time.Now()
	todayMid := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	target, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		return false, 0
	}
	diff := target.Sub(todayMid)
	if diff < 0 {
		daysUntil = int(-diff.Hours() / 24)
		return false, daysUntil
	}
	daysUntil = int(diff.Hours() / 24)
	return diff > (35 * 24 * time.Hour), daysUntil
}

type availableHour struct {
	Time          string `json:"time"`
	Capacity      int    `json:"capacity"`
	TotalCapacity int    `json:"totalCapacity,omitempty"`
	Bookings      int    `json:"bookings,omitempty"`
	Status        string `json:"status,omitempty"`
}

func formatAvailableHours(hours []availableHour) string {
	if len(hours) == 0 {
		return ""
	}
	times := make([]string, 0, len(hours))
	for _, h := range hours {
		times = append(times, h.Time)
	}
	if len(times) == 1 {
		return times[0]
	}
	if len(times) == 2 {
		return times[0] + " o " + times[1]
	}
	last := times[len(times)-1]
	return strings.Join(times[:len(times)-1], ", ") + " o " + last
}

func (s *Server) fetchClosedAndOpenedDays(r *http.Request) (closed map[string]bool, opened map[string]bool, err error) {
	closed = map[string]bool{}
	opened = map[string]bool{}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return nil, nil, errors.New("unknown restaurant")
	}

	rows, err := s.db.QueryContext(r.Context(), "SELECT date, is_open FROM restaurant_days WHERE restaurant_id = ?", restaurantID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var isOpen bool
		if err := rows.Scan(&d, &isOpen); err != nil {
			return nil, nil, err
		}
		ds := d.Format("2006-01-02")
		if isOpen {
			opened[ds] = true
		} else {
			closed[ds] = true
		}
	}
	return closed, opened, nil
}

func (s *Server) computeAvailableHoursForPartySize(r *http.Request, date string, partySize int) ([]availableHour, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return nil, errors.New("unknown restaurant")
	}

	dailyLimit := 45
	_ = s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if dailyLimit <= 0 {
		dailyLimit = 45
	}

	bookingsByHour, _, err := s.fetchBookingsByHourHHMM(r, date)
	if err != nil {
		return nil, err
	}

	var hourDataRaw sql.NullString
	err = s.db.QueryRowContext(r.Context(), "SELECT hourData FROM hour_configuration WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&hourDataRaw)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	hourData := map[string]HourSlot{}
	activeHours := []string{}

	if err == sql.ErrNoRows || !hourDataRaw.Valid || strings.TrimSpace(hourDataRaw.String) == "" {
		activeHours, err = s.getOpeningHoursForDate(r, date)
		if err != nil {
			return nil, err
		}
		equal := 100.0 / float64(len(activeHours))
		for _, hour := range activeHours {
			bookings := bookingsByHour[hour]
			totalCapacity := int(math.Ceil((equal / 100.0) * float64(dailyLimit)))
			availableCapacity := totalCapacity - bookings
			completion := 0.0
			if totalCapacity > 0 {
				completion = (float64(bookings) / float64(totalCapacity)) * 100.0
			}
			status := "available"
			if completion > 90 {
				status = "full"
			} else if completion > 70 {
				status = "limited"
			}
			hourData[hour] = HourSlot{
				Status:        status,
				Capacity:      availableCapacity,
				TotalCapacity: totalCapacity,
				Bookings:      bookings,
				Percentage:    equal,
				Completion:    completion,
				IsClosed:      false,
			}
		}
	} else {
		if err := json.Unmarshal([]byte(hourDataRaw.String), &hourData); err != nil {
			return nil, errors.New("Invalid hourData JSON in database")
		}
		for hour, data := range hourData {
			bookings := bookingsByHour[hour]
			data.Bookings = bookings

			totalCapacity := int(math.Ceil((data.Percentage / 100.0) * float64(dailyLimit)))
			data.TotalCapacity = totalCapacity
			data.Capacity = totalCapacity - bookings
			if totalCapacity > 0 {
				data.Completion = (float64(bookings) / float64(totalCapacity)) * 100.0
			} else {
				data.Completion = 0
			}
			if !data.IsClosed && data.Status != "closed" {
				if data.Completion > 90 {
					data.Status = "full"
				} else if data.Completion > 70 {
					data.Status = "limited"
				} else {
					data.Status = "available"
				}
			}
			hourData[hour] = data
		}

		activeHours = make([]string, 0, len(hourData))
		for h := range hourData {
			activeHours = append(activeHours, h)
		}
	}

	sort.Strings(activeHours)

	out := []availableHour{}
	for _, h := range activeHours {
		slot := hourData[h]
		if slot.IsClosed || strings.EqualFold(slot.Status, "closed") {
			continue
		}
		if slot.Capacity >= partySize {
			out = append(out, availableHour{
				Time:          h,
				Capacity:      slot.Capacity,
				TotalCapacity: slot.TotalCapacity,
				Bookings:      slot.Bookings,
				Status:        slot.Status,
			})
		}
	}
	return out, nil
}

func (s *Server) handleCheckDateAvailability(w http.ResponseWriter, r *http.Request) {
	var input struct {
		NewDate      string `json:"newDate"`
		PartySize    *int   `json:"partySize"`
		CurrentTime  string `json:"currentTime"`
		BookingID    *int   `json:"bookingId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"available": false,
			"message":   "Unknown restaurant",
		})
		return
	}

	newDate := strings.TrimSpace(input.NewDate)
	if newDate == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing required parameter: newDate",
		})
		return
	}

	// Simple mode: only open/closed checks.
	simpleCheck := input.PartySize == nil

	closedDays, openedDays, err := s.fetchClosedAndOpenedDays(r)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"available": false,
			"message":   err.Error(),
		})
		return
	}

	// Special holidays: cannot be booked/modified.
	special := map[string]bool{
		"12-24": true, "12-25": true, "12-31": true,
		"01-01": true, "01-05": true, "01-06": true,
	}
	if t, err := time.ParseInLocation("2006-01-02", newDate, time.Local); err == nil {
		if special[t.Format("01-02")] {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success":        true,
				"available":      false,
				"hasAvailability": false,
				"message":        "Uy, esos días festivos (24, 25 y 31 de diciembre, 1, 5 y 6 de enero) tienen un menú especial y no se pueden modificar reservas. ¿Prefieres otro día? 😊",
				"reason":         "special_holiday",
			})
			return
		}
	}

	if isDateClosed(newDate, closedDays, openedDays) {
		formatted := ""
		if t, err := time.ParseInLocation("2006-01-02", newDate, time.Local); err == nil {
			formatted = t.Format("02/01/2006")
		} else {
			formatted = newDate
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"available":      false,
			"hasAvailability": false,
			"message":        "Ese día (" + formatted + ") estamos cerrados. ¿Qué tal otro día? Abrimos jueves, viernes, sábado y domingo 😊",
			"reason":         "closed_day",
		})
		return
	}

	if tooFar, daysUntil := dateTooFarInFuture(newDate); tooFar {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"available":      false,
			"hasAvailability": false,
			"message":        "Uy, esa fecha está muy lejos todavía (más de 35 días). Solo aceptamos reservas con un máximo de 35 días de antelación. ¿Qué tal una fecha más cercana? 😊",
			"reason":         "too_far_future",
			"daysUntil":      daysUntil,
		})
		return
	}

	if simpleCheck {
		formatted := ""
		if t, err := time.ParseInLocation("2006-01-02", newDate, time.Local); err == nil {
			formatted = t.Format("02/01/2006")
		} else {
			formatted = newDate
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":           true,
			"available":         true,
			"hasAvailability":   true,
			"message":           "El día " + formatted + " está disponible para reservas 😊",
			"reason":            "date_open",
			"simpleCheck":       true,
			"isExplicitlyOpened": openedDays[newDate],
		})
		return
	}

	partySize := *input.PartySize
	availableHours, err := s.computeAvailableHoursForPartySize(r, newDate, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"available": false,
			"message":   "Error al obtener disponibilidad de horarios",
		})
		return
	}

	if len(availableHours) == 0 {
		formatted := ""
		if t, err := time.ParseInLocation("2006-01-02", newDate, time.Local); err == nil {
			formatted = t.Format("02/01/2006")
		} else {
			formatted = newDate
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"available":      false,
			"hasAvailability": false,
			"message":        "Ay, lo siento 😔 Ese día (" + formatted + ") no tengo ninguna mesa libre para " + strconv.Itoa(partySize) + " personas. ¿Te vendría bien otro día?",
			"reason":         "no_hours_available",
			"availableHours": []availableHour{},
		})
		return
	}

	currentTime := strings.TrimSpace(input.CurrentTime)
	if len(currentTime) >= 5 {
		currentTime = currentTime[:5]
	}
	currentTimeAvailable := false
	if currentTime != "" {
		for _, h := range availableHours {
			if h.Time == currentTime {
				currentTimeAvailable = true
				break
			}
		}
	}

	// Cross-validation: daily capacity check for modifications (daily_limits, default 100).
	if input.BookingID != nil {
		dailyLimit := 100
		_ = s.db.QueryRowContext(r.Context(), "SELECT daily_limit FROM daily_limits WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, newDate).Scan(&dailyLimit)
		if dailyLimit <= 0 {
			dailyLimit = 100
		}

		currentTotal := 0
		_ = s.db.QueryRowContext(r.Context(), `
			SELECT COALESCE(SUM(party_size), 0) AS total
			FROM bookings
			WHERE restaurant_id = ?
			  AND reservation_date = ?
			  AND id != ?
			  AND status != 'cancelled'
		`, restaurantID, newDate, *input.BookingID).Scan(&currentTotal)

		if currentTotal+partySize > dailyLimit {
			formatted := ""
			if t, err := time.ParseInLocation("2006-01-02", newDate, time.Local); err == nil {
				formatted = t.Format("02/01/2006")
			} else {
				formatted = newDate
			}
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success":        true,
				"available":      false,
				"hasAvailability": false,
				"message":        "Ay, qué pena 😔 Ese día (" + formatted + ") ya estamos completos para grupos de " + strconv.Itoa(partySize) + " personas. ¿Te viene bien otro día?",
				"reason":         "capacity_exceeded_new_date",
				"dailyLimit":     dailyLimit,
				"currentTotal":   currentTotal,
			})
			return
		}
	}

	formatted := ""
	if t, err := time.ParseInLocation("2006-01-02", newDate, time.Local); err == nil {
		formatted = t.Format("02/01/2006")
	} else {
		formatted = newDate
	}

	if currentTime != "" && currentTimeAvailable {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":            true,
			"available":          true,
			"hasAvailability":    true,
			"currentTimeAvailable": true,
			"message":            "¡Perfecto! 😊 Hay disponibilidad para " + strconv.Itoa(partySize) + " personas el " + formatted + " a las " + currentTime,
			"availableHours":     availableHours,
		})
		return
	}

	hoursFormatted := formatAvailableHours(availableHours)
	message := ""
	if currentTime != "" {
		message = "Esa hora (" + currentTime + ") no está libre ese día 😔 Pero tengo disponible: " + hoursFormatted + ". ¿Cuál te viene mejor?"
	} else {
		message = "Para " + strconv.Itoa(partySize) + " personas el " + formatted + ", tengo disponible: " + hoursFormatted + ". ¿Qué hora prefieres?"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":             true,
		"available":           true,
		"hasAvailability":     true,
		"currentTimeAvailable": false,
		"message":             message,
		"reason":              "current_time_not_available",
		"availableHours":      availableHours,
	})
}

func (s *Server) handleCheckPartySizeAvailability(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ReservationDate  string `json:"reservationDate"`
		CurrentPartySize int    `json:"currentPartySize"`
		NewPartySize     int    `json:"newPartySize"`
		BookingID        int    `json:"bookingId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"available": false,
			"message":   err.Error(),
		})
		return
	}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"available": false,
			"message":   "Unknown restaurant",
		})
		return
	}

	date := strings.TrimSpace(input.ReservationDate)
	if date == "" || input.CurrentPartySize == 0 || input.NewPartySize == 0 || input.BookingID == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   false,
			"available": false,
			"message":   "Missing required parameters",
		})
		return
	}

	if input.NewPartySize > 8 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"available": false,
			"message":   "No se pueden modificar reservas para más de 8 comensales. Máximo permitido: 8 personas.",
		})
		return
	}

	dailyLimit := 100
	_ = s.db.QueryRowContext(r.Context(), "SELECT daily_limit FROM daily_limits WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if dailyLimit <= 0 {
		dailyLimit = 100
	}

	peopleDifference := input.NewPartySize - input.CurrentPartySize

	currentTotal := 0
	_ = s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(SUM(party_size), 0) as total_party_size
		FROM bookings
		WHERE restaurant_id = ?
		  AND reservation_date = ?
		  AND id != ?
		  AND status != 'cancelled'
	`, restaurantID, date, input.BookingID).Scan(&currentTotal)

	newTotal := currentTotal + input.NewPartySize
	available := newTotal <= dailyLimit

	if available {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":          true,
			"available":        true,
			"message":          "Hay disponibilidad para la modificación",
			"currentTotal":     currentTotal,
			"newTotal":         newTotal,
			"dailyLimit":       dailyLimit,
			"peopleDifference": peopleDifference,
			"spotsRemaining":   dailyLimit - newTotal,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"available":      false,
		"message":        "Lo siento, no hay disponibilidad para aumentar a " + strconv.Itoa(input.NewPartySize) + " personas. El límite diario es " + strconv.Itoa(dailyLimit) + " y ya hay " + strconv.Itoa(currentTotal) + " personas reservadas.",
		"currentTotal":   currentTotal,
		"newTotal":       newTotal,
		"dailyLimit":     dailyLimit,
		"spotsRemaining": dailyLimit - currentTotal,
	})
}

func (s *Server) handleValidateBookingModifiable(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BookingID int `json:"bookingId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    err.Error(),
		})
		return
	}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    "Unknown restaurant",
		})
		return
	}

	if input.BookingID == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    "Missing booking ID",
		})
		return
	}

	if err := s.ensureModificationHistoryTable(r.Context()); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    err.Error(),
		})
		return
	}

	var (
		resDate   time.Time
		resTime   time.Time
		status    string
		createdAt time.Time
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT reservation_date, reservation_time, status, created_at
		FROM bookings
		WHERE restaurant_id = ? AND id = ?
	`, restaurantID, input.BookingID).Scan(&resDate, &resTime, &status, &createdAt)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    "Reserva no encontrada",
		})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    err.Error(),
		})
		return
	}

	if status == "cancelled" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"modifiable": false,
			"reason":     "cancelled",
			"message":    "No se puede modificar una reserva cancelada",
		})
		return
	}

	reservationDateTime := time.Date(resDate.Year(), resDate.Month(), resDate.Day(), resTime.Hour(), resTime.Minute(), resTime.Second(), 0, time.Local)
	now := time.Now()

	if reservationDateTime.Before(now) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"modifiable": false,
			"reason":     "past_date",
			"message":    "No se pueden modificar reservas que ya han pasado",
		})
		return
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	resDateMid := time.Date(resDate.Year(), resDate.Month(), resDate.Day(), 0, 0, 0, 0, time.Local)

	if resDateMid.Equal(today) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"modifiable": false,
			"reason":     "same_day",
			"message":    "No se pueden modificar reservas para el mismo día. Por favor, contacta directamente con el restaurante.",
		})
		return
	}

	tomorrow := today.AddDate(0, 0, 1)
	if resDateMid.Equal(tomorrow) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"modifiable": false,
			"reason":     "next_day",
			"message":    "No se pueden modificar reservas para mañana. Por favor, contacta directamente con el restaurante al [TELÉFONO].",
		})
		return
	}

	hoursUntil := int(reservationDateTime.Sub(now).Hours())
	if hoursUntil < 24 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"modifiable": false,
			"reason":     "insufficient_time",
			"message":    "Se requiere al menos 24 horas de antelación para modificar una reserva. Por favor, contacta directamente con el restaurante.",
		})
		return
	}

	var modCount int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) as mod_count FROM modification_history WHERE restaurant_id = ? AND booking_id = ?", restaurantID, input.BookingID).Scan(&modCount); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    false,
			"modifiable": false,
			"message":    err.Error(),
		})
		return
	}

	if modCount >= 3 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":    true,
			"modifiable": false,
			"reason":     "max_modifications",
			"message":    "Has alcanzado el límite máximo de 3 modificaciones para esta reserva. Para más cambios, contacta directamente con el restaurante.",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":               true,
		"modifiable":            true,
		"message":               "Reserva puede ser modificada",
		"modificationsRemaining": 3 - modCount,
		"hoursUntilReservation": hoursUntil,
	})
}

func (s *Server) handleSaveModificationHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BookingID      int    `json:"bookingId"`
		CustomerPhone  string `json:"customerPhone"`
		FieldModified  string `json:"fieldModified"`
		OldValue       string `json:"oldValue"`
		NewValue       string `json:"newValue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if input.BookingID == 0 || strings.TrimSpace(input.FieldModified) == "" || strings.TrimSpace(input.OldValue) == "" || strings.TrimSpace(input.NewValue) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing required parameters",
		})
		return
	}

	if err := s.ensureModificationHistoryTable(r.Context()); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO modification_history
			(restaurant_id, booking_id, customer_phone, field_modified, old_value, new_value, modification_date)
		VALUES (?, ?, ?, ?, ?, ?, NOW())
	`, restaurantID, input.BookingID, strings.TrimSpace(input.CustomerPhone), strings.TrimSpace(input.FieldModified), input.OldValue, input.NewValue)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	id, _ := res.LastInsertId()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "Modification history saved",
		"historyId": id,
	})
}

func (s *Server) handleUpdateReservation(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input struct {
		BookingID int             `json:"bookingId"`
		Field     string          `json:"field"`
		Value     json.RawMessage `json:"value"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid JSON: " + err.Error(),
		})
		return
	}

	field := strings.TrimSpace(input.Field)
	if input.BookingID == 0 || field == "" || len(input.Value) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing required parameters: bookingId, field, value",
		})
		return
	}

	allowed := map[string]bool{
		"reservation_date": true,
		"reservation_time": true,
		"party_size":       true,
		"rice_type":        true,
	}
	if !allowed[field] {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid field name. Allowed: reservation_date, reservation_time, party_size, rice_type",
		})
		return
	}

	column := field
	if field == "rice_type" {
		column = "arroz_type"
	}

	// Use raw JSON bytes for arroz_type (valid JSON required by DB constraint).
	var value any
	switch field {
	case "party_size":
		var n int
		if err := json.Unmarshal(input.Value, &n); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid value for party_size",
			})
			return
		}
		value = n
	case "reservation_date", "reservation_time":
		var sVal string
		if err := json.Unmarshal(input.Value, &sVal); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid value format",
			})
			return
		}
		value = sVal
	case "rice_type":
		if !json.Valid(input.Value) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid value format",
			})
			return
		}
		value = string(input.Value)
	default:
		value = string(input.Value)
	}

	query := "UPDATE bookings SET " + column + " = ? WHERE restaurant_id = ? AND id = ?"
	_, err := s.db.ExecContext(r.Context(), query, value, restaurantID, input.BookingID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Database update failed: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"message":  "Reservation updated successfully",
		"bookingId": input.BookingID,
		"field":    field,
		"value":    json.RawMessage(input.Value),
	})
}

func (s *Server) handleNotifyRestaurantModification(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	var input struct {
		BookingID      int    `json:"bookingId"`
		CustomerName   string `json:"customerName"`
		CustomerPhone  string `json:"customerPhone"`
		FieldModified  string `json:"fieldModified"`
		OldValue       string `json:"oldValue"`
		NewValue       string `json:"newValue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if input.BookingID == 0 || strings.TrimSpace(input.FieldModified) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing required parameters",
		})
		return
	}

	fieldNames := map[string]string{
		"reservation_date": "Fecha",
		"date":             "Fecha",
		"reservation_time": "Hora",
		"hour":             "Hora",
		"party_size":       "Número de personas",
		"people":           "Número de personas",
		"rice_type":        "Tipo de arroz",
		"arroz":            "Tipo de arroz",
	}
	fieldName := fieldNames[input.FieldModified]
	if fieldName == "" {
		fieldName = input.FieldModified
	}

	message := "🔔 MODIFICACIÓN DE RESERVA\n\n"
	message += "Reserva: #" + strconv.Itoa(input.BookingID) + "\n"
	message += "Cliente: " + input.CustomerName + "\n"
	message += "Teléfono: " + input.CustomerPhone + "\n\n"
	message += "Campo modificado: " + fieldName + "\n"
	message += "❌ Antes: " + input.OldValue + "\n"
	message += "✅ Nuevo: " + input.NewValue + "\n\n"
	message += "Fecha modificación: " + time.Now().Format("02/01/2006 15:04")

	notifications := map[string]any{}

	// 1) Email (legacy uses PHP mail(); we don't have SMTP configured here).
	emailTo := s.restaurantFallbackEmail(r.Context(), restaurantID)
	notifications["email"] = map[string]any{
		"sent": false,
		"to":   emailTo,
	}

	// 2) WhatsApp to restaurant recipients (best-effort if configured).
	sendURL, restaurantNumbers := s.uazapiSendTextURL(r.Context(), restaurantID)
	whatsappResults := []map[string]any{}
	whatsSentAny := false
	if sendURL != "" && len(restaurantNumbers) > 0 {
		for _, n := range restaurantNumbers {
			body, code, err := sendUazAPI(r.Context(), sendURL, map[string]any{
				"number": n,
				"text":   message,
			})
			sent := err == nil && (code == 200 || code == 201)
			if sent {
				whatsSentAny = true
			}
			whatsappResults = append(whatsappResults, map[string]any{
				"number": n,
				"sent":   sent,
				"error":  errString(err),
				"http":   code,
				"body":   body,
			})
		}
	} else {
		for _, n := range restaurantNumbers {
			whatsappResults = append(whatsappResults, map[string]any{
				"number": n,
				"sent":   false,
			})
		}
	}

	notifications["whatsapp"] = map[string]any{
		"sent":    whatsSentAny,
		"results": whatsappResults,
	}

	// 3) Log to file (best-effort).
	logPath := filepath.Join("logs", "modifications.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] "
		line += "Booking #" + strconv.Itoa(input.BookingID) + " - " + fieldName + ": " + input.OldValue + " → " + input.NewValue + " by " + input.CustomerPhone + "\n"
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			notifications["log"] = map[string]any{"saved": false, "file": logPath}
		} else {
			_, _ = f.WriteString(line)
			_ = f.Close()
			notifications["log"] = map[string]any{"saved": true, "file": logPath}
		}
	} else {
		notifications["log"] = map[string]any{"saved": false, "file": logPath}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"message":       "Restaurant notified successfully",
		"notifications": notifications,
	})
}
