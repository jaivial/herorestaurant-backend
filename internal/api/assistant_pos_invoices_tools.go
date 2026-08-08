package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// --- POS typed reads (reuse backoffice_pos_*.go handlers) ---

func (s *Server) assistantPOSVisitsList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if strings.TrimSpace(in.Status) != "" {
		q["status"] = in.Status
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOPOSVisitsList, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("pos_visits_list", body, code)
	}
	return botHandlerResponse("pos_visits_list", body)
}

func (s *Server) assistantPOSTicketsList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if strings.TrimSpace(in.Status) != "" {
		q["status"] = in.Status
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOPOSTicketsList, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("pos_tickets_list", body, code)
	}
	return botHandlerResponse("pos_tickets_list", body)
}

func (s *Server) assistantPOSCashClosuresList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ShiftID int64 `json:"shift_id"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.ShiftID > 0 {
		q["shiftId"] = strconv.FormatInt(in.ShiftID, 10)
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOPOSCashClosures, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("pos_cash_closures_list", body, code)
	}
	return botHandlerResponse("pos_cash_closures_list", body)
}

func (s *Server) assistantPOSCashSummary(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ShiftID int64 `json:"shift_id"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.ShiftID > 0 {
		q["shiftId"] = strconv.FormatInt(in.ShiftID, 10)
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOPOSCashSummary, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("pos_cash_summary", body, code)
	}
	return botHandlerResponse("pos_cash_summary", body)
}

// --- Invoices typed reads (reuse backoffice_invoices.go handlers) ---

func (s *Server) assistantInvoicesList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Search   string `json:"search"`
		Status   string `json:"status"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
		Page     int    `json:"page"`
		Limit    int    `json:"limit"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.Search != "" {
		q["search"] = in.Search
	}
	if in.Status != "" {
		q["status"] = in.Status
	}
	if in.DateFrom != "" {
		q["date_from"] = in.DateFrom
	}
	if in.DateTo != "" {
		q["date_to"] = in.DateTo
	}
	if in.Page > 0 {
		q["page"] = strconv.Itoa(in.Page)
	}
	if in.Limit > 0 {
		q["limit"] = strconv.Itoa(in.Limit)
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOInvoicesList, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("invoices_list", body, code)
	}
	return botHandlerResponse("invoices_list", body)
}

func (s *Server) assistantInvoiceGet(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(input, &in)
	body, code, err := s.assistantCallHandler(ctx, s.handleBOInvoiceGet, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.Itoa(in.ID)},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("invoice_get", body, code)
	}
	return botHandlerResponse("invoice_get", body)
}

// --- Platform typed reads (reuse backoffice_settings.go handlers) ---

func (s *Server) assistantIntegrationsGet(ctx context.Context, _ int, _ json.RawMessage) (string, error) {
	body, code, err := s.assistantCallHandler(ctx, s.handleBOIntegrationsGet, assistantHandlerInput{})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("integrations_get", body, code)
	}
	return botHandlerResponse("integrations_get", body)
}

func (s *Server) assistantBrandingGet(ctx context.Context, _ int, _ json.RawMessage) (string, error) {
	body, code, err := s.assistantCallHandler(ctx, s.handleBOBrandingGet, assistantHandlerInput{})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("branding_get", body, code)
	}
	return botHandlerResponse("branding_get", body)
}
