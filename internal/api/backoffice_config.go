package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

const (
	defaultOpeningMode  = "both"
	defaultDailyLimit   = 45
	defaultMesasLimit   = "999"
	maxRestaurantFloors = 8
)

var defaultMorningHours = []string{
	"08:00", "08:30", "09:00", "09:30",
	"10:00", "10:30", "11:00", "11:30",
	"12:00", "12:30", "13:00", "13:30",
	"14:00", "14:30", "15:00", "15:30",
	"16:00", "16:30",
}

var defaultNightHours = []string{
	"17:30", "18:00", "18:30", "19:00",
	"19:30", "20:00", "20:30", "21:00",
	"21:30", "22:00", "22:30", "23:00",
	"23:30", "00:00", "00:30",
}

var defaultWeekdayOpen = map[string]bool{
	"monday":    false,
	"tuesday":   false,
	"wednesday": false,
	"thursday":  true,
	"friday":    true,
	"saturday":  true,
	"sunday":    true,
}

type reservationDefaults struct {
	OpeningMode      string
	MorningHours     []string
	NightHours       []string
	WeekdayOpen      map[string]bool
	DailyLimit       int
	MesasDeDosLimit  string
	MesasDeTresLimit string
}

type boConfigFloor struct {
	ID          int    `json:"id"`
	FloorNumber int    `json:"floorNumber"`
	Name        string `json:"name"`
	IsGround    bool   `json:"isGround"`
	Active      bool   `json:"active"`
}

func cloneStrings(in []string) []string {
	out := make([]string, 0, len(in))
	out = append(out, in...)
	return out
}

func cloneWeekdayOpen(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range defaultWeekdayOpen {
		out[k] = v
	}
	for k, v := range in {
		if key := normalizeWeekdayKey(k); key != "" {
			out[key] = v
		}
	}
	return out
}

func normalizeOpeningMode(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "morning", "night", "both":
		return v
	default:
		return defaultOpeningMode
	}
}

func normalizeLimitOrFallback(raw string, fallback string) string {
	limit := strings.TrimSpace(raw)
	if limit == "" {
		return fallback
	}
	if strings.EqualFold(limit, "sin_limite") {
		return "999"
	}
	if _, err := strconv.Atoi(limit); err != nil {
		return fallback
	}
	return limit
}

func normalizeLimitInput(raw string) (string, error) {
	limit := strings.TrimSpace(raw)
	if limit == "" {
		return "", badRequest("limit requerido")
	}
	if strings.EqualFold(limit, "sin_limite") {
		limit = "999"
	}
	n, err := strconv.Atoi(limit)
	if err != nil || n < 0 || n > 999 {
		return "", badRequest("limit invalido")
	}
	return strconv.Itoa(n), nil
}

func normalizeHoursList(raw []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		hhmm, err := normalizeHHMM(item)
		if err != nil {
			continue
		}
		if _, err := time.Parse("15:04", hhmm); err != nil {
			continue
		}
		if seen[hhmm] {
			continue
		}
		seen[hhmm] = true
		out = append(out, hhmm)
	}
	sortServiceHours(out)
	return out
}

func parseHoursJSON(raw sql.NullString) ([]string, bool) {
	if !raw.Valid {
		return nil, false
	}
	s := strings.TrimSpace(raw.String)
	if s == "" {
		return nil, false
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, false
	}
	return normalizeHoursList(arr), true
}

func normalizeWeekdayKey(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "monday", "lunes":
		return "monday"
	case "tuesday", "martes":
		return "tuesday"
	case "wednesday", "miercoles", "miércoles":
		return "wednesday"
	case "thursday", "jueves":
		return "thursday"
	case "friday", "viernes":
		return "friday"
	case "saturday", "sabado", "sábado":
		return "saturday"
	case "sunday", "domingo":
		return "sunday"
	default:
		return ""
	}
}

func normalizeWeekdayOpen(raw map[string]bool, fallback map[string]bool) map[string]bool {
	out := cloneWeekdayOpen(fallback)
	for k, v := range raw {
		if key := normalizeWeekdayKey(k); key != "" {
			out[key] = v
		}
	}
	return out
}

func parseWeekdayOpenJSON(raw sql.NullString, fallback map[string]bool) (map[string]bool, bool) {
	if !raw.Valid {
		return nil, false
	}
	s := strings.TrimSpace(raw.String)
	if s == "" {
		return nil, false
	}
	var m map[string]bool
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, false
	}
	return normalizeWeekdayOpen(m, fallback), true
}

func weekdayKeyFromTime(wd time.Weekday) string {
	switch wd {
	case time.Monday:
		return "monday"
	case time.Tuesday:
		return "tuesday"
	case time.Wednesday:
		return "wednesday"
	case time.Thursday:
		return "thursday"
	case time.Friday:
		return "friday"
	case time.Saturday:
		return "saturday"
	case time.Sunday:
		return "sunday"
	default:
		return ""
	}
}

func isWeekdayOpen(date string, weekdayOpen map[string]bool) bool {
	t, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return true
	}
	key := weekdayKeyFromTime(t.Weekday())
	if key == "" {
		return true
	}
	if v, ok := normalizeWeekdayOpen(nil, weekdayOpen)[key]; ok {
		return v
	}
	return true
}

func hhmmToMinutes(hhmm string) (int, bool) {
	if len(hhmm) != 5 || hhmm[2] != ':' {
		return 0, false
	}
	h, errH := strconv.Atoi(hhmm[:2])
	m, errM := strconv.Atoi(hhmm[3:])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func serviceHourSortKey(hhmm string) int {
	minutes, ok := hhmmToMinutes(hhmm)
	if !ok {
		return 0
	}
	// Reservation service-day order: 08:00..23:59 then 00:00..07:59.
	if minutes < 8*60 {
		return minutes + 24*60
	}
	return minutes
}

func sortServiceHours(hours []string) {
	sort.Slice(hours, func(i, j int) bool {
		ki := serviceHourSortKey(hours[i])
		kj := serviceHourSortKey(hours[j])
		if ki == kj {
			return hours[i] < hours[j]
		}
		return ki < kj
	})
}

func splitHoursByShift(all []string) (morning []string, night []string) {
	for _, h := range normalizeHoursList(all) {
		minutes, ok := hhmmToMinutes(h)
		if !ok {
			continue
		}
		if minutes >= 8*60 && minutes <= 17*60 {
			morning = append(morning, h)
			continue
		}
		night = append(night, h)
	}
	sortServiceHours(morning)
	sortServiceHours(night)
	return morning, night
}

func modeFromHours(morning []string, night []string) string {
	hasMorning := len(morning) > 0
	hasNight := len(night) > 0
	if hasMorning && hasNight {
		return "both"
	}
	if hasMorning {
		return "morning"
	}
	if hasNight {
		return "night"
	}
	return defaultOpeningMode
}

func mergeHoursByMode(mode string, morning []string, night []string) []string {
	switch normalizeOpeningMode(mode) {
	case "morning":
		return normalizeHoursList(morning)
	case "night":
		return normalizeHoursList(night)
	default:
		all := make([]string, 0, len(morning)+len(night))
		all = append(all, morning...)
		all = append(all, night...)
		return normalizeHoursList(all)
	}
}

func floorNameForNumber(number int) string {
	if number <= 0 {
		return "Planta baja"
	}
	return fmt.Sprintf("Planta %d", number)
}

func (s *Server) loadReservationDefaults(ctx context.Context, restaurantID int) (reservationDefaults, error) {
	out := reservationDefaults{
		OpeningMode:      defaultOpeningMode,
		MorningHours:     cloneStrings(defaultMorningHours),
		NightHours:       cloneStrings(defaultNightHours),
		WeekdayOpen:      cloneWeekdayOpen(defaultWeekdayOpen),
		DailyLimit:       defaultDailyLimit,
		MesasDeDosLimit:  defaultMesasLimit,
		MesasDeTresLimit: defaultMesasLimit,
	}

	var (
		modeRaw       sql.NullString
		morningRaw    sql.NullString
		nightRaw      sql.NullString
		weekdayRaw    sql.NullString
		dailyLimitRaw sql.NullInt64
		mesas2Raw     sql.NullString
		mesas3Raw     sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT opening_mode, morning_hours_json, night_hours_json, weekday_open_json, daily_limit, mesas_de_dos_limit, mesas_de_tres_limit
		FROM restaurant_reservation_defaults
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&modeRaw, &morningRaw, &nightRaw, &weekdayRaw, &dailyLimitRaw, &mesas2Raw, &mesas3Raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return out, nil
		}
		return out, err
	}

	if modeRaw.Valid {
		out.OpeningMode = normalizeOpeningMode(modeRaw.String)
	}
	if list, ok := parseHoursJSON(morningRaw); ok {
		out.MorningHours = list
	}
	if list, ok := parseHoursJSON(nightRaw); ok {
		out.NightHours = list
	}
	if weekdayOpen, ok := parseWeekdayOpenJSON(weekdayRaw, out.WeekdayOpen); ok {
		out.WeekdayOpen = weekdayOpen
	}
	if dailyLimitRaw.Valid && dailyLimitRaw.Int64 >= 0 && dailyLimitRaw.Int64 <= 500 {
		out.DailyLimit = int(dailyLimitRaw.Int64)
	}
	if mesas2Raw.Valid {
		out.MesasDeDosLimit = normalizeLimitOrFallback(mesas2Raw.String, defaultMesasLimit)
	}
	if mesas3Raw.Valid {
		out.MesasDeTresLimit = normalizeLimitOrFallback(mesas3Raw.String, defaultMesasLimit)
	}
	return out, nil
}

func (s *Server) upsertReservationDefaults(ctx context.Context, restaurantID int, next reservationDefaults) error {
	mode := normalizeOpeningMode(next.OpeningMode)
	morning := normalizeHoursList(next.MorningHours)
	night := normalizeHoursList(next.NightHours)
	dailyLimit := next.DailyLimit
	if dailyLimit < 0 || dailyLimit > 500 {
		dailyLimit = defaultDailyLimit
	}
	mesas2 := normalizeLimitOrFallback(next.MesasDeDosLimit, defaultMesasLimit)
	mesas3 := normalizeLimitOrFallback(next.MesasDeTresLimit, defaultMesasLimit)
	weekdayOpen := normalizeWeekdayOpen(next.WeekdayOpen, defaultWeekdayOpen)

	morningJSON, _ := json.Marshal(morning)
	nightJSON, _ := json.Marshal(night)
	weekdayJSON, _ := json.Marshal(weekdayOpen)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_reservation_defaults (
			restaurant_id, opening_mode, morning_hours_json, night_hours_json, weekday_open_json, daily_limit, mesas_de_dos_limit, mesas_de_tres_limit
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			opening_mode = VALUES(opening_mode),
			morning_hours_json = VALUES(morning_hours_json),
			night_hours_json = VALUES(night_hours_json),
			weekday_open_json = VALUES(weekday_open_json),
			daily_limit = VALUES(daily_limit),
			mesas_de_dos_limit = VALUES(mesas_de_dos_limit),
			mesas_de_tres_limit = VALUES(mesas_de_tres_limit)
	`, restaurantID, mode, string(morningJSON), string(nightJSON), string(weekdayJSON), dailyLimit, mesas2, mesas3)
	return err
}

func (s *Server) loadDefaultFloors(ctx context.Context, restaurantID int) ([]boConfigFloor, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active)
		VALUES (?, 0, 'Planta baja', 1, 1)
		ON DUPLICATE KEY UPDATE
			floor_name = VALUES(floor_name),
			is_ground = VALUES(is_ground),
			is_active = 1
	`, restaurantID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, floor_number, floor_name, is_ground, is_active
		FROM restaurant_floors
		WHERE restaurant_id = ?
		ORDER BY floor_number ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]boConfigFloor, 0, 4)
	for rows.Next() {
		var row boConfigFloor
		var isGroundInt int
		var activeInt int
		if err := rows.Scan(&row.ID, &row.FloorNumber, &row.Name, &isGroundInt, &activeInt); err != nil {
			return nil, err
		}
		row.IsGround = isGroundInt != 0
		row.Active = activeInt != 0
		if row.IsGround {
			row.Active = true
		}
		if strings.TrimSpace(row.Name) == "" {
			row.Name = floorNameForNumber(row.FloorNumber)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Server) ensureFloorCount(ctx context.Context, restaurantID int, count int) error {
	if count < 1 || count > maxRestaurantFloors {
		return badRequest("count invalido")
	}

	for i := 0; i < count; i++ {
		name := floorNameForNumber(i)
		isGround := 0
		if i == 0 {
			isGround = 1
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active)
			VALUES (?, ?, ?, ?, 1)
			ON DUPLICATE KEY UPDATE
				floor_name = VALUES(floor_name),
				is_ground = VALUES(is_ground),
				is_active = IF(VALUES(floor_number) = 0, 1, is_active)
		`, restaurantID, i, name, isGround); err != nil {
			return err
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM restaurant_floors
		WHERE restaurant_id = ? AND floor_number >= ?
	`, restaurantID, count)
	if err != nil {
		return err
	}
	defer rows.Close()

	var removeIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return err
		}
		removeIDs = append(removeIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, floorID := range removeIDs {
		if _, err := s.db.ExecContext(ctx, `
			DELETE FROM restaurant_floor_overrides
			WHERE restaurant_id = ? AND floor_id = ?
		`, restaurantID, floorID); err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, `
		DELETE FROM restaurant_floors
		WHERE restaurant_id = ? AND floor_number >= ?
	`, restaurantID, count)
	return err
}

func (s *Server) loadDateFloors(ctx context.Context, restaurantID int, date string) ([]boConfigFloor, error) {
	floors, err := s.loadDefaultFloors(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	if len(floors) == 0 {
		return floors, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT floor_id, is_active
		FROM restaurant_floor_overrides
		WHERE restaurant_id = ? AND date = ?
	`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	override := map[int]bool{}
	for rows.Next() {
		var floorID int
		var activeInt int
		if err := rows.Scan(&floorID, &activeInt); err != nil {
			return nil, err
		}
		override[floorID] = activeInt != 0
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range floors {
		if floors[i].IsGround {
			floors[i].Active = true
			continue
		}
		if v, ok := override[floors[i].ID]; ok {
			floors[i].Active = v
		}
	}
	return floors, nil
}

type boConfigDayRequest struct {
	Date   string `json:"date"`
	IsOpen bool   `json:"isOpen"`
}

func (s *Server) handleBOConfigDayGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	var isOpenInt sql.NullInt64
	err = s.db.QueryRowContext(r.Context(), `
		SELECT is_open
		FROM restaurant_days
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&isOpenInt)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando restaurant_days")
		return
	}

	isOpen := isWeekdayOpen(date, defaults.WeekdayOpen)
	if isOpenInt.Valid {
		isOpen = isOpenInt.Int64 != 0
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"isOpen":  isOpen,
	})
}

func (s *Server) handleBOConfigDaySet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigDayRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	isOpenInt := 0
	if req.IsOpen {
		isOpenInt = 1
	}

	// Upsert by (restaurant_id, date).
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO restaurant_days (restaurant_id, date, is_open)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE is_open = VALUES(is_open)
	`, a.ActiveRestaurantID, date, isOpenInt)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando restaurant_days")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"isOpen":  req.IsOpen,
	})
}

type boConfigOpeningHoursRequest struct {
	Date         string   `json:"date"`
	Hours        []string `json:"hours"`
	MorningHours []string `json:"morningHours"`
	NightHours   []string `json:"nightHours"`
	OpeningMode  string   `json:"openingMode"`
}

func (s *Server) handleBOConfigOpeningHoursGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	var hoursRaw sql.NullString
	err = s.db.QueryRowContext(r.Context(), `
		SELECT hoursarray
		FROM openinghours
		WHERE restaurant_id = ? AND dateselected = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&hoursRaw)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando openinghours")
		return
	}

	source := "default"
	openingMode := defaults.OpeningMode
	morningHours := cloneStrings(defaults.MorningHours)
	nightHours := cloneStrings(defaults.NightHours)
	hours := mergeHoursByMode(openingMode, morningHours, nightHours)

	if list, ok := parseHoursJSON(hoursRaw); ok {
		source = "override"
		morningHours, nightHours = splitHoursByShift(list)
		openingMode = modeFromHours(morningHours, nightHours)
		hours = mergeHoursByMode(openingMode, morningHours, nightHours)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"date":         date,
		"openingMode":  openingMode,
		"morningHours": morningHours,
		"nightHours":   nightHours,
		"hours":        hours,
		"source":       source,
	})
}

func (s *Server) handleBOConfigOpeningHoursSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigOpeningHoursRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}
	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	openingMode := normalizeOpeningMode(req.OpeningMode)
	morningHours := normalizeHoursList(req.MorningHours)
	nightHours := normalizeHoursList(req.NightHours)
	if len(req.MorningHours) == 0 && len(req.NightHours) == 0 {
		all := normalizeHoursList(req.Hours)
		morningHours, nightHours = splitHoursByShift(all)
		if strings.TrimSpace(req.OpeningMode) == "" {
			openingMode = modeFromHours(morningHours, nightHours)
		}
	}
	normalized := mergeHoursByMode(openingMode, morningHours, nightHours)
	hoursJSON, _ := json.Marshal(normalized)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO openinghours (restaurant_id, dateselected, hoursarray)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE hoursarray = VALUES(hoursarray)
	`, a.ActiveRestaurantID, date, string(hoursJSON))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando openinghours")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"date":         date,
		"openingMode":  openingMode,
		"morningHours": morningHours,
		"nightHours":   nightHours,
		"hours":        normalized,
		"source":       "override",
	})
}

type boConfigMesasDeDosRequest struct {
	Date  string `json:"date"`
	Limit string `json:"limit"`
}

func (s *Server) handleBOConfigMesasDeDosGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	var limit sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT dailyLimit
		FROM mesas_de_dos
		WHERE restaurant_id = ? AND reservationDate = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&limit)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando mesas_de_dos")
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}
	source := "default"
	outLimit := defaults.MesasDeDosLimit
	if limit.Valid {
		outLimit = normalizeLimitOrFallback(limit.String, defaults.MesasDeDosLimit)
		source = "override"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"limit":   outLimit,
		"source":  source,
	})
}

func (s *Server) handleBOConfigMesasDeDosSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigMesasDeDosRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	date := strings.TrimSpace(req.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	limit, err := normalizeLimitInput(req.Limit)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "limit invalido",
		})
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO mesas_de_dos (restaurant_id, reservationDate, dailyLimit)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE dailyLimit = VALUES(dailyLimit)
	`, a.ActiveRestaurantID, date, limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando mesas_de_dos")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"limit":   limit,
		"source":  "override",
	})
}

type boConfigMesasDeTresRequest struct {
	Date  string `json:"date"`
	Limit string `json:"limit"`
}

func (s *Server) handleBOConfigMesasDeTresGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	var limit sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT dailyLimit
		FROM mesas_de_tres
		WHERE restaurant_id = ? AND reservationDate = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&limit)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando mesas_de_tres")
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}
	source := "default"
	outLimit := defaults.MesasDeTresLimit
	if limit.Valid {
		outLimit = normalizeLimitOrFallback(limit.String, defaults.MesasDeTresLimit)
		source = "override"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"limit":   outLimit,
		"source":  source,
	})
}

func (s *Server) handleBOConfigMesasDeTresSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigMesasDeTresRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	date := strings.TrimSpace(req.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	limit, err := normalizeLimitInput(req.Limit)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "limit invalido",
		})
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO mesas_de_tres (restaurant_id, reservationDate, dailyLimit)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE dailyLimit = VALUES(dailyLimit)
	`, a.ActiveRestaurantID, date, limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando mesas_de_tres")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"limit":   limit,
		"source":  "override",
	})
}

type boConfigSalonCondesaRequest struct {
	Date  string `json:"date"`
	State bool   `json:"state"`
}

func (s *Server) handleBOConfigSalonCondesaGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	var state sql.NullInt64
	err := s.db.QueryRowContext(r.Context(), `
		SELECT state
		FROM salon_condesa
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&state)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando salon_condesa")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"state":   state.Valid && state.Int64 != 0,
	})
}

func (s *Server) handleBOConfigSalonCondesaSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigSalonCondesaRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	date := strings.TrimSpace(req.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	stateInt := 0
	if req.State {
		stateInt = 1
	}

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO salon_condesa (restaurant_id, date, state)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE state = VALUES(state)
	`, a.ActiveRestaurantID, date, stateInt)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando salon_condesa")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"state":   req.State,
	})
}

type boConfigDailyLimitRequest struct {
	Date  string `json:"date"`
	Limit int    `json:"limit"`
}

func (s *Server) handleBOConfigDailyLimitGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID
	defaults, err := s.loadReservationDefaults(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	// Best-effort daily limit (reservation_manager schema is legacy).
	var dailyLimit sql.NullInt64
	_ = s.db.QueryRowContext(r.Context(), `
		SELECT dailyLimit
		FROM reservation_manager
		WHERE restaurant_id = ? AND reservationDate = ?
		ORDER BY id DESC
		LIMIT 1
	`, restaurantID, date).Scan(&dailyLimit)

	source := "default"
	limit := int64(defaults.DailyLimit)
	if dailyLimit.Valid {
		limit = dailyLimit.Int64
		source = "override"
	}

	var totalPeople int64
	_ = s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(SUM(party_size), 0)
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ?
	`, restaurantID, date).Scan(&totalPeople)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"date":             date,
		"limit":            limit,
		"totalPeople":      totalPeople,
		"freeBookingSeats": limit - totalPeople,
		"source":           source,
	})
}

func (s *Server) handleBOConfigDailyLimitSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigDailyLimitRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}
	if req.Limit < 0 || req.Limit > 500 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid limit",
		})
		return
	}

	// Keep a single row per (restaurant_id, date): reservation_manager lacks a unique key in dumps.
	restaurantID := a.ActiveRestaurantID
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ?", restaurantID, date)
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO reservation_manager (restaurant_id, reservationDate, dailyLimit)
		VALUES (?, ?, ?)
	`, restaurantID, date, req.Limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando reservation_manager")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"limit":   req.Limit,
	})
}

type boConfigDefaultsSetRequest struct {
	OpeningMode      *string          `json:"openingMode,omitempty"`
	MorningHours     *[]string        `json:"morningHours,omitempty"`
	NightHours       *[]string        `json:"nightHours,omitempty"`
	WeekdayOpen      *map[string]bool `json:"weekdayOpen,omitempty"`
	DailyLimit       *int             `json:"dailyLimit,omitempty"`
	MesasDeDosLimit  *string          `json:"mesasDeDosLimit,omitempty"`
	MesasDeTresLimit *string          `json:"mesasDeTresLimit,omitempty"`
}

func (s *Server) handleBOConfigDefaultsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}
	hours := mergeHoursByMode(defaults.OpeningMode, defaults.MorningHours, defaults.NightHours)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"openingMode":      defaults.OpeningMode,
		"morningHours":     defaults.MorningHours,
		"nightHours":       defaults.NightHours,
		"weekdayOpen":      defaults.WeekdayOpen,
		"hours":            hours,
		"dailyLimit":       defaults.DailyLimit,
		"mesasDeDosLimit":  defaults.MesasDeDosLimit,
		"mesasDeTresLimit": defaults.MesasDeTresLimit,
	})
}

func (s *Server) handleBOConfigDefaultsSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigDefaultsSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	current, err := s.loadReservationDefaults(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	if req.OpeningMode != nil {
		current.OpeningMode = normalizeOpeningMode(*req.OpeningMode)
	}
	if req.MorningHours != nil {
		current.MorningHours = normalizeHoursList(*req.MorningHours)
	}
	if req.NightHours != nil {
		current.NightHours = normalizeHoursList(*req.NightHours)
	}
	if req.WeekdayOpen != nil {
		current.WeekdayOpen = normalizeWeekdayOpen(*req.WeekdayOpen, current.WeekdayOpen)
	}
	if req.DailyLimit != nil {
		if *req.DailyLimit < 0 || *req.DailyLimit > 500 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid dailyLimit",
			})
			return
		}
		current.DailyLimit = *req.DailyLimit
	}
	if req.MesasDeDosLimit != nil {
		limit, err := normalizeLimitInput(*req.MesasDeDosLimit)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "mesasDeDosLimit invalido",
			})
			return
		}
		current.MesasDeDosLimit = limit
	}
	if req.MesasDeTresLimit != nil {
		limit, err := normalizeLimitInput(*req.MesasDeTresLimit)
		if err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "mesasDeTresLimit invalido",
			})
			return
		}
		current.MesasDeTresLimit = limit
	}

	if err := s.upsertReservationDefaults(r.Context(), a.ActiveRestaurantID, current); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando defaults")
		return
	}

	hours := mergeHoursByMode(current.OpeningMode, current.MorningHours, current.NightHours)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"openingMode":      current.OpeningMode,
		"morningHours":     current.MorningHours,
		"nightHours":       current.NightHours,
		"weekdayOpen":      current.WeekdayOpen,
		"hours":            hours,
		"dailyLimit":       current.DailyLimit,
		"mesasDeDosLimit":  current.MesasDeDosLimit,
		"mesasDeTresLimit": current.MesasDeTresLimit,
	})
}

type boConfigFloorsDefaultsSetRequest struct {
	Count       *int  `json:"count,omitempty"`
	FloorNumber *int  `json:"floorNumber,omitempty"`
	Active      *bool `json:"active,omitempty"`
}

func (s *Server) handleBOConfigFloorsDefaultsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	floors, err := s.loadDefaultFloors(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"floors":  floors,
	})
}

func (s *Server) handleBOConfigFloorsDefaultsSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigFloorsDefaultsSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	if req.Count == nil && (req.FloorNumber == nil || req.Active == nil) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "count o floorNumber/active requerido",
		})
		return
	}

	if req.Count != nil {
		if err := s.ensureFloorCount(r.Context(), a.ActiveRestaurantID, *req.Count); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}

	if req.FloorNumber != nil && req.Active != nil {
		floorNumber := *req.FloorNumber
		if floorNumber < 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "floorNumber invalido",
			})
			return
		}

		floors, err := s.loadDefaultFloors(r.Context(), a.ActiveRestaurantID)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
			return
		}

		var target *boConfigFloor
		for i := range floors {
			if floors[i].FloorNumber == floorNumber {
				target = &floors[i]
				break
			}
		}
		if target == nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "planta no encontrada",
			})
			return
		}

		nextActive := *req.Active
		if target.IsGround {
			nextActive = true
		}
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE restaurant_floors
			SET is_active = ?
			WHERE restaurant_id = ? AND floor_number = ?
		`, boolToInt(nextActive), a.ActiveRestaurantID, floorNumber)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando planta")
			return
		}
	}

	floors, err := s.loadDefaultFloors(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"floors":  floors,
	})
}

type boConfigDayFloorSetRequest struct {
	Date        string `json:"date"`
	FloorNumber int    `json:"floorNumber"`
	Active      bool   `json:"active"`
}

func (s *Server) handleBOConfigFloorsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	floors, err := s.loadDateFloors(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"floors":  floors,
	})
}

func (s *Server) handleBOConfigFloorsSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigDayFloorSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}
	if req.FloorNumber < 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "floorNumber invalido",
		})
		return
	}

	floors, err := s.loadDefaultFloors(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}

	var target *boConfigFloor
	for i := range floors {
		if floors[i].FloorNumber == req.FloorNumber {
			target = &floors[i]
			break
		}
	}
	if target == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "planta no encontrada",
		})
		return
	}

	if target.IsGround {
		_, _ = s.db.ExecContext(r.Context(), `
			DELETE FROM restaurant_floor_overrides
			WHERE restaurant_id = ? AND date = ? AND floor_id = ?
		`, a.ActiveRestaurantID, date, target.ID)
	} else if req.Active == target.Active {
		_, _ = s.db.ExecContext(r.Context(), `
			DELETE FROM restaurant_floor_overrides
			WHERE restaurant_id = ? AND date = ? AND floor_id = ?
		`, a.ActiveRestaurantID, date, target.ID)
	} else {
		_, err = s.db.ExecContext(r.Context(), `
			INSERT INTO restaurant_floor_overrides (restaurant_id, date, floor_id, is_active)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE is_active = VALUES(is_active)
		`, a.ActiveRestaurantID, date, target.ID, boolToInt(req.Active))
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando planta del dia")
			return
		}
	}

	finalFloors, err := s.loadDateFloors(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"floors":  finalFloors,
	})
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
