package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// --- Horarios writes (reuse backoffice_fichaje.go handlers, confirmation on) ---

// assistantScheduleMutation runs the confirmation+execute flow for a horarios
// write tool, then forwards the confirmed payload to the domain handler.
func (s *Server) assistantScheduleMutation(ctx context.Context, rid int, tool string, handler http.HandlerFunc, input json.RawMessage, urlParam map[string]string, body map[string]any) (string, error) {
	return s.assistantConfirmedMutation(ctx, rid, tool, handler, input, assistantHandlerInput{
		URLParam: urlParam,
		Body:     body,
	})
}

func (s *Server) assistantSchedulesCreate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Date      string `json:"date"`
		MemberID  int    `json:"member_id"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantScheduleMutation(ctx, rid, "schedules_create", s.handleBOHorariosAssign, input, nil, map[string]any{
		"date": in.Date, "memberId": in.MemberID, "startTime": in.StartTime, "endTime": in.EndTime,
	})
}

func (s *Server) assistantSchedulesUpdate(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		ID        int    `json:"id"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantScheduleMutation(ctx, rid, "schedules_update", s.handleBOHorariosUpdate, input, map[string]string{"id": strconv.Itoa(in.ID)}, map[string]any{
		"startTime": in.StartTime, "endTime": in.EndTime,
	})
}

func (s *Server) assistantSchedulesDelete(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantScheduleMutation(ctx, rid, "schedules_delete", s.handleBOHorariosDelete, input, map[string]string{"id": strconv.Itoa(in.ID)}, nil)
}
