package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// rebalancePercentages returns a new map where changedHour is set to newValue and
// every other hour is rescaled so the total sums to exactly 100.
//
// Rounding: remaining budget is distributed proportionally to each other hour's
// previous weight; the largest remainder absorbs the sub-cent drift so the final
// sum is guaranteed to be 100.00.
func rebalancePercentages(percentages map[string]float64, changedHour string, newValue float64) map[string]float64 {
	out := make(map[string]float64, len(percentages))
	for k, v := range percentages {
		out[k] = v
	}

	if _, ok := out[changedHour]; !ok {
		return out
	}

	newValue = clampPct(newValue)
	out[changedHour] = newValue

	remaining := 100.0 - newValue
	if remaining < 0 {
		remaining = 0
	}

	others := make([]string, 0, len(out)-1)
	othersSum := 0.0
	for k, v := range out {
		if k == changedHour {
			continue
		}
		others = append(others, k)
		othersSum += v
	}

	if len(others) == 0 {
		// Only one hour: it must be 100.
		out[changedHour] = 100
		return out
	}

	if othersSum <= 0 {
		// No prior weights: distribute equally.
		each := roundPct(remaining / float64(len(others)))
		for _, k := range others {
			out[k] = each
		}
		// Absorb drift on the first sorted hour.
		sortedOthers := append([]string(nil), others...)
		sort.Strings(sortedOthers)
		othersTotal := 0.0
		for _, k := range sortedOthers {
			othersTotal += out[k]
		}
		drift := roundPct(remaining) - roundPct(othersTotal)
		if drift != 0 && len(sortedOthers) > 0 {
			out[sortedOthers[0]] = roundPct(out[sortedOthers[0]] + drift)
		}
		return out
	}

	// Scale others proportionally.
	scaled := make(map[string]float64, len(others))
	rawSum := 0.0
	for _, k := range others {
		raw := remaining * (out[k] / othersSum)
		scaled[k] = raw
		rawSum += raw
	}
	// Round each, then fix drift on the hour with the largest rounding remainder.
	rounded := make(map[string]float64, len(others))
	floors := make(map[string]float64, len(others))
	roundedSum := 0.0
	for _, k := range others {
		floor := math.Floor(scaled[k]*100) / 100
		floors[k] = floor
		rounded[k] = floor
		roundedSum += floor
	}

	target := roundPct(remaining)
	drift := roundTo(target - roundedSum)
	if drift != 0 {
		// Assign drift (in 0.01 steps) to hours with the largest fractional remainder.
		type rem struct {
			k    string
			frac float64
		}
		rems := make([]rem, 0, len(others))
		for _, k := range others {
			frac := scaled[k] - floors[k]
			rems = append(rems, rem{k: k, frac: frac})
		}
		sort.SliceStable(rems, func(i, j int) bool {
			if rems[i].frac == rems[j].frac {
				return rems[i].k < rems[j].k
			}
			return rems[i].frac > rems[j].frac
		})
		steps := int(math.Round(drift * 100))
		if steps > len(rems) {
			steps = len(rems)
		}
		if steps < -len(rems) {
			steps = -len(rems)
		}
		for i := 0; i < absInt(steps); i++ {
			idx := i
			if steps < 0 {
				idx = len(rems) - 1 - i
			}
			k := rems[idx].k
			rounded[k] = roundTo(rounded[k] + 0.01)
		}
	}
	for _, k := range others {
		out[k] = rounded[k]
	}

	return out
}

// percentagesToPeople derives people counts from percentages and a daily limit.
// People is always derived (decision #2): round(pct/100*limit).
func percentagesToPeople(percentages map[string]float64, dailyLimit int) map[string]int {
	out := make(map[string]int, len(percentages))
	for hour, pct := range percentages {
		out[hour] = int(math.Round((pct / 100.0) * float64(dailyLimit)))
	}
	return out
}

// peopleToPercentage converts a people count into a percentage for a given daily limit.
func peopleToPercentage(people int, dailyLimit int) float64 {
	if dailyLimit <= 0 {
		return 0
	}
	return roundPct((float64(people) / float64(dailyLimit)) * 100.0)
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func roundPct(v float64) float64 {
	return math.Round(v*100) / 100
}

func roundTo(v float64) float64 {
	return math.Round(v*100) / 100
}

func sumPct(m map[string]float64) float64 {
	total := 0.0
	for _, v := range m {
		total += v
	}
	return roundPct(total)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// normalizePercentages ensures every active hour is present (equal split when empty)
// and drops entries for hours no longer active. Returns the normalized map and whether
// it was synthesized from defaults (no stored config).
func normalizePercentages(stored map[string]float64, activeHours []string) (map[string]float64, bool) {
	if len(activeHours) == 0 {
		return map[string]float64{}, false
	}
	synthesized := len(stored) == 0
	out := make(map[string]float64, len(activeHours))
	if synthesized {
		each := roundPct(100.0 / float64(len(activeHours)))
		for _, h := range activeHours {
			out[h] = each
		}
		// Absorb rounding drift on the first hour so the total is exactly 100.
		sortedHours := append([]string(nil), activeHours...)
		sort.Strings(sortedHours)
		drift := roundPct(100.0 - sumPct(out))
		if drift != 0 && len(sortedHours) > 0 {
			out[sortedHours[0]] = roundPct(out[sortedHours[0]] + drift)
		}
		return out, true
	}
	for _, h := range activeHours {
		v, ok := stored[h]
		if !ok {
			v = 0
		}
		out[h] = roundPct(v)
	}
	return out, false
}

// validatePercentagesSum returns true when the map sums to 100 (±0.1 tolerance).
func validatePercentagesSum(percentages map[string]float64) bool {
	total := 0.0
	for _, v := range percentages {
		total += v
	}
	return math.Abs(total-100.0) <= 0.1
}

// resolveHourSplitEnabled returns the effective flag for a date: the per-date
// override when present, otherwise the restaurant default, otherwise true.
func (s *Server) resolveHourSplitEnabled(ctx context.Context, restaurantID int, date string) (bool, string, error) {
	var enabled int
	err := s.db.QueryRowContext(ctx,
		"SELECT enabled FROM hour_split_override WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1",
		restaurantID, date,
	).Scan(&enabled)
	if err == nil {
		return enabled != 0, "override", nil
	}
	if err != sql.ErrNoRows {
		return true, "default", err
	}

	defaults, err := s.loadReservationDefaults(ctx, restaurantID)
	if err != nil {
		return true, "default", err
	}
	return defaults.HourSplitEnabled, "default", nil
}

// loadHourPercentages reads the stored percentages for a date from hours_percentage,
// normalized against the active hours. When no row exists an equal split is synthesized.
func (s *Server) loadHourPercentages(ctx context.Context, restaurantID int, date string, activeHours []string) (map[string]float64, bool, error) {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT hoursPercentages FROM hours_percentage WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1",
		restaurantID, date,
	).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return nil, false, err
	}

	stored := map[string]float64{}
	if err == nil && raw.Valid && strings.TrimSpace(raw.String) != "" {
		if uerr := json.Unmarshal([]byte(raw.String), &stored); uerr != nil {
			stored = map[string]float64{}
		}
	}

	normalized, synthesized := normalizePercentages(stored, activeHours)
	return normalized, synthesized, nil
}

func (s *Server) saveHourPercentages(ctx context.Context, restaurantID int, date string, percentages map[string]float64) error {
	payload, _ := json.Marshal(percentages)

	var exists int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM hours_percentage WHERE restaurant_id = ? AND reservationDate = ? LIMIT 1",
		restaurantID, date,
	).Scan(&exists)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		_, err = s.db.ExecContext(ctx,
			"INSERT INTO hours_percentage (restaurant_id, reservationDate, hoursPercentages) VALUES (?, ?, ?)",
			restaurantID, date, string(payload),
		)
		return err
	}
	_, err = s.db.ExecContext(ctx,
		"UPDATE hours_percentage SET hoursPercentages = ? WHERE restaurant_id = ? AND reservationDate = ?",
		string(payload), restaurantID, date,
	)
	return err
}

func (s *Server) setHourSplitOverride(ctx context.Context, restaurantID int, date string, enabled bool) error {
	flag := 0
	if enabled {
		flag = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hour_split_override (restaurant_id, reservationDate, enabled)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE enabled = VALUES(enabled)
	`, restaurantID, date, flag)
	return err
}

func (s *Server) clearHourSplitOverride(ctx context.Context, restaurantID int, date string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM hour_split_override WHERE restaurant_id = ? AND reservationDate = ?",
		restaurantID, date,
	)
	return err
}

// ---------------------------------------------------------------------------
// Admin handlers: GET/POST /api/admin/config/hour-split, POST .../hour-split-percentages
// ---------------------------------------------------------------------------

type boHourSplitPercentagesRequest struct {
	Date        *string             `json:"date,omitempty"`
	Hour        *string             `json:"hour,omitempty"`
	Percentage  *float64            `json:"percentage,omitempty"`
	People      *int                `json:"people,omitempty"`
	Percentages *map[string]float64 `json:"percentages,omitempty"`
}

func (s *Server) handleBOConfigHourSplitGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	enabled, source, err := s.resolveHourSplitEnabled(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando hour-split")
		return
	}

	activeHours, err := s.getOpeningHoursForDate(r, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando horarios")
		return
	}
	sortServiceHours(activeHours)

	percentages, _, err := s.loadHourPercentages(r.Context(), restaurantID, date, activeHours)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando porcentajes")
		return
	}

	dailyLimit := s.dailyLimitFor(r, restaurantID, date)

	bookingsByHour, totalPeople, err := s.fetchBookingsByHourHHMM(r, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando reservas")
		return
	}

	hourlyCapacities := percentagesToPeople(percentages, dailyLimit)

	resp := map[string]any{
		"success":          true,
		"date":             date,
		"enabled":          enabled,
		"source":           source,
		"dailyLimit":       dailyLimit,
		"totalPeople":      totalPeople,
		"activeHours":      activeHours,
		"percentages":      percentages,
		"hourlyCapacities": hourlyCapacities,
		"bookingsByHour":   bookingsByHour,
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBOConfigHourSplitSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	var req struct {
		Date    string `json:"date"`
		Enabled *bool  `json:"enabled"`
	}
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}
	if req.Enabled == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "enabled required"})
		return
	}

	defaults, err := s.loadReservationDefaults(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando defaults")
		return
	}

	// If the requested value matches the default, drop the override so the row follows
	// the default automatically (avoids stale overrides).
	if *req.Enabled == defaults.HourSplitEnabled {
		if err := s.clearHourSplitOverride(r.Context(), restaurantID, date); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando hour-split")
			return
		}
	} else {
		if err := s.setHourSplitOverride(r.Context(), restaurantID, date, *req.Enabled); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando hour-split")
			return
		}
	}

	enabled, source, err := s.resolveHourSplitEnabled(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando hour-split")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
		"enabled": enabled,
		"source":  source,
	})
}

func (s *Server) handleBOConfigHourSplitPercentagesSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	var req boHourSplitPercentagesRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if req.Date != nil {
		date = strings.TrimSpace(*req.Date)
	}
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}

	activeHours, err := s.getOpeningHoursForDate(r, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando horarios")
		return
	}
	sortServiceHours(activeHours)

	current, _, err := s.loadHourPercentages(r.Context(), restaurantID, date, activeHours)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando porcentajes")
		return
	}

	var next map[string]float64
	switch {
	case req.Percentages != nil:
		// Full map: normalize to active hours and validate sum.
		next, _ = normalizePercentages(*req.Percentages, activeHours)
		if !validatePercentagesSum(next) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Percentages must sum to 100",
			})
			return
		}
	case req.Hour != nil && req.Percentage != nil:
		next = rebalancePercentages(current, *req.Hour, *req.Percentage)
	case req.Hour != nil && req.People != nil:
		dailyLimit := s.dailyLimitFor(r, restaurantID, date)
		pct := peopleToPercentage(*req.People, dailyLimit)
		next = rebalancePercentages(current, *req.Hour, pct)
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid percentages payload"})
		return
	}

	if err := s.saveHourPercentages(r.Context(), restaurantID, date, next); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando porcentajes")
		return
	}

	dailyLimit := s.dailyLimitFor(r, restaurantID, date)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"date":             date,
		"percentages":      next,
		"hourlyCapacities": percentagesToPeople(next, dailyLimit),
	})
}

// dailyLimitFor resolves the daily limit for a date (override > default > 45).
func (s *Server) dailyLimitFor(r *http.Request, restaurantID int, date string) int {
	dailyLimit := 0
	_ = s.db.QueryRowContext(r.Context(),
		"SELECT dailyLimit FROM reservation_manager WHERE restaurant_id = ? AND reservationDate = ? ORDER BY id DESC LIMIT 1",
		restaurantID, date,
	).Scan(&dailyLimit)
	if dailyLimit > 0 {
		return dailyLimit
	}
	defaults, err := s.loadReservationDefaults(r.Context(), restaurantID)
	if err == nil && defaults.DailyLimit > 0 {
		return defaults.DailyLimit
	}
	return 45
}
