package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/analytics"
	"preactvillacarmen/internal/httpx"
)

type analyticsRefreshRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func parseAnalyticsDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, errors.New("date must use YYYY-MM-DD")
	}
	return parsed.UTC(), nil
}

func parseAnalyticsRange(fromValue, toValue string) (analytics.DateRange, error) {
	from, err := parseAnalyticsDate(fromValue)
	if err != nil {
		return analytics.DateRange{}, err
	}
	to, err := parseAnalyticsDate(toValue)
	if err != nil {
		return analytics.DateRange{}, err
	}
	if from.After(to) {
		return analytics.DateRange{}, errors.New("from must not be after to")
	}
	if to.Sub(from) > 1825*24*time.Hour {
		return analytics.DateRange{}, errors.New("date range is too large")
	}
	return analytics.DateRange{From: from, To: to}, nil
}

func parseAnalyticsGranularity(value string) (analytics.Granularity, error) {
	granularity := analytics.Granularity(strings.ToLower(strings.TrimSpace(value)))
	if granularity == "" {
		granularity = analytics.GranularityDay
	}
	switch granularity {
	case analytics.GranularityDay, analytics.GranularityWeek, analytics.GranularityMonth, analytics.GranularityQuarter, analytics.GranularityYear:
		return granularity, nil
	default:
		return "", errors.New("granularity must be day, week, month, quarter or year")
	}
}

func (s *Server) handleBOAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rangeValue, err := parseAnalyticsRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	granularity, err := parseAnalyticsGranularity(r.URL.Query().Get("granularity"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	compare := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("compare")))
	if compare != "" && compare != "previous" {
		httpx.WriteError(w, http.StatusBadRequest, "compare must be previous")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	service := analytics.NewService(s.db)
	overview, err := service.OverviewWithBreakdowns(ctx, auth.ActiveRestaurantID, rangeValue, granularity, compare == "previous")
	if err == nil && overview.DataQuality.RefreshRequired {
		refreshRange := rangeValue
		if compare == "previous" {
			previous := analytics.PreviousComparisonRange(rangeValue.From, rangeValue.To)
			refreshRange.From = previous.Start
		}
		if _, refreshErr := service.Refresh(ctx, auth.ActiveRestaurantID, refreshRange); refreshErr != nil {
			err = refreshErr
		} else {
			overview, err = service.OverviewWithBreakdowns(ctx, auth.ActiveRestaurantID, rangeValue, granularity, compare == "previous")
		}
	}
	if err != nil {
		log.Printf("analytics overview failed restaurant_id=%d: %v", auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load analytics overview")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":            true,
		"currency":           overview.Currency,
		"from":               overview.From,
		"to":                 overview.To,
		"granularity":        overview.Granularity,
		"summary":            overview.Summary,
		"comparison":         overview.Comparison,
		"series":             overview.Series,
		"wasteBreakdown":     overview.WasteBreakdown,
		"dataQuality":        overview.DataQuality,
		"topItems":           overview.TopItems,
		"paymentMethods":     overview.PaymentMethods,
		"dayOfWeek":          overview.DayOfWeek,
		"hourlyDistribution": overview.HourlyDistribution,
		"revenueByCategory":  overview.RevenueByCategory,
	})
}

func (s *Server) handleBOAnalyticsRefresh(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var input analyticsRefreshRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&input); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	rangeValue, err := parseAnalyticsRange(input.From, input.To)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	previous := analytics.PreviousComparisonRange(rangeValue.From, rangeValue.To)
	refreshRange := rangeValue
	refreshRange.From = previous.Start
	result, err := analytics.NewService(s.db).Refresh(ctx, auth.ActiveRestaurantID, refreshRange)
	if err != nil {
		log.Printf("analytics refresh failed restaurant_id=%d: %v", auth.ActiveRestaurantID, err)
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to refresh analytics")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"runId":       result.RunID,
		"rowsWritten": result.RowsWritten,
		"from":        result.From,
		"to":          result.To,
		"mode":        "idempotent_selected_range_rebuild",
		"outbox":      false,
	})
}
