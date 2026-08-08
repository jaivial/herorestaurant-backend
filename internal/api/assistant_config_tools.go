package api

import (
	"context"
	"encoding/json"
	"strconv"
)

// --- Config writes (reuse legacy form handlers, confirmation on) ---

// assistantBookingLimitsUpdate sets the daily booking limit for a date of the
// active restaurant. Requires confirmation.
func (s *Server) assistantBookingLimitsUpdate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Date       string `json:"date"`
		DailyLimit int    `json:"daily_limit"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "booking_limits_update", s.handleUpdateDailyLimit, input, assistantHandlerInput{
		Form: map[string]string{"date": in.Date, "daily_limit": strconv.Itoa(in.DailyLimit)},
	})
}

// assistantBookingLimitsGet reads the daily booking limit and occupancy for a
// date of the active restaurant.
func (s *Server) assistantBookingLimitsGet(ctx context.Context, _ int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(input, &in)
	body, code, err := s.assistantCallHandler(ctx, s.handleFetchDailyLimit, assistantHandlerInput{
		Query: map[string]string{"date": in.Date},
	})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", assistantHandlerError("booking_limits_get", body, code)
	}
	return botHandlerResponse("booking_limits_get", body)
}
