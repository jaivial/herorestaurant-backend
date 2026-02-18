package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"preactvillacarmen/internal/httpx"
)

type calendarCacheEntry struct {
	payload   []byte
	etag      string
	expiresAt time.Time
}

var (
	calendarCacheMu sync.Mutex
	calendarCache   = map[string]calendarCacheEntry{}
)

func (s *Server) handleGetCalendarData(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"error": "Unknown restaurant",
		})
		return
	}

	now := time.Now()

	month := int(now.Month())
	if raw := strings.TrimSpace(r.URL.Query().Get("month")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			month = n
		}
	}

	year := now.Year()
	if raw := strings.TrimSpace(r.URL.Query().Get("year")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			year = n
		}
	}

	if month < 1 || month > 12 || year < 2000 || year > 2100 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error": "Invalid month or year",
		})
		return
	}

	cacheKey := strconv.Itoa(restaurantID) + ":" + strconv.Itoa(year) + "-" + strconv.Itoa(month)
	calendarCacheMu.Lock()
	if entry, ok := calendarCache[cacheKey]; ok && now.Before(entry.expiresAt) {
		calendarCacheMu.Unlock()
		w.Header().Set("ETag", entry.etag)
		if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm == entry.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(entry.payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(entry.payload)
		return
	}
	calendarCacheMu.Unlock()

	firstDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	lastDay := firstDay.AddDate(0, 1, -1)
	firstDayStr := firstDay.Format("2006-01-02")
	lastDayStr := lastDay.Format("2006-01-02")

	type calendarDay struct {
		Date         string `json:"date"`
		BookingCount int    `json:"booking_count"`
		TotalPeople  int    `json:"total_people"`
		Limit        int    `json:"limit"`
		IsOpen       bool   `json:"is_open"`
	}

	defaultLimit := 45
	totalDays := lastDay.Day()

	calendarData := make(map[string]calendarDay, totalDays)
	for day := 1; day <= totalDays; day++ {
		d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
		dateStr := d.Format("2006-01-02")
		weekday := int(d.Weekday()) // 0=Sunday .. 6=Saturday
		isClosedByDefault := weekday == 1 || weekday == 2 || weekday == 3
		calendarData[dateStr] = calendarDay{
			Date:         dateStr,
			BookingCount: 0,
			TotalPeople:  0,
			Limit:        defaultLimit,
			IsOpen:       !isClosedByDefault,
		}
	}

	// Bookings by day (excluding cancelled).
	bookingRows, err := s.db.QueryContext(r.Context(), `
		SELECT reservation_date, COUNT(*) AS booking_count, COALESCE(SUM(party_size), 0) AS total_people
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date BETWEEN ? AND ?
		  AND status != 'cancelled'
		GROUP BY reservation_date
	`, restaurantID, firstDayStr, lastDayStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}
	defer bookingRows.Close()

	for bookingRows.Next() {
		var d time.Time
		var bookingCount int
		var totalPeople int
		if err := bookingRows.Scan(&d, &bookingCount, &totalPeople); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"error":   "Database error",
				"message": err.Error(),
			})
			return
		}
		dateStr := d.Format("2006-01-02")
		if day, ok := calendarData[dateStr]; ok {
			day.BookingCount = bookingCount
			day.TotalPeople = totalPeople
			calendarData[dateStr] = day
		}
	}

	// Daily limits.
	limitRows, err := s.db.QueryContext(r.Context(), `
		SELECT reservationDate AS date, dailyLimit AS reservation_limit
		FROM reservation_manager
		WHERE restaurant_id = ? AND reservationDate BETWEEN ? AND ?
	`, restaurantID, firstDayStr, lastDayStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}
	defer limitRows.Close()

	for limitRows.Next() {
		var d time.Time
		var limit sql.NullInt64
		if err := limitRows.Scan(&d, &limit); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"error":   "Database error",
				"message": err.Error(),
			})
			return
		}
		dateStr := d.Format("2006-01-02")
		if day, ok := calendarData[dateStr]; ok && limit.Valid {
			day.Limit = int(limit.Int64)
			calendarData[dateStr] = day
		}
	}

	// Open/closed overrides.
	daysRows, err := s.db.QueryContext(r.Context(), `
		SELECT date, is_open
		FROM restaurant_days
		WHERE restaurant_id = ? AND date BETWEEN ? AND ?
	`, restaurantID, firstDayStr, lastDayStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"error":   "Database error",
			"message": err.Error(),
		})
		return
	}
	defer daysRows.Close()

	for daysRows.Next() {
		var d time.Time
		var isOpen bool
		if err := daysRows.Scan(&d, &isOpen); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"error":   "Database error",
				"message": err.Error(),
			})
			return
		}
		dateStr := d.Format("2006-01-02")
		if day, ok := calendarData[dateStr]; ok {
			day.IsOpen = isOpen
			calendarData[dateStr] = day
		}
	}

	// Preserve legacy ordering (day 1..N).
	result := make([]calendarDay, 0, totalDays)
	for day := 1; day <= totalDays; day++ {
		d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
		dateStr := d.Format("2006-01-02")
		if v, ok := calendarData[dateStr]; ok {
			result = append(result, v)
		}
	}

	response := map[string]any{
		"success": true,
		"data":    result,
	}
	payload, _ := json.Marshal(response)
	etag := `"` + md5Hex(payload) + `"`

	calendarCacheMu.Lock()
	calendarCache[cacheKey] = calendarCacheEntry{
		payload:   payload,
		etag:      etag,
		expiresAt: now.Add(20 * time.Second),
	}
	calendarCacheMu.Unlock()

	w.Header().Set("ETag", etag)
	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
