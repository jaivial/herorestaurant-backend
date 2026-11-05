package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Simple billing entries typed by hand for Estadísticas > Ventas.
// Coordination id: manual_billing_stats_v1. The amount is stored already split
// (gross/net/VAT) so every read is a plain SUM and the filter maths lives in
// one place: the bucket gaps and weekday/VAT breakdowns below.
const (
	manualBillingTag           = "manual_billing_stats_v1"
	manualBillingQueryTimeout  = 20 * time.Second
	manualBillingMaxAmountEUR  = 1_000_000_000
	manualBillingMaxListedRows = 200
)

type manualBillingEntry struct {
	ID          int64
	Date        time.Time
	GrossCents  int64
	NetCents    int64
	VatCents    int64
	VatRateBP   int
	IncludesVat bool
}

type manualBillingCreateRequest struct {
	Date        string   `json:"date"`
	Amount      *float64 `json:"amount"`
	IncludesVat bool     `json:"includesVat"`
	VatRate     *float64 `json:"vatRate"`
}

type manualBillingTotals struct {
	Entries            int     `json:"entries"`
	GrossEUR           float64 `json:"grossEUR"`
	NetEUR             float64 `json:"netEUR"`
	VatEUR             float64 `json:"vatEUR"`
	VatIncludedEntries int     `json:"vatIncludedEntries"`
	ActiveDays         int     `json:"activeDays"`
	AvgGrossEUR        float64 `json:"avgGrossEUR"`
}

type manualBillingSeriesPoint struct {
	Key      string  `json:"key"`
	Entries  int     `json:"entries"`
	GrossEUR float64 `json:"grossEUR"`
	NetEUR   float64 `json:"netEUR"`
	VatEUR   float64 `json:"vatEUR"`
}

type manualBillingWeekdayPoint struct {
	Weekday  int     `json:"weekday"`
	Entries  int     `json:"entries"`
	GrossEUR float64 `json:"grossEUR"`
	NetEUR   float64 `json:"netEUR"`
	VatEUR   float64 `json:"vatEUR"`
}

type manualBillingVatRateRow struct {
	Rate     float64 `json:"rate"`
	Entries  int     `json:"entries"`
	GrossEUR float64 `json:"grossEUR"`
	NetEUR   float64 `json:"netEUR"`
	VatEUR   float64 `json:"vatEUR"`
}

type manualBillingSeasonalWeek struct {
	Entries  int     `json:"entries"`
	GrossEUR float64 `json:"grossEUR"`
	NetEUR   float64 `json:"netEUR"`
	VatEUR   float64 `json:"vatEUR"`
}

type manualBillingSeasonalMonth struct {
	Month    int                         `json:"month"`
	Entries  int                         `json:"entries"`
	GrossEUR float64                     `json:"grossEUR"`
	NetEUR   float64                     `json:"netEUR"`
	VatEUR   float64                     `json:"vatEUR"`
	Weeks    []manualBillingSeasonalWeek `json:"weeks"`
}

type manualBillingSeasonalYear struct {
	Year              int                          `json:"year"`
	Entries           int                          `json:"entries"`
	GrossEUR          float64                      `json:"grossEUR"`
	NetEUR            float64                      `json:"netEUR"`
	VatEUR            float64                      `json:"vatEUR"`
	Partial           bool                         `json:"partial"`
	DeltaPercentGross *float64                     `json:"deltaPercentGross"`
	DeltaToDate       bool                         `json:"deltaToDate"`
	Months            []manualBillingSeasonalMonth `json:"months"`
}

type manualBillingEntryPayload struct {
	ID          int64   `json:"id"`
	Date        string  `json:"date"`
	GrossEUR    float64 `json:"grossEUR"`
	NetEUR      float64 `json:"netEUR"`
	VatEUR      float64 `json:"vatEUR"`
	VatRate     float64 `json:"vatRate"`
	IncludesVat bool    `json:"includesVat"`
}

type manualBillingAccumulator struct {
	entries int
	gross   int64
	net     int64
	vat     int64
}

func (acc *manualBillingAccumulator) add(entry manualBillingEntry) {
	acc.entries++
	acc.gross += entry.GrossCents
	acc.net += entry.NetCents
	acc.vat += entry.VatCents
}

func (acc manualBillingAccumulator) grossEUR() float64 {
	return affluenceRound(float64(acc.gross) / 100)
}
func (acc manualBillingAccumulator) netEUR() float64 { return affluenceRound(float64(acc.net) / 100) }
func (acc manualBillingAccumulator) vatEUR() float64 { return affluenceRound(float64(acc.vat) / 100) }

func manualBillingEUR(cents int64) float64 { return affluenceRound(float64(cents) / 100) }

// manualBillingPercentDelta keeps the sign meaningful when the previous period
// was negative (the ledger accepts negative entries), so the denominator is
// always absolute — same rule as the analytics revenue comparison.
func manualBillingPercentDelta(current, previous float64) *float64 {
	if previous == 0 {
		return nil
	}
	delta := affluenceRound((current - previous) / math.Abs(previous) * 100)
	return &delta
}

// manualBillingSplit turns the signed amount the owner typed into net + VAT
// cents. With VAT included the net is the rounded base and the VAT is the
// remainder, so gross never drifts; without VAT the amount is all net (0%).
func manualBillingSplit(grossCents int64, vatRateBP int) (int64, int64) {
	if vatRateBP <= 0 {
		return grossCents, 0
	}
	net := int64(math.Round(float64(grossCents) * 10000 / float64(10000+vatRateBP)))
	return net, grossCents - net
}

func manualBillingEntryFromRow(id int64, date time.Time, gross, net, vat int64, rateBP int, includesVat bool) manualBillingEntry {
	return manualBillingEntry{ID: id, Date: date, GrossCents: gross, NetCents: net, VatCents: vat, VatRateBP: rateBP, IncludesVat: includesVat}
}

func (entry manualBillingEntry) payload() manualBillingEntryPayload {
	return manualBillingEntryPayload{
		ID:          entry.ID,
		Date:        affluenceFormatDate(entry.Date),
		GrossEUR:    manualBillingEUR(entry.GrossCents),
		NetEUR:      manualBillingEUR(entry.NetCents),
		VatEUR:      manualBillingEUR(entry.VatCents),
		VatRate:     float64(entry.VatRateBP) / 100,
		IncludesVat: entry.IncludesVat,
	}
}

func manualBillingTotalsFrom(entries []manualBillingEntry) manualBillingTotals {
	var acc manualBillingAccumulator
	days := make(map[string]struct{}, len(entries))
	vatIncluded := 0
	for _, entry := range entries {
		acc.add(entry)
		days[affluenceFormatDate(entry.Date)] = struct{}{}
		if entry.IncludesVat {
			vatIncluded++
		}
	}
	totals := manualBillingTotals{
		Entries:            acc.entries,
		GrossEUR:           acc.grossEUR(),
		NetEUR:             acc.netEUR(),
		VatEUR:             acc.vatEUR(),
		VatIncludedEntries: vatIncluded,
		ActiveDays:         len(days),
	}
	if acc.entries > 0 {
		totals.AvgGrossEUR = affluenceRound(totals.GrossEUR / float64(acc.entries))
	}
	return totals
}

// manualBillingSeries gap-fills every bucket between from and to with zeros.
func manualBillingSeries(entries []manualBillingEntry, from, to time.Time, bucket string) []manualBillingSeriesPoint {
	byKey := make(map[string]*manualBillingAccumulator, len(entries))
	for _, entry := range entries {
		key := affluenceFormatDate(affluenceBucketStart(entry.Date, bucket))
		if byKey[key] == nil {
			byKey[key] = &manualBillingAccumulator{}
		}
		byKey[key].add(entry)
	}
	series := make([]manualBillingSeriesPoint, 0, 32)
	for cursor, steps := affluenceBucketStart(from, bucket), 0; !cursor.After(to) && steps < affluenceMaxBucketsPerRun; steps++ {
		key := affluenceFormatDate(cursor)
		point := manualBillingSeriesPoint{Key: key}
		if acc := byKey[key]; acc != nil {
			point.Entries = acc.entries
			point.GrossEUR = acc.grossEUR()
			point.NetEUR = acc.netEUR()
			point.VatEUR = acc.vatEUR()
		}
		series = append(series, point)
		cursor = affluenceNextBucket(cursor, bucket)
	}
	return series
}

// manualBillingWeekday spreads the range over ISO weekdays (1=Mon..7=Sun).
func manualBillingWeekday(entries []manualBillingEntry) []manualBillingWeekdayPoint {
	accumulators := make([]manualBillingAccumulator, 7)
	for _, entry := range entries {
		index := (int(entry.Date.Weekday()) + 6) % 7
		accumulators[index].add(entry)
	}
	points := make([]manualBillingWeekdayPoint, 7)
	for i := range points {
		acc := accumulators[i]
		points[i] = manualBillingWeekdayPoint{
			Weekday:  i + 1,
			Entries:  acc.entries,
			GrossEUR: acc.grossEUR(),
			NetEUR:   acc.netEUR(),
			VatEUR:   acc.vatEUR(),
		}
	}
	return points
}

// manualBillingVatBreakdown answers "how much was paid with VAT and how much of
// that was VAT": one row per rate, ascending, so 0% rows stay visible.
func manualBillingVatBreakdown(entries []manualBillingEntry) []manualBillingVatRateRow {
	byRate := make(map[int]*manualBillingAccumulator, 4)
	rates := make([]int, 0, 4)
	for _, entry := range entries {
		if byRate[entry.VatRateBP] == nil {
			byRate[entry.VatRateBP] = &manualBillingAccumulator{}
			rates = append(rates, entry.VatRateBP)
		}
		byRate[entry.VatRateBP].add(entry)
	}
	sort.Ints(rates)
	rows := make([]manualBillingVatRateRow, 0, len(rates))
	for _, rateBP := range rates {
		acc := byRate[rateBP]
		rows = append(rows, manualBillingVatRateRow{
			Rate:     float64(rateBP) / 100,
			Entries:  acc.entries,
			GrossEUR: acc.grossEUR(),
			NetEUR:   acc.netEUR(),
			VatEUR:   acc.vatEUR(),
		})
	}
	return rows
}

// manualBillingEntrySelect is the shared column list: DATE_FORMAT is required
// because the DSN sets parseTime=true, so a raw DATE column scanned into a
// string would arrive as RFC3339 and not parse below.
const manualBillingEntrySelect = `
	SELECT id, DATE_FORMAT(entry_date, '%Y-%m-%d'), gross_cents, net_cents, vat_cents, vat_rate_bp, includes_vat
	FROM analytics_manual_billing_entries`

func scanManualBillingEntries(rows *sql.Rows) ([]manualBillingEntry, error) {
	entries := make([]manualBillingEntry, 0, 64)
	for rows.Next() {
		var id, gross, net, vat int64
		var date string
		var rateBP int
		var includesVat bool
		if err := rows.Scan(&id, &date, &gross, &net, &vat, &rateBP, &includesVat); err != nil {
			return nil, err
		}
		parsed, parseErr := time.Parse("2006-01-02", date)
		if parseErr != nil {
			return nil, parseErr
		}
		entries = append(entries, manualBillingEntryFromRow(id, parsed, gross, net, vat, rateBP, includesVat))
	}
	return entries, rows.Err()
}

func loadManualBillingEntries(ctx context.Context, db *sql.DB, restaurantID int, from, to time.Time) ([]manualBillingEntry, error) {
	rows, err := db.QueryContext(ctx, manualBillingEntrySelect+`
		WHERE restaurant_id = ? AND entry_date BETWEEN ? AND ?
		ORDER BY entry_date ASC, id ASC
	`, restaurantID, affluenceFormatDate(from), affluenceFormatDate(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanManualBillingEntries(rows)
}

// loadManualBillingSeasonalEntries loads every entry of the selected months
// (all years), which is what both seasonality comparisons aggregate.
func loadManualBillingSeasonalEntries(ctx context.Context, db *sql.DB, restaurantID int, months []int) ([]manualBillingEntry, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(months)), ",")
	args := make([]any, 0, len(months)+1)
	args = append(args, restaurantID)
	for _, month := range months {
		args = append(args, month)
	}
	rows, err := db.QueryContext(ctx, manualBillingEntrySelect+fmt.Sprintf(`
		WHERE restaurant_id = ? AND MONTH(entry_date) IN (%s)
		ORDER BY entry_date ASC, id ASC
	`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanManualBillingEntries(rows)
}

func manualBillingHistory(ctx context.Context, db *sql.DB, restaurantID int) (string, string, error) {
	var first, last sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT DATE_FORMAT(MIN(entry_date), '%Y-%m-%d'), DATE_FORMAT(MAX(entry_date), '%Y-%m-%d')
		FROM analytics_manual_billing_entries
		WHERE restaurant_id = ?
	`, restaurantID).Scan(&first, &last)
	if err != nil {
		return "", "", err
	}
	return first.String, last.String, nil
}

func manualBillingListed(entries []manualBillingEntry) []manualBillingEntryPayload {
	payloads := make([]manualBillingEntryPayload, 0, len(entries))
	for i := len(entries) - 1; i >= 0 && len(payloads) < manualBillingMaxListedRows; i-- {
		payloads = append(payloads, entries[i].payload())
	}
	return payloads
}

func (s *Server) handleBOAnalyticsManualBillingCreate(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var input manualBillingCreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	date, err := parseAnalyticsDate(input.Date)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Amount == nil || math.IsNaN(*input.Amount) || math.IsInf(*input.Amount, 0) {
		httpx.WriteError(w, http.StatusBadRequest, "amount is required")
		return
	}
	if math.Abs(*input.Amount) > manualBillingMaxAmountEUR {
		httpx.WriteError(w, http.StatusBadRequest, "amount is too large")
		return
	}
	grossCents := int64(math.Round(*input.Amount * 100))
	if grossCents == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "amount must not be zero")
		return
	}
	vatRateBP := 0
	if input.IncludesVat {
		rate := 10.0
		if input.VatRate != nil {
			rate = *input.VatRate
		}
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 100 {
			httpx.WriteError(w, http.StatusBadRequest, "vatRate must be between 0 and 100")
			return
		}
		vatRateBP = int(math.Round(rate * 100))
	}
	netCents, vatCents := manualBillingSplit(grossCents, vatRateBP)

	ctx, cancel := context.WithTimeout(r.Context(), manualBillingQueryTimeout)
	defer cancel()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO analytics_manual_billing_entries
			(restaurant_id, entry_date, gross_cents, net_cents, vat_cents, vat_rate_bp, includes_vat)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, auth.ActiveRestaurantID, affluenceFormatDate(date), grossCents, netCents, vatCents, vatRateBP, input.IncludesVat)
	if err != nil {
		log.Printf("%s create failed restaurant_id=%d: %v", manualBillingTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to save billing entry")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		id = 0
	}
	entry := manualBillingEntryFromRow(id, date, grossCents, netCents, vatCents, vatRateBP, input.IncludesVat)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"success": true,
		"tag":     manualBillingTag,
		"entry":   entry.payload(),
	})
}

func (s *Server) handleBOAnalyticsManualBilling(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), manualBillingQueryTimeout)
	defer cancel()
	entries, err := loadManualBillingEntries(ctx, s.db, auth.ActiveRestaurantID, from, to)
	if err != nil {
		log.Printf("%s load failed restaurant_id=%d: %v", manualBillingTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load billing statistics")
		return
	}
	firstDate, lastDate, err := manualBillingHistory(ctx, s.db, auth.ActiveRestaurantID)
	if err != nil {
		log.Printf("%s history failed restaurant_id=%d: %v", manualBillingTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load billing statistics")
		return
	}

	// Previous period: same length, ending the day before `from`.
	previousTo := from.AddDate(0, 0, -1)
	previousFrom := previousTo.AddDate(0, 0, -int(to.Sub(from)/(24*time.Hour)))
	previousEntries, err := loadManualBillingEntries(ctx, s.db, auth.ActiveRestaurantID, previousFrom, previousTo)
	if err != nil {
		log.Printf("%s previous period failed restaurant_id=%d: %v", manualBillingTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load billing statistics")
		return
	}

	totals := manualBillingTotalsFrom(entries)
	previous := manualBillingTotalsFrom(previousEntries)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tag":     manualBillingTag,
		"from":    affluenceFormatDate(from),
		"to":      affluenceFormatDate(to),
		"bucket":  bucket,
		"history": map[string]any{"firstDate": nullableDate(firstDate), "lastDate": nullableDate(lastDate)},
		"totals":  totals,
		"previous": map[string]any{
			"from":               affluenceFormatDate(previousFrom),
			"to":                 affluenceFormatDate(previousTo),
			"entries":            previous.Entries,
			"grossEUR":           previous.GrossEUR,
			"netEUR":             previous.NetEUR,
			"vatEUR":             previous.VatEUR,
			"vatIncludedEntries": previous.VatIncludedEntries,
			"activeDays":         previous.ActiveDays,
			"avgGrossEUR":        previous.AvgGrossEUR,
		},
		"deltaPercent": map[string]any{
			"entries":  manualBillingPercentDelta(float64(totals.Entries), float64(previous.Entries)),
			"grossEUR": manualBillingPercentDelta(totals.GrossEUR, previous.GrossEUR),
			"netEUR":   manualBillingPercentDelta(totals.NetEUR, previous.NetEUR),
			"vatEUR":   manualBillingPercentDelta(totals.VatEUR, previous.VatEUR),
		},
		"series":       manualBillingSeries(entries, from, to, bucket),
		"weekday":      manualBillingWeekday(entries),
		"vatBreakdown": manualBillingVatBreakdown(entries),
		// Totals count every row; the list is capped so one long history cannot
		// grow the response without bound.
		"entriesTruncated": len(entries) > manualBillingMaxListedRows,
		"entries":          manualBillingListed(entries),
	})
}

// ---------------------------------------------------------------- seasonal

func manualBillingMonthFinished(year, month int, today time.Time) bool {
	monthEnd := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC)
	return !monthEnd.After(today)
}

// manualBillingGrossUntil sums the gross of one year's selected months up to
// limit (inclusive), so an unfinished month compares like-for-like.
func manualBillingGrossUntil(byMonth map[int][]manualBillingEntry, limit time.Time) float64 {
	limitDay := time.Date(limit.Year(), limit.Month(), limit.Day(), 0, 0, 0, 0, time.UTC)
	var acc manualBillingAccumulator
	for _, entries := range byMonth {
		for _, entry := range entries {
			if !entry.Date.After(limitDay) {
				acc.add(entry)
			}
		}
	}
	return acc.grossEUR()
}

// manualBillingSeasonalYears builds one card per year with entries in the
// selected months. Unlike bookings, a year without manual entries has nothing
// to compare, so only years with data are listed. Each card's delta is
// measured against the previous listed year; an unfinished current season is
// compared up to the same date of that year.
func manualBillingSeasonalYears(entries []manualBillingEntry, months []int, today time.Time) []manualBillingSeasonalYear {
	byYear := make(map[int]map[int][]manualBillingEntry)
	for _, entry := range entries {
		year, month := entry.Date.Year(), int(entry.Date.Month())
		if byYear[year] == nil {
			byYear[year] = make(map[int][]manualBillingEntry, len(months))
		}
		byYear[year][month] = append(byYear[year][month], entry)
	}
	years := make([]int, 0, len(byYear))
	for year := range byYear {
		years = append(years, year)
	}
	sort.Ints(years)

	out := make([]manualBillingSeasonalYear, 0, len(years))
	for index, year := range years {
		card := manualBillingSeasonalYear{Year: year, Months: make([]manualBillingSeasonalMonth, 0, len(months))}
		var cardAcc manualBillingAccumulator
		for _, month := range months {
			var monthAcc manualBillingAccumulator
			weekAccs := make([]manualBillingAccumulator, affluenceWeeksPerMonth)
			for _, entry := range byYear[year][month] {
				monthAcc.add(entry)
				cardAcc.add(entry)
				weekIndex := (entry.Date.Day() - 1) / 7
				if weekIndex >= affluenceWeeksPerMonth {
					weekIndex = affluenceWeeksPerMonth - 1
				}
				weekAccs[weekIndex].add(entry)
			}
			monthRow := manualBillingSeasonalMonth{
				Month:    month,
				Entries:  monthAcc.entries,
				GrossEUR: monthAcc.grossEUR(),
				NetEUR:   monthAcc.netEUR(),
				VatEUR:   monthAcc.vatEUR(),
				Weeks:    make([]manualBillingSeasonalWeek, affluenceWeeksPerMonth),
			}
			for weekIndex, week := range weekAccs {
				monthRow.Weeks[weekIndex] = manualBillingSeasonalWeek{
					Entries:  week.entries,
					GrossEUR: week.grossEUR(),
					NetEUR:   week.netEUR(),
					VatEUR:   week.vatEUR(),
				}
			}
			card.Months = append(card.Months, monthRow)
			if !manualBillingMonthFinished(year, month, today) {
				card.Partial = true
			}
		}
		card.Entries = cardAcc.entries
		card.GrossEUR = cardAcc.grossEUR()
		card.NetEUR = cardAcc.netEUR()
		card.VatEUR = cardAcc.vatEUR()
		if index > 0 {
			previous := out[index-1]
			previousGross := previous.GrossEUR
			if card.Partial {
				previousGross = manualBillingGrossUntil(byYear[previous.Year], today.AddDate(-(year-previous.Year), 0, 0))
				card.DeltaToDate = true
			}
			card.DeltaPercentGross = manualBillingPercentDelta(card.GrossEUR, previousGross)
		}
		out = append(out, card)
	}
	return out
}

func (s *Server) handleBOAnalyticsManualBillingSeasonal(w http.ResponseWriter, r *http.Request) {
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

	ctx, cancel := context.WithTimeout(r.Context(), manualBillingQueryTimeout)
	defer cancel()
	entries, err := loadManualBillingSeasonalEntries(ctx, s.db, auth.ActiveRestaurantID, months)
	if err != nil {
		log.Printf("%s seasonal failed restaurant_id=%d: %v", manualBillingTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load seasonal billing statistics")
		return
	}
	firstDate, lastDate, err := manualBillingHistory(ctx, s.db, auth.ActiveRestaurantID)
	if err != nil {
		log.Printf("%s seasonal history failed restaurant_id=%d: %v", manualBillingTag, auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load seasonal billing statistics")
		return
	}

	location := affluenceMadridLocation()
	now := time.Now().In(location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tag":     manualBillingTag,
		"months":  months,
		"today":   affluenceFormatDate(today),
		"history": map[string]any{"firstDate": nullableDate(firstDate), "lastDate": nullableDate(lastDate)},
		"years":   manualBillingSeasonalYears(entries, months, today),
	})
}
