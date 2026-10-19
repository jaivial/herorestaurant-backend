package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Forky custom tools for the Estadísticas > IA y WhatsApp tab. They reuse the
// same aggregation as the HTTP handlers so the assistant and the panel can
// never disagree. Coordination id: forky_messaging_tools_v1.

func (s *Server) assistantAIUsageReport(ctx context.Context, rid int, raw json.RawMessage) (string, error) {
	var in struct {
		Granularity string `json:"granularity"`
		DateFrom    string `json:"date_from"`
		DateTo      string `json:"date_to"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	granularity, err := parseMessagingGranularity(in.Granularity)
	if err != nil {
		return "", err
	}
	from, to, err := parseMessagingRange(in.DateFrom, in.DateTo)
	if err != nil {
		return "", err
	}
	report, err := s.aiUsageReport(ctx, rid, from, to, granularity)
	if err != nil {
		return "", err
	}
	return botJSON(map[string]any{
		"granularity": granularity,
		"from":        from.Format("2006-01-02"),
		"to":          to.AddDate(0, 0, -1).Format("2006-01-02"),
		"summary":     report.Summary,
		"series":      report.Series,
		"by_model":    report.ByModel,
		"by_feature":  report.ByFeature,
		"pricing":     report.Pricing,
		"note":        "Costes en USD. DeepSeek V4.1 Flash aplica tarifa peak/off-peak (01:00-04:00 y 06:00-10:00 UTC, L-V); MiniMax-M3.1-Flash es tarifa plana.",
	}), nil
}

func (s *Server) assistantWhatsAppMessagesList(ctx context.Context, rid int, raw json.RawMessage) (string, error) {
	var in struct {
		Granularity string `json:"granularity"`
		DateFrom    string `json:"date_from"`
		DateTo      string `json:"date_to"`
		Direction   string `json:"direction"`
		Search      string `json:"search"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	granularity, err := parseMessagingGranularity(in.Granularity)
	if err != nil {
		return "", err
	}
	from, to, err := parseMessagingRange(in.DateFrom, in.DateTo)
	if err != nil {
		return "", err
	}
	direction := strings.ToLower(strings.TrimSpace(in.Direction))
	if direction != "" && direction != "inbound" && direction != "outbound" {
		return "", fmt.Errorf("direction must be inbound or outbound")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	report, err := s.whatsappLogReport(ctx, rid, from, to, granularity, direction, in.Search, 1, limit)
	if err != nil {
		return "", err
	}
	return botJSON(map[string]any{
		"granularity": granularity,
		"from":        from.Format("2006-01-02"),
		"to":          to.AddDate(0, 0, -1).Format("2006-01-02"),
		"summary":     report.Summary,
		"series":      report.Series,
		"messages":    report.Logs,
		"has_more":    report.HasMore,
	}), nil
}
