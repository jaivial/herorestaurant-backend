package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Customer affluence statistics (affluence_stats_v1). READ ONLY over the
// bookings history: one row per reservation_date is aggregated in SQL and the
// bucketing / gap-filling / comparison maths lives here so both handlers share
// the exact same building blocks.

const (
	affluenceTag              = "affluence_stats_v1"
	affluenceQueryTimeout     = 30 * time.Second
	affluenceMaxRangeDays     = 4018 // ~11 years
	affluenceMinReservation   = "2000-01-01"
	affluenceWeeksPerMonth    = 4
	affluenceMaxBucketsPerRun = 5000
)

type affluenceDailyRow struct {
	Date     time.Time
	Bookings int
	Covers   int
}

type affluenceTotals struct {
	Bookings              int     `json:"bookings"`
	Covers                int     `json:"covers"`
	AvgPartySize          float64 `json:"avgPartySize"`
	ActiveDays            int     `json:"activeDays"`
	AvgCoversPerActiveDay float64 `json:"avgCoversPerActiveDay"`
}

type affluenceBucketPoint struct {
	Key      string `json:"key"`
	Bookings int    `json:"bookings"`
	Covers   int    `json:"covers"`
}

type affluenceWeekdayPoint struct {
	Weekday  int `json:"weekday"`
	Bookings int `json:"bookings"`
	Covers   int `json:"covers"`
}

type affluencePeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type affluenceSeasonalWeek struct {
	Bookings int `json:"bookings"`
	Covers   int `json:"covers"`
}

type affluenceSeasonalMonth struct {
	Month    int                     `json:"month"`
	Bookings int                     `json:"bookings"`
	Covers   int                     `json:"covers"`
	Weeks    []affluenceSeasonalWeek `json:"weeks"`
}

type affluenceSeasonalYear struct {
	Year               int                      `json:"year"`
	Bookings           int                      `json:"bookings"`
	Covers             int                      `json:"covers"`
	Partial            bool                     `json:"partial"`
	DeltaPercentCovers *float64                 `json:"deltaPercentCovers"`
	DeltaToDate        bool                     `json:"deltaToDate"`
	Months             []affluenceSeasonalMonth `json:"months"`
}

// ---------------------------------------------------------------- helpers

func affluenceRound(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Round(value*100) / 100
}

func affluencePercentDelta(current, previous float64) *float64 {
	if previous == 0 {
		return nil
	}
	delta := affluenceRound((current - previous) / previous * 100)
	return &delta
}

func affluenceMadridLocation() *time.Location {
	if loc, err := time.LoadLocation("Europe/Madrid"); err == nil {
		return loc
	}
	return time.UTC
}

func affluenceFormatDate(t time.Time) string { return t.Format("2006-01-02") }

func affluenceTotalsFrom(rows []affluenceDailyRow) affluenceTotals {
	totals := affluenceTotals{}
	for _, row := range rows {
		if row.Bookings == 0 && row.Covers == 0 {
			continue
		}
		totals.Bookings += row.Bookings
		totals.Covers += row.Covers
		totals.ActiveDays++
	}
	if totals.Bookings > 0 {
		totals.AvgPartySize = affluenceRound(float64(totals.Covers) / float64(totals.Bookings))
	}
	if totals.ActiveDays > 0 {
		totals.AvgCoversPerActiveDay = affluenceRound(float64(totals.Covers) / float64(totals.ActiveDays))
	}
	return totals
}

// affluenceWeekdayPoints spreads the range rows over ISO weekdays (1=Mon..7=Sun).
func affluenceWeekdayPoints(rows []affluenceDailyRow) []affluenceWeekdayPoint {
	points := make([]affluenceWeekdayPoint, 7)
	for i := range points {
		points[i].Weekday = i + 1
	}
	for _, row := range rows {
		index := (int(row.Date.Weekday()) + 6) % 7
		points[index].Bookings += row.Bookings
		points[index].Covers += row.Covers
	}
	return points
}

// affluenceBucketStart truncates a date to the first day of its bucket.
func affluenceBucketStart(date time.Time, bucket string) time.Time {
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	switch bucket {
	case "week":
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset)
	case "month":
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "year":
		return time.Date(day.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		return day
	}
}

func affluenceNextBucket(date time.Time, bucket string) time.Time {
	switch bucket {
	case "week":
		return date.AddDate(0, 0, 7)
	case "month":
		return date.AddDate(0, 1, 0)
	case "year":
		return date.AddDate(1, 0, 0)
	default:
		return date.AddDate(0, 0, 1)
	}
}

// affluenceBucketedSeries gap-fills every bucket between from and to with zeros.
func affluenceBucketedSeries(rows []affluenceDailyRow, from, to time.Time, bucket string) []affluenceBucketPoint {
	byKey := make(map[string]affluenceBucketPoint, len(rows))
	for _, row := range rows {
		key := affluenceBucketStart(row.Date, bucket)
		point := byKey[affluenceFormatDate(key)]
		point.Bookings += row.Bookings
		point.Covers += row.Covers
		byKey[affluenceFormatDate(key)] = point
	}
	series := make([]affluenceBucketPoint, 0, 32)
	for cursor, steps := affluenceBucketStart(from, bucket), 0; !cursor.After(to) && steps < affluenceMaxBucketsPerRun; steps++ {
		key := affluenceFormatDate(cursor)
		point := byKey[key]
		point.Key = key
		series = append(series, point)
		cursor = affluenceNextBucket(cursor, bucket)
	}
	return series
}

// affluenceDailyRows loads the daily aggregates of a range in one query.
func affluenceDailyRows(ctx context.Context, db *sql.DB, restaurantID int, from, to time.Time) ([]affluenceDailyRow, error) {
	// DATE_FORMAT is required: the DSN sets parseTime=true, so a raw DATE column
	// scanned into a string arrives as RFC3339 and would not parse below.
	rows, err := db.QueryContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d'), COUNT(*), COALESCE(SUM(party_size), 0)
		FROM bookings
		WHERE restaurant_id = ?
		  AND status <> 'cancelled'
		  AND reservation_date >= ?
		  AND reservation_date BETWEEN ? AND ?
		GROUP BY reservation_date
		ORDER BY reservation_date ASC
	`, restaurantID, affluenceMinReservation, affluenceFormatDate(from), affluenceFormatDate(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAffluenceDailyRows(rows)
}

// scanAffluenceDailyRows reads the (date, bookings, covers) triples shared by
// every affluence query. A malformed date is a hard error, never a skipped row.
func scanAffluenceDailyRows(rows *sql.Rows) ([]affluenceDailyRow, error) {
	var out []affluenceDailyRow
	for rows.Next() {
		var row affluenceDailyRow
		var date string
		if err := rows.Scan(&date, &row.Bookings, &row.Covers); err != nil {
			return nil, err
		}
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, fmt.Errorf("affluence: unexpected reservation_date %q: %w", date, err)
		}
		row.Date = parsed
		out = append(out, row)
	}
	return out, rows.Err()
}

// affluenceHistory returns the whole-table bounds (non-cancelled, >= 2000).
func affluenceHistory(ctx context.Context, db *sql.DB, restaurantID int) (string, string, error) {
	var first, last sql.NullString
	// DATE_FORMAT for the same parseTime=true reason as affluenceDailyRows.
	err := db.QueryRowContext(ctx, `
		SELECT DATE_FORMAT(MIN(reservation_date), '%Y-%m-%d'), DATE_FORMAT(MAX(reservation_date), '%Y-%m-%d')
		FROM bookings
		WHERE restaurant_id = ? AND status <> 'cancelled' AND reservation_date >= ?
	`, restaurantID, affluenceMinReservation).Scan(&first, &last)
	if err != nil {
		return "", "", err
	}
	return first.String, last.String, nil
}

func parseAffluenceBucket(raw string) (string, error) {
	bucket := strings.ToLower(strings.TrimSpace(raw))
	if bucket == "" {
		return "day", nil
	}
	switch bucket {
	case "day", "week", "month", "year":
		return bucket, nil
	default:
		return "", fmt.Errorf("bucket must be day, week, month or year")
	}
}

func parseAffluenceMonths(raw string) ([]int, error) {
	seen := make(map[int]bool, 12)
	months := make([]int, 0, 12)
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		month, err := strconv.Atoi(value)
		if err != nil || month < 1 || month > 12 {
			return nil, fmt.Errorf("months must be a comma list of values between 1 and 12")
		}
		if seen[month] {
			continue
		}
		seen[month] = true
		months = append(months, month)
	}
	if len(months) == 0 {
		return nil, fmt.Errorf("months must contain at least one month between 1 and 12")
	}
	sort.Ints(months)
	return months, nil
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleBOAffluence(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	query := r.URL.Query()
	from, err := parseAnalyticsDate(query.Get("from"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseAnalyticsDate(query.Get("to"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if from.After(to) {
		httpx.WriteError(w, http.StatusBadRequest, "from must not be after to")
		return
	}
	if to.Sub(from) > affluenceMaxRangeDays*24*time.Hour {
		httpx.WriteError(w, http.StatusBadRequest, "date range is too large")
		return
	}
	bucket, err := parseAffluenceBucket(query.Get("bucket"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), affluenceQueryTimeout)
	defer cancel()

	rows, err := affluenceDailyRows(ctx, s.db, auth.ActiveRestaurantID, from, to)
	if err != nil {
		log.Printf("%s failed restaurant_id=%d: %v", affluenceTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load affluence statistics")
		return
	}
	firstDate, lastDate, err := affluenceHistory(ctx, s.db, auth.ActiveRestaurantID)
	if err != nil {
		log.Printf("%s history failed restaurant_id=%d: %v", affluenceTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load affluence statistics")
		return
	}

	// Previous period: same length, ending the day before `from`.
	previousTo := from.AddDate(0, 0, -1)
	previousFrom := previousTo.AddDate(0, 0, -int(to.Sub(from)/(24*time.Hour)))
	previousRows, err := affluenceDailyRows(ctx, s.db, auth.ActiveRestaurantID, previousFrom, previousTo)
	if err != nil {
		log.Printf("%s previous period failed restaurant_id=%d: %v", affluenceTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load affluence statistics")
		return
	}

	totals := affluenceTotalsFrom(rows)
	previous := affluenceTotalsFrom(previousRows)
	history := map[string]any{"firstDate": nullableDate(firstDate), "lastDate": nullableDate(lastDate)}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tag":     affluenceTag,
		"from":    affluenceFormatDate(from),
		"to":      affluenceFormatDate(to),
		"bucket":  bucket,
		"history": history,
		"totals":  totals,
		"previous": map[string]any{
			"from":                  affluenceFormatDate(previousFrom),
			"to":                    affluenceFormatDate(previousTo),
			"bookings":              previous.Bookings,
			"covers":                previous.Covers,
			"avgPartySize":          previous.AvgPartySize,
			"activeDays":            previous.ActiveDays,
			"avgCoversPerActiveDay": previous.AvgCoversPerActiveDay,
		},
		"deltaPercent": map[string]any{
			"bookings": affluencePercentDelta(float64(totals.Bookings), float64(previous.Bookings)),
			"covers":   affluencePercentDelta(float64(totals.Covers), float64(previous.Covers)),
		},
		"series":  affluenceBucketedSeries(rows, from, to, bucket),
		"weekday": affluenceWeekdayPoints(rows),
	})
}

func (s *Server) handleBOAffluenceSeasonal(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	months, err := parseAffluenceMonths(r.URL.Query().Get("months"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), affluenceQueryTimeout)
	defer cancel()

	rows, err := affluenceSeasonalRows(ctx, s.db, auth.ActiveRestaurantID, months)
	if err != nil {
		log.Printf("%s seasonal failed restaurant_id=%d: %v", affluenceTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load seasonal affluence statistics")
		return
	}
	firstDate, lastDate, err := affluenceHistory(ctx, s.db, auth.ActiveRestaurantID)
	if err != nil {
		log.Printf("%s seasonal history failed restaurant_id=%d: %v", affluenceTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load seasonal affluence statistics")
		return
	}

	location := affluenceMadridLocation()
	now := time.Now().In(location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tag":     affluenceTag,
		"months":  months,
		"today":   affluenceFormatDate(today),
		"history": map[string]any{"firstDate": nullableDate(firstDate), "lastDate": nullableDate(lastDate)},
		"years":   affluenceSeasonalYears(rows, months, firstDate, today),
	})
}

// ---------------------------------------------------------------- seasonal

func affluenceSeasonalRows(ctx context.Context, db *sql.DB, restaurantID int, months []int) ([]affluenceDailyRow, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(months)), ",")
	args := make([]any, 0, len(months)+2)
	args = append(args, restaurantID, affluenceMinReservation)
	for _, month := range months {
		args = append(args, month)
	}
	// '%%Y-%%m-%%d' because this template goes through fmt.Sprintf (placeholders).
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT DATE_FORMAT(reservation_date, '%%Y-%%m-%%d'), COUNT(*), COALESCE(SUM(party_size), 0)
		FROM bookings
		WHERE restaurant_id = ?
		  AND status <> 'cancelled'
		  AND reservation_date >= ?
		  AND MONTH(reservation_date) IN (%s)
		GROUP BY reservation_date
		ORDER BY reservation_date ASC
	`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAffluenceDailyRows(rows)
}

func nullableDate(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// affluenceSeasonalYears builds one card per comparable year: every year from the
// first year with history up to the current one, plus future years that already
// hold data for the selected months (pre-booked future reservations).
func affluenceSeasonalYears(rows []affluenceDailyRow, months []int, firstDate string, today time.Time) []affluenceSeasonalYear {
	byYearMonth := make(map[int]map[int][]affluenceDailyRow)
	yearSet := make(map[int]bool)
	for _, row := range rows {
		year, month := row.Date.Year(), int(row.Date.Month())
		if byYearMonth[year] == nil {
			byYearMonth[year] = make(map[int][]affluenceDailyRow, len(months))
		}
		byYearMonth[year][month] = append(byYearMonth[year][month], row)
		yearSet[year] = true
	}
	if parsed, err := time.Parse("2006-01-02", firstDate); err == nil {
		yearSet[parsed.Year()] = true
	}

	firstYear := 0
	for year := range yearSet {
		if firstYear == 0 || year < firstYear {
			firstYear = year
		}
	}
	if firstYear == 0 {
		return []affluenceSeasonalYear{}
	}
	for year := firstYear; year <= today.Year(); year++ {
		yearSet[year] = true
	}

	historyStart, _ := time.Parse("2006-01-02", firstDate)
	years := make([]int, 0, len(yearSet))
	for year := range yearSet {
		// Skip years whose selected months all ended before the history starts:
		// they would read as a misleading zero, not as "no data".
		if affluenceYearHasHistory(year, months, historyStart) {
			years = append(years, year)
		}
	}
	sort.Ints(years)

	out := make([]affluenceSeasonalYear, 0, len(years))
	for index, year := range years {
		card := affluenceSeasonalYear{Year: year, Months: make([]affluenceSeasonalMonth, 0, len(months))}
		for _, month := range months {
			monthRows := byYearMonth[year][month]
			entry := affluenceSeasonalMonth{Month: month, Weeks: make([]affluenceSeasonalWeek, affluenceWeeksPerMonth)}
			for _, row := range monthRows {
				entry.Bookings += row.Bookings
				entry.Covers += row.Covers
				weekIndex := (row.Date.Day() - 1) / 7
				if weekIndex >= affluenceWeeksPerMonth {
					weekIndex = affluenceWeeksPerMonth - 1
				}
				entry.Weeks[weekIndex].Bookings += row.Bookings
				entry.Weeks[weekIndex].Covers += row.Covers
			}
			card.Months = append(card.Months, entry)
			card.Bookings += entry.Bookings
			card.Covers += entry.Covers
			if !affluenceMonthFinished(year, month, today) {
				card.Partial = true
			}
		}
		if index > 0 {
			previousCovers := out[index-1].Covers
			if card.Partial {
				// An unfinished season is compared like-for-like: the previous year
				// only up to the same day, so a running month does not read as a drop.
				previousCovers = affluenceCoversUntil(byYearMonth[out[index-1].Year], today.AddDate(-(year-out[index-1].Year), 0, 0))
				card.DeltaToDate = true
			}
			card.DeltaPercentCovers = affluencePercentDelta(float64(card.Covers), float64(previousCovers))
		}
		out = append(out, card)
	}
	return out
}

func affluenceMonthFinished(year, month int, today time.Time) bool {
	monthEnd := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	return !monthEnd.After(today)
}

func affluenceYearHasHistory(year int, months []int, historyStart time.Time) bool {
	for _, month := range months {
		if !time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Before(historyStart) {
			return true
		}
	}
	return false
}

// affluenceCoversUntil sums the covers of one year's selected months up to limit (inclusive).
func affluenceCoversUntil(byMonth map[int][]affluenceDailyRow, limit time.Time) int {
	limitDay := time.Date(limit.Year(), limit.Month(), limit.Day(), 0, 0, 0, 0, time.UTC)
	covers := 0
	for _, rows := range byMonth {
		for _, row := range rows {
			if !row.Date.After(limitDay) {
				covers += row.Covers
			}
		}
	}
	return covers
}
