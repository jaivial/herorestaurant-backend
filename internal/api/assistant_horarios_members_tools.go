package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- Horarios reads (reuse backoffice_fichaje.go handlers) ---

// assistantSchedulesByDate lists all work schedules for the active restaurant
// on a date (default today).
func (s *Server) assistantSchedulesByDate(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	body, code, err := s.assistantCallHandler(ctx, s.handleBOHorariosList, assistantHandlerInput{
		Query: map[string]string{"date": in.Date},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("schedules_by_date", body, code)
	}
	return botHandlerResponse("schedules_by_date", body)
}

// assistantSchedulesMonth lists schedule coverage for the active restaurant in
// a given year/month (default: current month).
func (s *Server) assistantSchedulesMonth(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Year  int `json:"year"`
		Month int `json:"month"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.Year > 0 {
		q["year"] = strconv.Itoa(in.Year)
	}
	if in.Month > 0 {
		q["month"] = strconv.Itoa(in.Month)
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOHorariosMonth, assistantHandlerInput{Query: q})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("schedules_month", body, code)
	}
	return botHandlerResponse("schedules_month", body)
}

// --- Miembros reads (reuse backoffice_members.go handlers) ---

// assistantMembersList lists active members of the active restaurant.
func (s *Server) assistantMembersList(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	_ = input
	body, code, err := s.assistantCallHandler(ctx, s.handleBOMembersList, assistantHandlerInput{})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("members_list", body, code)
	}
	return botHandlerResponse("members_list", body)
}

// assistantMemberGet returns one member of the active restaurant by id.
func (s *Server) assistantMemberGet(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(input, &in)
	body, code, err := s.assistantCallHandler(ctx, s.handleBOMemberGet, assistantHandlerInput{
		URLParam: map[string]string{"id": strconv.Itoa(in.ID)},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("member_get", body, code)
	}
	return botHandlerResponse("member_get", body)
}

// assistantMemberBalanceGet returns the quarterly balance of one member
// (estado de cuenta).
func (s *Server) assistantMemberBalanceGet(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		ID   int    `json:"id"`
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	q := map[string]string{}
	if in.Date != "" {
		q["date"] = in.Date
	}
	body, code, err := s.assistantCallHandler(ctx, s.handleBOMemberQuarterBalance, assistantHandlerInput{
		Query:    q,
		URLParam: map[string]string{"id": strconv.Itoa(in.ID)},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("member_balance_get", body, code)
	}
	return botHandlerResponse("member_balance_get", body)
}
