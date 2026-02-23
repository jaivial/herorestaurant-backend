package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) loadRiceTypes(ctx context.Context, restaurantID int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DESCRIPCION
		FROM FINDE
		WHERE restaurant_id = ? AND TIPO = 'ARROZ' AND (active = 1 OR active IS NULL)
		ORDER BY DESCRIPCION ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0)
	seen := make(map[string]struct{})
	for rows.Next() {
		var desc sql.NullString
		if err := rows.Scan(&desc); err != nil {
			return nil, err
		}
		if !desc.Valid {
			continue
		}
		item := strings.TrimSpace(desc.String)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) handleFetchArroz(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	out, err := s.loadRiceTypes(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error consultando FINDE",
		})
		return
	}

	// Legacy endpoint returns a bare JSON array.
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleReservationsRiceTypes(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	out, err := s.loadRiceTypes(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error consultando tipos de arroz",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"riceTypes": out,
	})
}

func (s *Server) handleUpdateDailyLimit(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid form data")
		return
	}
	date := strings.TrimSpace(r.FormValue("date"))
	dailyLimitStr := strings.TrimSpace(r.FormValue("daily_limit"))

	if date == "" || dailyLimitStr == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date and daily_limit parameters are required",
		})
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}
	dailyLimit, err := strconv.Atoi(dailyLimitStr)
	if err != nil || dailyLimit < 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Daily limit must be a non-negative number",
		})
		return
	}

	// `reservation_manager` can have multiple rows per date in legacy dumps (no unique key).
	// Enforce 1 row per (restaurant_id, date) by replace-style behavior.
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?", restaurantID, date); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error updating daily limit: " + err.Error(),
		})
		return
	}
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO reservation_manager (restaurant_id, reservationDate, dailyLimit)
		VALUES (?, ?, ?)
	`, restaurantID, date, dailyLimit)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error updating daily limit: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "Daily limit updated successfully",
		"date":       date,
		"dailyLimit": dailyLimit,
	})
}

func (s *Server) handleFetchDailyLimit(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date parameter is required",
		})
		return
	}
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date parameter is required",
		})
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	// Single roundtrip (legacy uses subqueries).
	var dailyLimit int
	var totalPeople int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT
			COALESCE((SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1), 45) AS daily_limit,
			COALESCE((SELECT SUM(party_size) FROM bookings WHERE restaurant_id = ? AND reservation_date = ?), 0) AS total_people
	`, restaurantID, date, restaurantID, date).Scan(&dailyLimit, &totalPeople)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error al cargar el límite diario: " + err.Error(),
		})
		return
	}

	freeSeats := dailyLimit - totalPeople
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"date":             date,
		"dailyLimit":       dailyLimit,
		"totalPeople":      totalPeople,
		"freeBookingSeats": freeSeats,
	})
}

func (s *Server) handleSetMesasDeDosLimit(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing required parameters",
		})
		return
	}

	date := strings.TrimSpace(r.FormValue("date"))
	dailyLimit := strings.TrimSpace(r.FormValue("daily_limit"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	if dailyLimit == "sin_limite" {
		dailyLimit = "999"
	}
	if !regexp.MustCompile(`^(?:[0-9]|[12][0-9]|3[0-9]|40|999)$`).MatchString(dailyLimit) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid daily limit value. Allowed values are 0-40 or 999.",
		})
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Please use YYYY-MM-DD",
		})
		return
	}

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO mesas_de_dos (restaurant_id, reservationDate, dailyLimit)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE dailyLimit = VALUES(dailyLimit)
	`, restaurantID, date, dailyLimit)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Database error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Límite de mesas de 2 actualizado correctamente",
	})
}

func (s *Server) handleGetMesasDeDosLimit(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing date parameter",
		})
		return
	}

	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Please use YYYY-MM-DD",
		})
		return
	}

	var dailyLimit sql.NullString
	err := s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM mesas_de_dos WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&dailyLimit)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Database error: " + err.Error(),
		})
		return
	}

	val := "5"
	msg := "No limit set for this date, using default value"
	if err == nil && dailyLimit.Valid {
		val = strings.TrimSpace(dailyLimit.String)
		msg = "Limit retrieved successfully"
	}
	if val == "sin_limite" {
		val = "999"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"daily_limit": val,
		"message":     msg,
	})
}

func (s *Server) handleFetchMesasDeDos(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing date parameter",
		})
		return
	}

	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Missing date parameter",
		})
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Please use YYYY-MM-DD",
		})
		return
	}

	limit := 5
	var limitRaw sql.NullString
	if err := s.db.QueryRowContext(r.Context(), "SELECT dailyLimit FROM mesas_de_dos WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1", restaurantID, date).Scan(&limitRaw); err == nil && limitRaw.Valid {
		v := strings.TrimSpace(limitRaw.String)
		if v == "sin_limite" {
			limit = 999
		} else if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}

	var bookedCount int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) as count FROM bookings WHERE restaurant_id = ? AND reservation_date = ? AND party_size = 2", restaurantID, date).Scan(&bookedCount); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Database error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":              true,
		"disponibilidadDeDos":  bookedCount < limit,
		"limiteMesasDeDos":     limit,
		"mesasDeDosReservadas": bookedCount,
	})
}

func (s *Server) handleCheckDayStatus(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date parameter is required",
		})
		return
	}

	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date parameter is required",
		})
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	dt, _ := time.Parse("2006-01-02", date)
	dayOfWeek := int(dt.Weekday())
	// Go: Sunday=0. Legacy: Monday=1 ... Sunday=7.
	legacyN := dayOfWeek
	if legacyN == 0 {
		legacyN = 7
	}
	weekdayNames := map[int]string{
		1: "Lunes",
		2: "Martes",
		3: "Miércoles",
		4: "Jueves",
		5: "Viernes",
		6: "Sábado",
		7: "Domingo",
	}
	weekday := weekdayNames[legacyN]
	isDefaultClosed := legacyN == 1 || legacyN == 2 || legacyN == 3
	isOpen := !isDefaultClosed

	var isOpenDB sql.NullInt64
	if err := s.db.QueryRowContext(r.Context(), "SELECT is_open FROM restaurant_days WHERE restaurant_id = ? AND date = ? LIMIT 1", restaurantID, date).Scan(&isOpenDB); err == nil && isOpenDB.Valid {
		isOpen = isOpenDB.Int64 != 0
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":               true,
		"date":                  date,
		"weekday":               weekday,
		"is_open":               isOpen,
		"is_default_closed_day": isDefaultClosed,
	})
}

func (s *Server) handleOpenDay(w http.ResponseWriter, r *http.Request) {
	s.handleSetDayOpenFlag(w, r, true)
}

func (s *Server) handleCloseDay(w http.ResponseWriter, r *http.Request) {
	s.handleSetDayOpenFlag(w, r, false)
}

func (s *Server) handleSetDayOpenFlag(w http.ResponseWriter, r *http.Request, isOpen bool) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date parameter is required",
		})
		return
	}
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Date parameter is required",
		})
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	openInt := 0
	if isOpen {
		openInt = 1
	}
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO restaurant_days (restaurant_id, date, is_open)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE is_open = VALUES(is_open)
	`, restaurantID, date, openInt)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error updating day status: " + err.Error(),
		})
		return
	}

	msg := "Day opened successfully"
	if !isOpen {
		msg = "Day closed successfully"
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": msg,
		"date":    date,
		"is_open": isOpen,
	})
}

func (s *Server) loadClosedDayOverrides(ctx context.Context, restaurantID int, fromDate string, toDate string) ([]string, []string, error) {
	sqlQuery := "SELECT date, is_open FROM restaurant_days WHERE restaurant_id = ?"
	args := []any{restaurantID}
	if fromDate != "" && toDate != "" {
		sqlQuery += " AND date BETWEEN ? AND ?"
		args = append(args, fromDate, toDate)
	}
	sqlQuery += " ORDER BY date ASC"

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	closedDays := make([]string, 0)
	openedDays := make([]string, 0)
	for rows.Next() {
		var date string
		var isOpenInt int
		if err := rows.Scan(&date, &isOpenInt); err != nil {
			return nil, nil, err
		}
		if isOpenInt != 0 {
			openedDays = append(openedDays, date)
		} else {
			closedDays = append(closedDays, date)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return closedDays, openedDays, nil
}

func parseClosedDaysRange(fromRaw string, toRaw string, maxDays int) (string, string, error) {
	fromDate := strings.TrimSpace(fromRaw)
	toDate := strings.TrimSpace(toRaw)
	if fromDate == "" || toDate == "" {
		return "", "", errors.New("Los parámetros from y to son obligatorios")
	}
	if !isValidISODate(fromDate) || !isValidISODate(toDate) {
		return "", "", errors.New("Formato de fecha inválido")
	}
	start, _ := time.Parse("2006-01-02", fromDate)
	end, _ := time.Parse("2006-01-02", toDate)
	if end.Before(start) {
		return "", "", errors.New("Rango de fechas inválido")
	}
	days := int(end.Sub(start).Hours()/24) + 1
	if days > maxDays {
		return "", "", errors.New("El rango máximo permitido es de 40 días")
	}
	return fromDate, toDate, nil
}

func (s *Server) handleFetchClosedDays(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	closedDays, openedDays, err := s.loadClosedDayOverrides(r.Context(), restaurantID, "", "")
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error fetching closed days: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"closed_days": closedDays,
		"opened_days": openedDays,
	})
}

func (s *Server) handleReservationsClosedDays(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	fromDate, toDate, err := parseClosedDaysRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"), 40)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	closedDays, openedDays, err := s.loadClosedDayOverrides(r.Context(), restaurantID, fromDate, toDate)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error fetching closed days: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"closed_days": closedDays,
		"opened_days": openedDays,
	})
}

func parseMonthYear(monthRaw string, yearRaw string) (int, int, error) {
	month, _ := strconv.Atoi(strings.TrimSpace(monthRaw))
	year, _ := strconv.Atoi(strings.TrimSpace(yearRaw))
	if month < 1 || month > 12 || year < 2000 || year > 2100 {
		return 0, 0, errors.New("Invalid month or year")
	}
	return month, year, nil
}

func (s *Server) buildMonthAvailability(ctx context.Context, restaurantID int, year int, month int) (map[string]map[string]int, error) {
	firstDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	lastDay := firstDay.AddDate(0, 1, -1)

	firstDayStr := firstDay.Format("2006-01-02")
	lastDayStr := lastDay.Format("2006-01-02")

	dailyLimits := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT reservationDate, MAX(dailyLimit) as dailyLimit
		FROM reservation_manager
		WHERE restaurant_id = ? AND reservationDate BETWEEN ? AND ?
		GROUP BY reservationDate
	`, restaurantID, firstDayStr, lastDayStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var lim int
		if err := rows.Scan(&d, &lim); err != nil {
			return nil, err
		}
		dailyLimits[d] = lim
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	bookings := map[string]int{}
	rows2, err := s.db.QueryContext(ctx, `
		SELECT reservation_date, COALESCE(SUM(party_size), 0) as total_people
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date BETWEEN ? AND ?
		GROUP BY reservation_date
	`, restaurantID, firstDayStr, lastDayStr)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var d string
		var total int
		if err := rows2.Scan(&d, &total); err != nil {
			return nil, err
		}
		bookings[d] = total
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	availability := map[string]map[string]int{}
	defaultLimit := 45
	daysInMonth := lastDay.Day()
	for day := 1; day <= daysInMonth; day++ {
		dateISO := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		lim := defaultLimit
		if v, ok := dailyLimits[dateISO]; ok {
			lim = v
		}
		total := bookings[dateISO]
		availability[dateISO] = map[string]int{
			"dailyLimit":       lim,
			"totalPeople":      total,
			"freeBookingSeats": lim - total,
		}
	}

	return availability, nil
}

func (s *Server) handleFetchMonthAvailability(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Month and year parameters are required",
		})
		return
	}

	month, year, err := parseMonthYear(r.FormValue("month"), r.FormValue("year"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	availability, err := s.buildMonthAvailability(r.Context(), restaurantID, year, month)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error al cargar la disponibilidad mensual: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"month":        month,
		"year":         year,
		"availability": availability,
	})
}

func (s *Server) handleReservationsMonthAvailability(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	month, year, err := parseMonthYear(r.URL.Query().Get("month"), r.URL.Query().Get("year"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	availability, err := s.buildMonthAvailability(r.Context(), restaurantID, year, month)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error al cargar la disponibilidad mensual: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"month":        month,
		"year":         year,
		"availability": availability,
	})
}

func (s *Server) handleFetchOccupancy(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": "Unknown restaurant",
		})
		return
	}

	_ = r.ParseForm()
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	var dailyLimit int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE((SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1), 45) AS daily_limit", restaurantID, date).Scan(&dailyLimit); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error": err.Error(),
		})
		return
	}
	var totalPeople int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COALESCE(SUM(party_size),0) FROM bookings WHERE restaurant_id = ? AND reservation_date = ?", restaurantID, date).Scan(&totalPeople); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error": err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"totalPeople": totalPeople,
		"dailyLimit":  dailyLimit,
		"date":        date,
		"status":      "OK",
	})
}

func isValidISODate(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// Encode any hourData map back to JSON for storage (ensures valid JSON).
func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var errBadRequest = errors.New("bad request")

func badRequest(msg string) error {
	return errors.New(msg)
}

func readJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return errBadRequest
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func normalizeHHMM(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", badRequest("empty time")
	}
	// Accept HH:MM or HH:MM:SS and normalize to HH:MM.
	if len(t) >= 5 {
		return t[:5], nil
	}
	return "", badRequest("invalid time")
}

func ensureHHMMSS(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", badRequest("empty time")
	}
	if len(t) == 5 {
		return t + ":00", nil
	}
	if len(t) == 8 {
		return t, nil
	}
	if len(t) > 8 {
		return t[:8], nil
	}
	return "", badRequest("invalid time")
}

func decodePossibleJSONValue(raw any) any {
	if raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		// Already a decoded type.
		return raw
	}
	if s == "" {
		return nil
	}
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed == "" {
		return nil
	}
	if trimmed[0] != '[' && trimmed[0] != '{' {
		return s
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return out
	}
	return s
}

func validatePhone9Digits(s string) bool {
	digits := regexp.MustCompile(`[^0-9]`).ReplaceAllString(s, "")
	return len(digits) == 9
}
