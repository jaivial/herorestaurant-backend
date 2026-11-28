package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/aiusage"
	"preactvillacarmen/internal/httpx"
)

// Coordination id: ai_usage_stats_api_v1 / whatsapp_message_logs_api_v1.
// Both handlers feed the Estadísticas > IA y WhatsApp tab and Forky's
// ai_usage_report / whatsapp_messages_list tools.

const (
	messagingGranularityWeek  = "week"
	messagingGranularityMonth = "month"
	messagingGranularityYear  = "year"
)

type aiUsageSummary struct {
	Calls           int64   `json:"calls"`
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
	CostUSD         float64 `json:"costUsd"`
	UnpricedCalls   int64   `json:"unpricedCalls"`
}

type aiUsagePeriod struct {
	Period          string  `json:"period"`
	Calls           int64   `json:"calls"`
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
	CostUSD         float64 `json:"costUsd"`
}

type aiUsageModelRow struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Calls           int64   `json:"calls"`
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
	CostUSD         float64 `json:"costUsd"`
	Priced          bool    `json:"priced"`
}

type aiUsageFeatureRow struct {
	Feature         string  `json:"feature"`
	Calls           int64   `json:"calls"`
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
	CostUSD         float64 `json:"costUsd"`
}

type aiUsageReport struct {
	Summary   aiUsageSummary      `json:"summary"`
	Series    []aiUsagePeriod     `json:"series"`
	ByModel   []aiUsageModelRow   `json:"byModel"`
	ByFeature []aiUsageFeatureRow `json:"byFeature"`
	Pricing   []aiusage.Pricing   `json:"pricing"`
}

type whatsappLogRow struct {
	ID          int64  `json:"id"`
	Direction   string `json:"direction"`
	MessageType string `json:"messageType"`
	UserPhone   string `json:"userPhone"`
	Content     string `json:"content"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	ToolName    string `json:"toolName"`
	CreatedAt   string `json:"createdAt"`
}

type whatsappLogPeriod struct {
	Period   string `json:"period"`
	Inbound  int64  `json:"inbound"`
	Outbound int64  `json:"outbound"`
	Total    int64  `json:"total"`
}

type whatsappLogSummary struct {
	Total    int64 `json:"total"`
	Inbound  int64 `json:"inbound"`
	Outbound int64 `json:"outbound"`
	Failed   int64 `json:"failed"`
}

type whatsappLogReport struct {
	Summary whatsappLogSummary  `json:"summary"`
	Series  []whatsappLogPeriod `json:"series"`
	Logs    []whatsappLogRow    `json:"logs"`
	Page    int                 `json:"page"`
	Limit   int                 `json:"limit"`
	HasMore bool                `json:"hasMore"`
}

func parseMessagingGranularity(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", messagingGranularityMonth:
		return messagingGranularityMonth, nil
	case messagingGranularityWeek:
		return messagingGranularityWeek, nil
	case messagingGranularityYear:
		return messagingGranularityYear, nil
	default:
		return "", fmt.Errorf("granularity must be week, month or year")
	}
}

func parseMessagingRange(fromValue, toValue string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := to.AddDate(0, 0, -29)
	if strings.TrimSpace(fromValue) != "" {
		parsed, err := parseAnalyticsDate(fromValue)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = parsed
	}
	if strings.TrimSpace(toValue) != "" {
		parsed, err := parseAnalyticsDate(toValue)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = parsed
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("from must not be after to")
	}
	if to.Sub(from) > 5*365*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("date range is too large")
	}
	return from, to.AddDate(0, 0, 1), nil
}

func messagingBucketExpr(granularity string) string {
	switch granularity {
	case messagingGranularityWeek:
		return "DATE_FORMAT(DATE_SUB(created_at, INTERVAL WEEKDAY(created_at) DAY), '%Y-%m-%d')"
	case messagingGranularityYear:
		return "DATE_FORMAT(created_at, '%Y-01-01')"
	default:
		return "DATE_FORMAT(created_at, '%Y-%m-01')"
	}
}

func (s *Server) aiUsageReport(ctx context.Context, restaurantID int, from, to time.Time, granularity string) (aiUsageReport, error) {
	out := aiUsageReport{
		Series:    []aiUsagePeriod{},
		ByModel:   []aiUsageModelRow{},
		ByFeature: []aiUsageFeatureRow{},
		Pricing:   aiusage.Catalog(),
	}
	if s == nil || s.db == nil || restaurantID <= 0 {
		return out, fmt.Errorf("restaurante activo no disponible")
	}
	if err := s.db.QueryRowContext(ctx, portableSQL(`SELECT COUNT(*),
            COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
            COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_usd),0),
            COALESCE(SUM(CASE WHEN priced=0 THEN 1 ELSE 0 END),0)
        FROM ai_usage_logs WHERE restaurant_id=? AND created_at>=? AND created_at<?`),
		restaurantID, from, to).Scan(
		&out.Summary.Calls, &out.Summary.InputTokens, &out.Summary.OutputTokens,
		&out.Summary.CacheReadTokens, &out.Summary.CostUSD, &out.Summary.UnpricedCalls); err != nil {
		return out, err
	}

	bucket := sqlFragment(messagingBucketExpr(granularity), messagingBucketExprPG(granularity))
	rows, err := s.db.QueryContext(ctx, portableSQL(`SELECT `+bucket+` period,
            COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
            COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_usd),0)
        FROM ai_usage_logs WHERE restaurant_id=? AND created_at>=? AND created_at<?
        GROUP BY period ORDER BY period`), restaurantID, from, to)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row aiUsagePeriod
		if err := rows.Scan(&row.Period, &row.Calls, &row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CostUSD); err != nil {
			rows.Close()
			return out, err
		}
		out.Series = append(out.Series, row)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, portableSQL(`SELECT provider, model, COUNT(*),
            COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
            COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_usd),0),
            COALESCE(SUM(CASE WHEN priced=0 THEN 0 ELSE 1 END),0)
        FROM ai_usage_logs WHERE restaurant_id=? AND created_at>=? AND created_at<?
        GROUP BY provider, model ORDER BY SUM(cost_usd) DESC, COUNT(*) DESC`), restaurantID, from, to)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row aiUsageModelRow
		var priced int64
		if err := rows.Scan(&row.Provider, &row.Model, &row.Calls, &row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CostUSD, &priced); err != nil {
			rows.Close()
			return out, err
		}
		row.Priced = priced > 0
		out.ByModel = append(out.ByModel, row)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, portableSQL(`SELECT feature, COUNT(*),
            COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
            COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cost_usd),0)
        FROM ai_usage_logs WHERE restaurant_id=? AND created_at>=? AND created_at<?
        GROUP BY feature ORDER BY SUM(cost_usd) DESC, COUNT(*) DESC`), restaurantID, from, to)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row aiUsageFeatureRow
		if err := rows.Scan(&row.Feature, &row.Calls, &row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CostUSD); err != nil {
			rows.Close()
			return out, err
		}
		out.ByFeature = append(out.ByFeature, row)
	}
	rows.Close()

	return out, rows.Err()
}

func (s *Server) whatsappLogReport(ctx context.Context, restaurantID int, from, to time.Time, granularity, direction, search string, page, limit int) (whatsappLogReport, error) {
	out := whatsappLogReport{
		Series: []whatsappLogPeriod{},
		Logs:   []whatsappLogRow{},
		Page:   page,
		Limit:  limit,
	}
	if s == nil || s.db == nil || restaurantID <= 0 {
		return out, fmt.Errorf("restaurante activo no disponible")
	}
	// wa_bot_store_pg_mysql_v1: the log lives in the bot store on PG.
	st := s.botStateDB()
	var fromArg, toArg any = from, to
	tsArg, like := "?", "LIKE"
	if st.Postgres {
		fromArg, toArg = from.UTC().Format("2006-01-02 15:04:05.000"), to.UTC().Format("2006-01-02 15:04:05.000")
		tsArg, like = "CAST(CAST(? AS text) AS timestamp(3))", "LIKE"
	}
	if st.DB == nil {
		return out, fmt.Errorf("restaurante activo no disponible")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if page <= 0 {
		page = 1
	}

	where := []string{"restaurant_id=?", "created_at>=" + tsArg, "created_at<" + tsArg}
	args := []any{restaurantID, fromArg, toArg}
	if direction == "inbound" || direction == "outbound" {
		where = append(where, "direction=?")
		args = append(args, direction)
	}
	if term := strings.TrimSpace(search); term != "" {
		if st.Postgres {
			// repair-wiring2: public columns are old-nondeterministic-collation (nondeterministic): PG
			// rejects (I)LIKE there; case-fold explicitly under "C" as the other
			// ported searches do. Accent folding of MySQL ci LIKE stays gated.
			where = append(where, `(LOWER(user_phone) COLLATE "C" LIKE LOWER(?) OR LOWER(content) COLLATE "C" LIKE LOWER(?))`)
		} else {
			where = append(where, "(user_phone "+like+" ? OR content "+like+" ?)")
		}
		like := "%" + term + "%"
		args = append(args, like, like)
	}
	clause := strings.Join(where, " AND ")

	if err := st.QueryRowContext(ctx, selectedSQL(`SELECT COUNT(*),
            COALESCE(SUM(CASE WHEN direction='inbound' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN direction='outbound' THEN 1 ELSE 0 END),0),
            COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0)
        FROM whatsapp_message_logs WHERE `+clause), args...).Scan(
		&out.Summary.Total, &out.Summary.Inbound, &out.Summary.Outbound, &out.Summary.Failed); err != nil {
		return out, err
	}

	bucket := messagingBucketExpr(granularity)
	if st.Postgres {
		bucket = messagingBucketExprPG(granularity)
	}
	seriesArgs := append([]any{}, args...)
	rows, err := st.QueryContext(ctx, selectedSQL(`SELECT `+bucket+` period,
            COALESCE(SUM(CASE WHEN direction='inbound' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN direction='outbound' THEN 1 ELSE 0 END),0), COUNT(*)
        FROM whatsapp_message_logs WHERE `+clause+` GROUP BY period ORDER BY period`), seriesArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row whatsappLogPeriod
		if err := rows.Scan(&row.Period, &row.Inbound, &row.Outbound, &row.Total); err != nil {
			rows.Close()
			return out, err
		}
		out.Series = append(out.Series, row)
	}
	rows.Close()

	listArgs := append(append([]any{}, args...), limit+1, (page-1)*limit)
	createdFmt := "DATE_FORMAT(created_at, '%Y-%m-%dT%H:%i:%sZ')"
	if st.Postgres {
		createdFmt = `to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	}
	rows, err = st.QueryContext(ctx, selectedSQL(`SELECT id,direction,message_type,user_phone,content,source,status,tool_name,
            `+createdFmt+`
        FROM whatsapp_message_logs WHERE `+clause+`
        ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`), listArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row whatsappLogRow
		if err := rows.Scan(&row.ID, &row.Direction, &row.MessageType, &row.UserPhone, &row.Content,
			&row.Source, &row.Status, &row.ToolName, &row.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Logs = append(out.Logs, row)
	}
	rows.Close()
	if len(out.Logs) > limit {
		out.Logs = out.Logs[:limit]
		out.HasMore = true
	}
	return out, rows.Err()
}

// messagingBucketExprPG mirrors messagingBucketExpr on PG (ISO weeks start
// on Monday, like MySQL WEEKDAY) (wa_bot_store_pg_mysql_v1).
func messagingBucketExprPG(granularity string) string {
	switch granularity {
	case messagingGranularityWeek:
		return "to_char(date_trunc('week', created_at), 'YYYY-MM-DD')"
	case messagingGranularityYear:
		return "to_char(created_at, 'YYYY-01-01')"
	default:
		return "to_char(created_at, 'YYYY-MM-01')"
	}
}

// GET /api/admin/analytics/ai-usage?from=&to=&granularity=
func (s *Server) handleBOAIUsage(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	granularity, err := parseMessagingGranularity(r.URL.Query().Get("granularity"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to, err := parseMessagingRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	report, err := s.aiUsageReport(ctx, auth.ActiveRestaurantID, from, to, granularity)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load AI usage")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"from":        from.Format("2006-01-02"),
		"to":          to.AddDate(0, 0, -1).Format("2006-01-02"),
		"granularity": granularity,
		"summary":     report.Summary,
		"series":      report.Series,
		"byModel":     report.ByModel,
		"byFeature":   report.ByFeature,
		"pricing":     report.Pricing,
	})
}

// GET /api/admin/analytics/whatsapp-messages?from=&to=&granularity=&direction=&search=&page=&limit=
func (s *Server) handleBOWhatsAppMessages(w http.ResponseWriter, r *http.Request) {
	auth, ok := boAuthFromContext(r.Context())
	if !ok || auth.ActiveRestaurantID <= 0 {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	granularity, err := parseMessagingGranularity(r.URL.Query().Get("granularity"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to, err := parseMessagingRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	report, err := s.whatsappLogReport(ctx, auth.ActiveRestaurantID, from, to, granularity,
		strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction"))),
		r.URL.Query().Get("search"), page, limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Unable to load WhatsApp messages")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"from":        from.Format("2006-01-02"),
		"to":          to.AddDate(0, 0, -1).Format("2006-01-02"),
		"granularity": granularity,
		"summary":     report.Summary,
		"series":      report.Series,
		"logs":        report.Logs,
		"page":        report.Page,
		"limit":       report.Limit,
		"hasMore":     report.HasMore,
	})
}
