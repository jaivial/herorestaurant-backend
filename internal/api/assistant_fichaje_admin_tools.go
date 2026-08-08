package api

import (
	"context"
	"encoding/json"
)

// --- Fichaje admin writes (reuse backoffice_fichaje.go handlers, confirmation
// on). These punch a member in/out on behalf of an admin, so no DNI/password
// is needed (unlike the self-service clock endpoints). ---

func (s *Server) assistantFichajeAdminStart(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		MemberID int `json:"member_id"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "fichaje_admin_start", s.handleBOFichajeAdminStart, input, assistantHandlerInput{
		Body: map[string]any{"memberId": in.MemberID},
	})
}

func (s *Server) assistantFichajeAdminStop(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		MemberID int `json:"member_id"`
	}
	_ = json.Unmarshal(input, &in)
	return s.assistantConfirmedMutation(ctx, rid, "fichaje_admin_stop", s.handleBOFichajeAdminStop, input, assistantHandlerInput{
		Body: map[string]any{"memberId": in.MemberID},
	})
}
