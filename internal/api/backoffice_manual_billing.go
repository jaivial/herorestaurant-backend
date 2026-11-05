package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sort"
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

func loadManualBillingEntries(ctx context.Context, db *sql.DB, restaurantID int, from, to time.Time) ([]manualBillingEntry, error) {
	// DATE_FORMAT is required: the DSN sets parseTime=true, so a raw DATE column
	// scanned into a string arrives as RFC3339 and would not parse below.
	rows, err := db.QueryContext(ctx, `
		SELECT id, DATE_FORMAT(entry_date, '%Y-%m-%d'), gross_cents, net_cents, vat_cents, vat_rate_bp, includes_vat
		FROM analytics_manual_billing_entries
		WHERE restaurant_id = ? AND entry_date BETWEEN ? AND ?
		ORDER BY entry_date ASC, id ASC
	`, restaurantID, affluenceFormatDate(from), affluenceFormatDate(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]manualBillingEntry, 0, 64)
	for rows.Next() {
		var id, gross, net, vat int64
		var date string
		var rateBP int
		var includesVat bool
		if err = rows.Scan(&id, &date, &gross, &net, &vat, &rateBP, &includesVat); err != nil {
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
			"entries":  affluencePercentDelta(float64(totals.Entries), float64(previous.Entries)),
			"grossEUR": affluencePercentDelta(totals.GrossEUR, previous.GrossEUR),
			"netEUR":   affluencePercentDelta(totals.NetEUR, previous.NetEUR),
			"vatEUR":   affluencePercentDelta(totals.VatEUR, previous.VatEUR),
		},
		"series":       manualBillingSeries(entries, from, to, bucket),
		"weekday":      manualBillingWeekday(entries),
		"vatBreakdown": manualBillingVatBreakdown(entries),
		"entries":      manualBillingListed(entries),
	})
}
