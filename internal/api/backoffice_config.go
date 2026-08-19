package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	OpeningMode            string
	MorningHours           []string
	NightHours             []string
	WeekdayOpen            map[string]bool
	DailyLimit             int
	MesasDeDosLimit        string
	MesasDeTresLimit       string
	HourSplitEnabled       bool
	DefaultHourPercentages map[string]float64
	AllowFloorReservation  bool
	AllowSalonReservation  bool
}

type boConfigFloor struct {
	ID               int    `json:"id"`
	FloorNumber      int    `json:"floorNumber"`
	Name             string `json:"name"`
	IsGround         bool   `json:"isGround"`
	Active           bool   `json:"active"`
	DateScoped       string `json:"dateScoped,omitempty"`
	MaxAforo         int    `json:"maxAforo"`
	TotalSalonAforo  int    `json:"totalSalonAforo"`
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

func countActiveFloors(floors []boConfigFloor) int {
	count := 0
	for _, floor := range floors {
		if floor.Active {
			count++
		}
	}
	return count
}

func (s *Server) loadReservationDefaults(ctx context.Context, restaurantID int) (reservationDefaults, error) {
	out := reservationDefaults{
		OpeningMode:            defaultOpeningMode,
		MorningHours:           cloneStrings(defaultMorningHours),
		NightHours:             cloneStrings(defaultNightHours),
		WeekdayOpen:            cloneWeekdayOpen(defaultWeekdayOpen),
		DailyLimit:             defaultDailyLimit,
		MesasDeDosLimit:        defaultMesasLimit,
		MesasDeTresLimit:       defaultMesasLimit,
		HourSplitEnabled:       true,
		DefaultHourPercentages: map[string]float64{},
	}

	var (
		modeRaw           sql.NullString
		morningRaw        sql.NullString
		nightRaw          sql.NullString
		weekdayRaw        sql.NullString
		dailyLimitRaw     sql.NullInt64
		mesas2Raw         sql.NullString
		mesas3Raw         sql.NullString
		hourSplitRaw      sql.NullInt64
		defaultPercentRaw sql.NullString
		allowFloorRaw     sql.NullInt64
		allowSalonRaw     sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT opening_mode, morning_hours_json, night_hours_json, weekday_open_json, daily_limit, mesas_de_dos_limit, mesas_de_tres_limit, hour_split_enabled, default_hour_percentages_json, allow_floor_reservation, allow_salon_reservation
		FROM restaurant_reservation_defaults
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&modeRaw, &morningRaw, &nightRaw, &weekdayRaw, &dailyLimitRaw, &mesas2Raw, &mesas3Raw, &hourSplitRaw, &defaultPercentRaw, &allowFloorRaw, &allowSalonRaw)
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
	if hourSplitRaw.Valid {
		out.HourSplitEnabled = hourSplitRaw.Int64 != 0
	}
	if allowFloorRaw.Valid {
		out.AllowFloorReservation = allowFloorRaw.Int64 != 0
	}
	if allowSalonRaw.Valid {
		out.AllowSalonReservation = allowSalonRaw.Int64 != 0
	}
	if defaultPercentRaw.Valid && strings.TrimSpace(defaultPercentRaw.String) != "" {
		var pcts map[string]float64
		if err := json.Unmarshal([]byte(defaultPercentRaw.String), &pcts); err == nil {
			out.DefaultHourPercentages = pcts
		}
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

	var defaultPercentJSON any = nil
	if len(next.DefaultHourPercentages) > 0 {
		b, _ := json.Marshal(next.DefaultHourPercentages)
		defaultPercentJSON = string(b)
	}
	hourSplitFlag := 0
	if next.HourSplitEnabled {
		hourSplitFlag = 1
	}
	allowFloorFlag := boolToInt(next.AllowFloorReservation)
	allowSalonFlag := boolToInt(next.AllowSalonReservation)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_reservation_defaults (
			restaurant_id, opening_mode, morning_hours_json, night_hours_json, weekday_open_json, daily_limit, mesas_de_dos_limit, mesas_de_tres_limit, hour_split_enabled, default_hour_percentages_json, allow_floor_reservation, allow_salon_reservation
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			opening_mode = VALUES(opening_mode),
			morning_hours_json = VALUES(morning_hours_json),
			night_hours_json = VALUES(night_hours_json),
			weekday_open_json = VALUES(weekday_open_json),
			daily_limit = VALUES(daily_limit),
			mesas_de_dos_limit = VALUES(mesas_de_dos_limit),
			mesas_de_tres_limit = VALUES(mesas_de_tres_limit),
			hour_split_enabled = VALUES(hour_split_enabled),
			default_hour_percentages_json = VALUES(default_hour_percentages_json),
			allow_floor_reservation = VALUES(allow_floor_reservation),
			allow_salon_reservation = VALUES(allow_salon_reservation)
	`, restaurantID, mode, string(morningJSON), string(nightJSON), string(weekdayJSON), dailyLimit, mesas2, mesas3, hourSplitFlag, defaultPercentJSON, allowFloorFlag, allowSalonFlag)
	return err
}

func (s *Server) loadDefaultFloors(ctx context.Context, restaurantID int) ([]boConfigFloor, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO restaurant_floors (restaurant_id, floor_number, floor_name, is_ground, is_active)
		VALUES (?, 0, 'Planta baja', 1, 1)
		ON DUPLICATE KEY UPDATE
			floor_name = VALUES(floor_name),
			is_ground = VALUES(is_ground)
	`, restaurantID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, floor_number, floor_name, is_ground, is_active, max_aforo
		FROM restaurant_floors
		WHERE restaurant_id = ? AND specific_date IS NULL
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
		var maxAforo sql.NullInt64
		if err := rows.Scan(&row.ID, &row.FloorNumber, &row.Name, &isGroundInt, &activeInt, &maxAforo); err != nil {
			return nil, err
		}
		row.IsGround = isGroundInt != 0
		row.Active = activeInt != 0
		if maxAforo.Valid {
			row.MaxAforo = int(maxAforo.Int64)
		}
		if strings.TrimSpace(row.Name) == "" {
			row.Name = floorNameForNumber(row.FloorNumber)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach the sum of each floor's salon capacity limits for the UI.
	aforo, err := s.floorSalonAforoSums(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].TotalSalonAforo = aforo[out[i].ID]
	}
	return out, nil
}

// floorSalonAforoSums returns floor_id -> SUM(capacity_limit) of that floor's
// salons (global only, has_capacity_limit=1), used to validate a floor's
// max_aforo against the salons it already owns.
func (s *Server) floorSalonAforoSums(ctx context.Context, restaurantID int) (map[int]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT floor_id, COALESCE(SUM(capacity_limit), 0)
		FROM restaurant_salons
		WHERE restaurant_id = ? AND specific_date IS NULL AND has_capacity_limit = 1
		GROUP BY floor_id
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var floorID, total int
		if err := rows.Scan(&floorID, &total); err != nil {
			return nil, err
		}
		out[floorID] = total
	}
	return out, rows.Err()
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
				is_active = is_active
		`, restaurantID, i, name, isGround); err != nil {
			return err
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM restaurant_floors
		WHERE restaurant_id = ? AND floor_number >= ? AND specific_date IS NULL
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
		WHERE restaurant_id = ? AND floor_number >= ? AND specific_date IS NULL
	`, restaurantID, count)
	return err
}

func (s *Server) loadDateFloors(ctx context.Context, restaurantID int, date string) ([]boConfigFloor, error) {
	floors, err := s.loadDefaultFloors(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	// Date-scoped floors exist only on `+"`date`"+`; a date-scoped floor with
	// the same floor_number shadows the global one on that date.
	dateRows, err := s.db.QueryContext(ctx, `
		SELECT id, floor_number, floor_name, is_ground, is_active, max_aforo
		FROM restaurant_floors
		WHERE restaurant_id = ? AND specific_date = ?
		ORDER BY floor_number ASC
	`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer dateRows.Close()

	scoped := make([]boConfigFloor, 0, 2)
	for dateRows.Next() {
		var row boConfigFloor
		var isGroundInt int
		var activeInt int
		var maxAforo sql.NullInt64
		if err := dateRows.Scan(&row.ID, &row.FloorNumber, &row.Name, &isGroundInt, &activeInt, &maxAforo); err != nil {
			return nil, err
		}
		row.IsGround = isGroundInt != 0
		row.Active = activeInt != 0
		if maxAforo.Valid {
			row.MaxAforo = int(maxAforo.Int64)
		}
		if strings.TrimSpace(row.Name) == "" {
			row.Name = floorNameForNumber(row.FloorNumber)
		}
		row.DateScoped = date
		scoped = append(scoped, row)
	}
	if err := dateRows.Err(); err != nil {
		return nil, err
	}

	if len(scoped) > 0 {
		shadowed := make(map[int]bool, len(scoped))
		for _, row := range scoped {
			shadowed[row.FloorNumber] = true
		}
		merged := make([]boConfigFloor, 0, len(floors)+len(scoped))
		for _, row := range floors {
			if !shadowed[row.FloorNumber] {
				merged = append(merged, row)
			}
		}
		merged = append(merged, scoped...)
		sort.SliceStable(merged, func(i, j int) bool {
			return merged[i].FloorNumber < merged[j].FloorNumber
		})
		floors = merged
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
		if v, ok := override[floors[i].ID]; ok {
			floors[i].Active = v
		}
	}

	// Per-date max_aforo override (NULL column = inherit the floor's value).
	aforoRows, err := s.db.QueryContext(ctx, `
		SELECT floor_id, max_aforo
		FROM restaurant_floor_aforo_overrides
		WHERE restaurant_id = ? AND date = ?
	`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer aforoRows.Close()
	for aforoRows.Next() {
		var floorID int
		var maxAforo sql.NullInt64
		if err := aforoRows.Scan(&floorID, &maxAforo); err != nil {
			return nil, err
		}
		for i := range floors {
			if floors[i].ID == floorID && maxAforo.Valid {
				floors[i].MaxAforo = int(maxAforo.Int64)
				break
			}
		}
	}
	if err := aforoRows.Err(); err != nil {
		return nil, err
	}
	return floors, nil
}

type boConfigDayRequest struct {
	Date       string   `json:"date"`
	Dates      []string `json:"dates"`
	RangeDates bool     `json:"rangeDates"`
	IsOpen     bool     `json:"isOpen"`
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

	isOpenInt := 0
	if req.IsOpen {
		isOpenInt = 1
	}

	// Range mode: apply the same is_open value to every date in one batch upsert.
	if req.RangeDates || len(req.Dates) > 0 {
		// Validate + dedupe while preserving order.
		seen := make(map[string]struct{}, len(req.Dates))
		dates := make([]string, 0, len(req.Dates))
		for _, d := range req.Dates {
			d = strings.TrimSpace(d)
			if d == "" || !isValidISODate(d) {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "Invalid date",
				})
				return
			}
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			dates = append(dates, d)
		}
		if len(dates) == 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Invalid date",
			})
			return
		}

		// Build a single multi-row upsert: VALUES (?,?,?),(?,?,?),...
		placeholders := make([]string, 0, len(dates))
		args := make([]any, 0, len(dates)*3)
		for _, d := range dates {
			placeholders = append(placeholders, "(?, ?, ?)")
			args = append(args, a.ActiveRestaurantID, d, isOpenInt)
		}
		query := "INSERT INTO restaurant_days (restaurant_id, date, is_open) VALUES " +
			strings.Join(placeholders, ", ") +
			" ON DUPLICATE KEY UPDATE is_open = VALUES(is_open)"
		if _, err := s.db.ExecContext(r.Context(), query, args...); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando restaurant_days")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"dates":   dates,
			"isOpen":  req.IsOpen,
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
	var modeRaw sql.NullString
	err = s.db.QueryRowContext(r.Context(), `
		SELECT hoursarray, opening_mode
		FROM openinghours
		WHERE restaurant_id = ? AND dateselected = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&hoursRaw, &modeRaw)
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
		// Use stored opening_mode if available, otherwise derive from hours
		if modeRaw.Valid && modeRaw.String != "" {
			openingMode = normalizeOpeningMode(modeRaw.String)
		} else {
			openingMode = modeFromHours(morningHours, nightHours)
		}
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
		INSERT INTO openinghours (restaurant_id, dateselected, hoursarray, opening_mode)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE hoursarray = VALUES(hoursarray), opening_mode = VALUES(opening_mode)
	`, a.ActiveRestaurantID, date, string(hoursJSON), openingMode)
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
	OpeningMode            *string             `json:"openingMode,omitempty"`
	MorningHours           *[]string           `json:"morningHours,omitempty"`
	NightHours             *[]string           `json:"nightHours,omitempty"`
	WeekdayOpen            *map[string]bool    `json:"weekdayOpen,omitempty"`
	DailyLimit             *int                `json:"dailyLimit,omitempty"`
	MesasDeDosLimit        *string             `json:"mesasDeDosLimit,omitempty"`
	MesasDeTresLimit       *string             `json:"mesasDeTresLimit,omitempty"`
	HourSplitEnabled       *bool               `json:"hourSplitEnabled,omitempty"`
	DefaultHourPercentages *map[string]float64 `json:"defaultHourPercentages,omitempty"`
	AllowFloorReservation  *bool               `json:"allowFloorReservation,omitempty"`
	AllowSalonReservation  *bool               `json:"allowSalonReservation,omitempty"`
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
		"success":                true,
		"openingMode":            defaults.OpeningMode,
		"morningHours":           defaults.MorningHours,
		"nightHours":             defaults.NightHours,
		"weekdayOpen":            defaults.WeekdayOpen,
		"hours":                  hours,
		"dailyLimit":             defaults.DailyLimit,
		"mesasDeDosLimit":        defaults.MesasDeDosLimit,
		"mesasDeTresLimit":       defaults.MesasDeTresLimit,
		"hourSplitEnabled":       defaults.HourSplitEnabled,
		"defaultHourPercentages": defaults.DefaultHourPercentages,
		"allowFloorReservation":  defaults.AllowFloorReservation,
		"allowSalonReservation": defaults.AllowSalonReservation,
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
	if req.HourSplitEnabled != nil {
		current.HourSplitEnabled = *req.HourSplitEnabled
	}
	if req.AllowFloorReservation != nil {
		current.AllowFloorReservation = *req.AllowFloorReservation
	}
	if req.AllowSalonReservation != nil {
		current.AllowSalonReservation = *req.AllowSalonReservation
	}
	if req.DefaultHourPercentages != nil {
		cleaned := make(map[string]float64, len(*req.DefaultHourPercentages))
		for k, v := range *req.DefaultHourPercentages {
			cleaned[k] = roundPct(v)
		}
		current.DefaultHourPercentages = cleaned
	}

	if err := s.upsertReservationDefaults(r.Context(), a.ActiveRestaurantID, current); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando defaults")
		return
	}

	hours := mergeHoursByMode(current.OpeningMode, current.MorningHours, current.NightHours)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":                true,
		"openingMode":            current.OpeningMode,
		"morningHours":           current.MorningHours,
		"nightHours":             current.NightHours,
		"weekdayOpen":            current.WeekdayOpen,
		"hours":                  hours,
		"dailyLimit":             current.DailyLimit,
		"mesasDeDosLimit":        current.MesasDeDosLimit,
		"mesasDeTresLimit":       current.MesasDeTresLimit,
		"hourSplitEnabled":       current.HourSplitEnabled,
		"defaultHourPercentages": current.DefaultHourPercentages,
		"allowFloorReservation":  current.AllowFloorReservation,
		"allowSalonReservation": current.AllowSalonReservation,
	})
}

type boConfigFloorsDefaultsSetRequest struct {
	Count       *int  `json:"count,omitempty"`
	FloorNumber *int  `json:"floorNumber,omitempty"`
	Active      *bool `json:"active,omitempty"`
	MaxAforo    *int  `json:"maxAforo,omitempty"`
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

	if req.Count == nil && (req.FloorNumber == nil || (req.Active == nil && req.MaxAforo == nil)) {
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

	if req.FloorNumber != nil {
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

		// max_aforo: reject a cap below the floor's current committed salon aforo.
		if req.MaxAforo != nil {
			if *req.MaxAforo < 0 {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "aforo invalido",
				})
				return
			}
			if *req.MaxAforo > 0 && target.TotalSalonAforo > *req.MaxAforo {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success":  false,
					"message":  "El aforo de la planta no puede ser menor que la suma de los aforos de sus salones",
					"totalSalonAforo": target.TotalSalonAforo,
				})
				return
			}
			// A capped floor obliges every salon to carry a capacity limit.
			maxAforoVal := sql.NullInt64{}
			if *req.MaxAforo > 0 {
				maxAforoVal = sql.NullInt64{Int64: int64(*req.MaxAforo), Valid: true}
			}
			if _, err := s.db.ExecContext(r.Context(), `
				UPDATE restaurant_floors
				SET max_aforo = ?
				WHERE restaurant_id = ? AND floor_number = ?
			`, maxAforoVal, a.ActiveRestaurantID, floorNumber); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando aforo de planta")
				return
			}
			if *req.MaxAforo > 0 {
				// Obligar límite: los salones ya existentes pasan a tener límite
				// (conservando su capacity_limit, que por defecto es 45).
				if _, err := s.db.ExecContext(r.Context(), `
					UPDATE restaurant_salons
					SET has_capacity_limit = 1
					WHERE restaurant_id = ? AND floor_id = ? AND has_capacity_limit = 0
				`, a.ActiveRestaurantID, target.ID); err != nil {
					httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando salones de la planta")
					return
				}
			}
		}

		if req.Active != nil {
			nextActive := *req.Active
			nextFloors := make([]boConfigFloor, len(floors))
			copy(nextFloors, floors)
			for i := range nextFloors {
				if nextFloors[i].FloorNumber == floorNumber {
					nextFloors[i].Active = nextActive
					break
				}
			}
			if countActiveFloors(nextFloors) == 0 {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "Debe haber al menos una planta activa",
				})
				return
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
			// Disabling a floor disables its salons (global default); re-enabling
			// restores them so the two states never drift apart.
			if _, err := s.db.ExecContext(r.Context(), `
				UPDATE restaurant_salons
				SET is_active = ?
				WHERE restaurant_id = ? AND floor_id = ?
			`, boolToInt(nextActive), a.ActiveRestaurantID, target.ID); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error sincronizando salones de la planta")
				return
			}
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
	MaxAforo    *int   `json:"maxAforo,omitempty"`
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

	nextFloors, err := s.loadDateFloors(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}
	for i := range nextFloors {
		if nextFloors[i].FloorNumber == req.FloorNumber {
			nextFloors[i].Active = req.Active
			break
		}
	}
	if countActiveFloors(nextFloors) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Debe haber al menos una planta activa",
		})
		return
	}

	if req.Active == target.Active {
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

	// Per-date max_aforo override for this floor on this date.
	if req.MaxAforo != nil {
		if *req.MaxAforo < 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "aforo invalido",
			})
			return
		}
		effSalonAforo := target.TotalSalonAforo
		if *req.MaxAforo > 0 && effSalonAforo > *req.MaxAforo {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success":  false,
				"message":  "El aforo de la planta no puede ser menor que la suma de los aforos de sus salones",
				"totalSalonAforo": effSalonAforo,
			})
			return
		}
		var aforoVal sql.NullInt64
		if *req.MaxAforo > 0 {
			aforoVal = sql.NullInt64{Int64: int64(*req.MaxAforo), Valid: true}
		}
		if *req.MaxAforo > 0 {
			_, err = s.db.ExecContext(r.Context(), `
				INSERT INTO restaurant_floor_aforo_overrides (restaurant_id, date, floor_id, max_aforo)
				VALUES (?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE max_aforo = VALUES(max_aforo)
			`, a.ActiveRestaurantID, date, target.ID, aforoVal)
		} else {
			_, err = s.db.ExecContext(r.Context(), `
				DELETE FROM restaurant_floor_aforo_overrides
				WHERE restaurant_id = ? AND date = ? AND floor_id = ?
			`, a.ActiveRestaurantID, date, target.ID)
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando aforo de planta del dia")
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

func normalizeRestaurantWebsiteURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("URL web inválida")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("URL web debe usar http o https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("URL web no puede incluir credenciales, query o fragmento")
	}

	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), nil
}

func websiteHostIsPublic(hostname string) bool {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return false
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return false
		}
	}
	return true
}

func websiteURLRespondsOK(ctx context.Context, websiteURL string) error {
	u, err := url.Parse(websiteURL)
	if err != nil || !websiteHostIsPublic(u.Hostname()) {
		return errors.New("URL web no permitida")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, websiteURL, nil)
	if err != nil {
		return fmt.Errorf("URL web inválida: %w", err)
	}
	req.Header.Set("User-Agent", "MenuStudioAI-WebsiteCheck/1.0")
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || !websiteHostIsPublic(req.URL.Hostname()) || len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo comprobar URL web: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("URL web debe responder HTTP 200 (respondió %d)", resp.StatusCode)
	}
	return nil
}

var validEntityTypes = map[string]bool{
	"autonomo": true,
	"sl":       true,
	"sl_new":   true,
	"sl_micro": true,
	"sa":       true,
}

type boRestaurantInfo struct {
	Direccion            string `json:"direccion"`
	Telefono             string `json:"telefono"`
	Email                string `json:"email"`
	CIF                  string `json:"cif"`
	DireccionFacturacion string `json:"direccionFacturacion"`
	Clasificacion        string `json:"clasificacion"`
	TipoEmpresa          string `json:"tipoEmpresa"`
	Website              string `json:"website"`
	MenuURL              string `json:"menuUrl"`
}

type boRestaurantInfoSetRequest struct {
	Direccion            *string `json:"direccion,omitempty"`
	Telefono             *string `json:"telefono,omitempty"`
	Email                *string `json:"email,omitempty"`
	CIF                  *string `json:"cif,omitempty"`
	DireccionFacturacion *string `json:"direccionFacturacion,omitempty"`
	Clasificacion        *string `json:"clasificacion,omitempty"`
	TipoEmpresa          *string `json:"tipoEmpresa,omitempty"`
	Website              *string `json:"website,omitempty"`
	MenuURL              *string `json:"menuUrl,omitempty"`
}

func (s *Server) loadRestaurantInfo(ctx context.Context, restaurantID int) (boRestaurantInfo, error) {
	out := boRestaurantInfo{Clasificacion: "sociedad", TipoEmpresa: "sl"}
	var (
		direccion            sql.NullString
		telefono             sql.NullString
		email                sql.NullString
		cif                  sql.NullString
		direccionFacturacion sql.NullString
		clasificacion        sql.NullString
		tipoEmpresa          sql.NullString
		website              sql.NullString
		menuURL              sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT direccion, telefono, email, cif, direccion_facturacion, clasificacion, tipo_empresa, website, menu_url
		FROM restaurant_info
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&direccion, &telefono, &email, &cif, &direccionFacturacion, &clasificacion, &tipoEmpresa, &website, &menuURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	out.Direccion = strings.TrimSpace(direccion.String)
	out.Telefono = strings.TrimSpace(telefono.String)
	out.Email = strings.TrimSpace(email.String)
	out.CIF = strings.TrimSpace(cif.String)
	out.DireccionFacturacion = strings.TrimSpace(direccionFacturacion.String)
	if clasificacion.Valid {
		v := strings.TrimSpace(clasificacion.String)
		if v == "persona_fisica" || v == "sociedad" {
			out.Clasificacion = v
		}
	}
	if tipoEmpresa.Valid {
		v := strings.TrimSpace(tipoEmpresa.String)
		if validEntityTypes[v] {
			out.TipoEmpresa = v
		}
	}
	if website.Valid {
		rawWebsite := strings.TrimSpace(website.String)
		if normalized, normalizeErr := normalizeRestaurantWebsiteURL(rawWebsite); normalizeErr == nil {
			out.Website = normalized
		} else {
			out.Website = rawWebsite
		}
	}
	if menuURL.Valid {
		out.MenuURL = strings.TrimSpace(menuURL.String)
	}
	return out, nil
}

type boWebsiteCheckRequest struct {
	Website string `json:"website"`
}

func (s *Server) handleBOWebsiteCheck(w http.ResponseWriter, r *http.Request) {
	var req boWebsiteCheckRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	normalized, err := normalizeRestaurantWebsiteURL(req.Website)
	if err != nil || normalized == "" {
		if err == nil {
			err = errors.New("URL web requerida")
		}
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := websiteURLRespondsOK(r.Context(), normalized); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"website": normalized,
	})
}

func (s *Server) handleBORestaurantInfoGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	info, err := s.loadRestaurantInfo(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando informacion del restaurante")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"restaurantInfo": info,
	})
}

func (s *Server) handleBORestaurantInfoSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boRestaurantInfoSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	current, err := s.loadRestaurantInfo(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando informacion del restaurante")
		return
	}

	if req.Direccion != nil {
		current.Direccion = strings.TrimSpace(*req.Direccion)
	}
	if req.Telefono != nil {
		current.Telefono = strings.TrimSpace(*req.Telefono)
	}
	if req.Email != nil {
		current.Email = strings.TrimSpace(*req.Email)
	}
	if req.CIF != nil {
		current.CIF = strings.TrimSpace(*req.CIF)
	}
	if req.DireccionFacturacion != nil {
		current.DireccionFacturacion = strings.TrimSpace(*req.DireccionFacturacion)
	}
	if req.Clasificacion != nil {
		v := strings.TrimSpace(*req.Clasificacion)
		if v == "persona_fisica" || v == "sociedad" {
			current.Clasificacion = v
		}
	}
	if req.TipoEmpresa != nil {
		v := strings.TrimSpace(*req.TipoEmpresa)
		if validEntityTypes[v] {
			current.TipoEmpresa = v
			// Keep the legacy coarse classification consistent with the entity.
			if v == "autonomo" {
				current.Clasificacion = "persona_fisica"
			} else {
				current.Clasificacion = "sociedad"
			}
		}
	}
	if req.Website != nil {
		normalizedWebsite, normalizeErr := normalizeRestaurantWebsiteURL(*req.Website)
		if normalizeErr != nil {
			httpx.WriteError(w, http.StatusBadRequest, normalizeErr.Error())
			return
		}
		if normalizedWebsite != "" {
			if checkErr := websiteURLRespondsOK(r.Context(), normalizedWebsite); checkErr != nil {
				httpx.WriteError(w, http.StatusBadRequest, checkErr.Error())
				return
			}
		}
		current.Website = normalizedWebsite
	}
	if req.MenuURL != nil {
		current.MenuURL = strings.TrimSpace(*req.MenuURL)
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO restaurant_info (
			restaurant_id, direccion, telefono, email, cif, direccion_facturacion, clasificacion, tipo_empresa, website, menu_url
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			direccion = VALUES(direccion),
			telefono = VALUES(telefono),
			email = VALUES(email),
			cif = VALUES(cif),
			direccion_facturacion = VALUES(direccion_facturacion),
			clasificacion = VALUES(clasificacion),
			tipo_empresa = VALUES(tipo_empresa),
			website = VALUES(website),
			menu_url = VALUES(menu_url)
	`, a.ActiveRestaurantID, current.Direccion, current.Telefono, current.Email, current.CIF, current.DireccionFacturacion, current.Clasificacion, current.TipoEmpresa, current.Website, current.MenuURL)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando informacion del restaurante")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"restaurantInfo": current,
	})
}

// --- Mandatory Menus Config ---

type boMandatoryMenuSaveRequest struct {
	Date           string `json:"date"`
	Status         bool   `json:"status"`
	Mandatory      bool   `json:"mandatory"`
	MenuIDs        []int  `json:"menuIds"`
	MenuChooseMain []int  `json:"menuChooseMain"`
}

func (s *Server) handleBOMandatoryMenusGet(w http.ResponseWriter, r *http.Request) {
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

	var id int
	var statusInt, mandatoryInt int
	var menuIDRaw, menuChooseMainRaw sql.NullString

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, status, mandatory, menu_id, menu_choose_main
		FROM mandatory_menus
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&id, &statusInt, &mandatoryInt, &menuIDRaw, &menuChooseMainRaw)

	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":        true,
			"date":           date,
			"status":         false,
			"mandatory":      false,
			"menuIds":        []int{},
			"menuChooseMain": []int{},
		})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando mandatory_menus")
		return
	}

	var menuIDs []int
	if menuIDRaw.Valid && menuIDRaw.String != "" {
		_ = json.Unmarshal([]byte(menuIDRaw.String), &menuIDs)
	}
	if menuIDs == nil {
		menuIDs = []int{}
	}

	var menuChooseMain []int
	if menuChooseMainRaw.Valid && menuChooseMainRaw.String != "" {
		_ = json.Unmarshal([]byte(menuChooseMainRaw.String), &menuChooseMain)
	}
	if menuChooseMain == nil {
		menuChooseMain = []int{}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"date":           date,
		"status":         statusInt != 0,
		"mandatory":      mandatoryInt != 0,
		"menuIds":        menuIDs,
		"menuChooseMain": menuChooseMain,
	})
}

func (s *Server) handleBOMandatoryMenusSave(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMandatoryMenuSaveRequest
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

	menuIDsJSON, _ := json.Marshal(req.MenuIDs)
	menuChooseMainJSON, _ := json.Marshal(req.MenuChooseMain)

	statusInt := 0
	if req.Status {
		statusInt = 1
	}
	mandatoryInt := 0
	if req.Mandatory {
		mandatoryInt = 1
	}

	// Upsert by (restaurant_id, date)
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO mandatory_menus (restaurant_id, date, status, mandatory, menu_id, menu_choose_main)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			status = VALUES(status),
			mandatory = VALUES(mandatory),
			menu_id = VALUES(menu_id),
			menu_choose_main = VALUES(menu_choose_main)
	`, a.ActiveRestaurantID, date, statusInt, mandatoryInt, string(menuIDsJSON), string(menuChooseMainJSON))

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando mandatory_menus")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"date":           date,
		"status":         req.Status,
		"mandatory":      req.Mandatory,
		"menuIds":        req.MenuIDs,
		"menuChooseMain": req.MenuChooseMain,
	})
}

// handleBOMenuSelectorGet returns menus for the dropdown selector in backoffice.
// Filters out draft menus (is_draft = 0).
func (s *Server) handleBOMenuSelectorGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, menu_title, COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') AS menu_type
		FROM menus
		WHERE restaurant_id = ? AND is_draft = 0 AND active = 1
		ORDER BY menu_type ASC, menu_title ASC
	`, a.ActiveRestaurantID)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando menus")
		return
	}
	defer rows.Close()

	var menus []map[string]any
	for rows.Next() {
		var id int
		var title, menuType string
		if err := rows.Scan(&id, &title, &menuType); err != nil {
			continue
		}
		menus = append(menus, map[string]any{
			"id":         id,
			"menu_title": title,
			"menu_type":  menuType,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"menus":   menus,
	})
}

// --- Email Provider Config ---

type boEmailProviderConfig struct {
	ID               int    `json:"id"`
	Provider         string `json:"provider"`
	SMTPHost         string `json:"smtpHost"`
	SMTPPort         int    `json:"smtpPort"`
	SMTPUsername     string `json:"smtpUsername"`
	SMTPPassword     string `json:"smtpPassword"`
	SMTPFromEmail    string `json:"smtpFromEmail"`
	SMTEncryption    string `json:"smtpEncryption"`
	GmailAppPassword string `json:"gmailAppPassword"`
	GmailFromEmail   string `json:"gmailFromEmail"`
	IsActive         bool   `json:"isActive"`
}

type boEmailProviderSetRequest struct {
	ID               *int    `json:"id,omitempty"`
	Provider         *string `json:"provider,omitempty"`
	SMTPHost         *string `json:"smtpHost,omitempty"`
	SMTPPort         *int    `json:"smtpPort,omitempty"`
	SMTPUsername     *string `json:"smtpUsername,omitempty"`
	SMTPPassword     *string `json:"smtpPassword,omitempty"`
	SMTPFromEmail    *string `json:"smtpFromEmail,omitempty"`
	SMTEncryption    *string `json:"smtpEncryption,omitempty"`
	GmailAppPassword *string `json:"gmailAppPassword,omitempty"`
	GmailFromEmail   *string `json:"gmailFromEmail,omitempty"`
	IsActive         *bool   `json:"isActive,omitempty"`
}

func defaultEmailProviderConfig() boEmailProviderConfig {
	return boEmailProviderConfig{
		Provider:         "smtp",
		SMTPHost:         "",
		SMTPPort:         587,
		SMTPUsername:     "",
		SMTPPassword:     "",
		SMTPFromEmail:    "",
		SMTEncryption:    "tls",
		GmailAppPassword: "",
		GmailFromEmail:   "",
		IsActive:         false,
	}
}

func boolFromInt(v int) bool { return v != 0 }

func (s *Server) loadEmailProviderConfig(ctx context.Context, restaurantID int) (boEmailProviderConfig, error) {
	out := defaultEmailProviderConfig()
	var id, smtpPort, isActiveInt int
	var provider, smtpHost, smtpUsername, smtpPassword, smtpFromEmail, smtpEncryption, gmailAppPassword, gmailFromEmail sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, provider, smtp_host, smtp_port, smtp_username, smtp_password, smtp_from_email, smtp_encryption,
			gmail_app_password, gmail_from_email, is_active
		FROM email_provider_config
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&id, &provider, &smtpHost, &smtpPort, &smtpUsername, &smtpPassword, &smtpFromEmail, &smtpEncryption, &gmailAppPassword, &gmailFromEmail, &isActiveInt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	out.ID = id
	if provider.Valid {
		out.Provider = strings.TrimSpace(provider.String)
	}
	if smtpHost.Valid {
		out.SMTPHost = strings.TrimSpace(smtpHost.String)
	}
	out.SMTPPort = smtpPort
	if smtpUsername.Valid {
		out.SMTPUsername = strings.TrimSpace(smtpUsername.String)
	}
	if smtpPassword.Valid {
		out.SMTPPassword = smtpPassword.String
	}
	if smtpFromEmail.Valid {
		out.SMTPFromEmail = strings.TrimSpace(smtpFromEmail.String)
	}
	if smtpEncryption.Valid {
		out.SMTEncryption = strings.TrimSpace(smtpEncryption.String)
	}
	if gmailAppPassword.Valid {
		out.GmailAppPassword = gmailAppPassword.String
	}
	if gmailFromEmail.Valid {
		out.GmailFromEmail = strings.TrimSpace(gmailFromEmail.String)
	}
	out.IsActive = boolFromInt(isActiveInt)
	return out, nil
}

func checkEmailProviderCompleteness(cfg boEmailProviderConfig) (bool, []string) {
	var missing []string
	switch cfg.Provider {
	case "gmail":
		if strings.TrimSpace(cfg.GmailFromEmail) == "" {
			missing = append(missing, "gmailFromEmail")
		}
		if strings.TrimSpace(cfg.GmailAppPassword) == "" {
			missing = append(missing, "gmailAppPassword")
		}
	default: // smtp
		if strings.TrimSpace(cfg.SMTPHost) == "" {
			missing = append(missing, "smtpHost")
		}
		if strings.TrimSpace(cfg.SMTPUsername) == "" {
			missing = append(missing, "smtpUsername")
		}
		if strings.TrimSpace(cfg.SMTPPassword) == "" {
			missing = append(missing, "smtpPassword")
		}
		if strings.TrimSpace(cfg.SMTPFromEmail) == "" {
			missing = append(missing, "smtpFromEmail")
		}
	}
	if !cfg.IsActive {
		missing = append(missing, "isActive")
	}
	return len(missing) == 0, missing
}

func (s *Server) handleBOEmailProviderGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	cfg, err := s.loadEmailProviderConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuracion de email")
		return
	}

	isComplete, missingFields := checkEmailProviderCompleteness(cfg)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"config":        cfg,
		"isComplete":    isComplete,
		"missingFields": missingFields,
	})
}

func normalizeEncryption(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none", "tls", "ssl":
		return v
	default:
		return "tls"
	}
}

func normalizeProvider(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "smtp", "gmail":
		return v
	default:
		return "smtp"
	}
}

func (s *Server) handleBOEmailProviderSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boEmailProviderSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	current, err := s.loadEmailProviderConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuracion de email")
		return
	}

	if req.Provider != nil {
		current.Provider = normalizeProvider(*req.Provider)
	}
	if req.SMTPHost != nil {
		current.SMTPHost = strings.TrimSpace(*req.SMTPHost)
	}
	if req.SMTPPort != nil {
		port := *req.SMTPPort
		if port <= 0 || port > 65535 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "smtp_port invalido",
			})
			return
		}
		current.SMTPPort = port
	}
	if req.SMTPUsername != nil {
		current.SMTPUsername = strings.TrimSpace(*req.SMTPUsername)
	}
	if req.SMTPPassword != nil {
		current.SMTPPassword = *req.SMTPPassword
	}
	if req.SMTPFromEmail != nil {
		current.SMTPFromEmail = strings.TrimSpace(*req.SMTPFromEmail)
	}
	if req.SMTEncryption != nil {
		current.SMTEncryption = normalizeEncryption(*req.SMTEncryption)
	}
	if req.GmailAppPassword != nil {
		current.GmailAppPassword = *req.GmailAppPassword
	}
	if req.GmailFromEmail != nil {
		current.GmailFromEmail = strings.TrimSpace(*req.GmailFromEmail)
	}
	if req.IsActive != nil {
		current.IsActive = *req.IsActive
	}

	activeInt := 0
	if current.IsActive {
		activeInt = 1
	}

	if current.ID > 0 {
		_, err = s.db.ExecContext(r.Context(), `
			UPDATE email_provider_config SET
				provider = ?, smtp_host = ?, smtp_port = ?, smtp_username = ?, smtp_password = ?,
				smtp_from_email = ?, smtp_encryption = ?, gmail_app_password = ?, gmail_from_email = ?,
				is_active = ?
			WHERE id = ? AND restaurant_id = ?
		`, current.Provider, current.SMTPHost, current.SMTPPort, current.SMTPUsername, current.SMTPPassword,
			current.SMTPFromEmail, current.SMTEncryption, current.GmailAppPassword, current.GmailFromEmail,
			activeInt, current.ID, a.ActiveRestaurantID)
	} else {
		_, err = s.db.ExecContext(r.Context(), `
			INSERT INTO email_provider_config (
				restaurant_id, provider, smtp_host, smtp_port, smtp_username, smtp_password,
				smtp_from_email, smtp_encryption, gmail_app_password, gmail_from_email, is_active
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, a.ActiveRestaurantID, current.Provider, current.SMTPHost, current.SMTPPort, current.SMTPUsername,
			current.SMTPPassword, current.SMTPFromEmail, current.SMTEncryption, current.GmailAppPassword,
			current.GmailFromEmail, activeInt)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando configuracion de email")
		return
	}

	// Reload to get the stored values.
	stored, err := s.loadEmailProviderConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo configuracion guardada")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"config":  stored,
	})
}
