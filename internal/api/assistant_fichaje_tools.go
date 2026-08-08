package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// assistantFichajeStateGet returns the fichaje state for the active
// restaurant, scoped to the authenticated backoffice user's clock member
// (member, active entry, today's schedule and active-entries list).
func (s *Server) assistantFichajeStateGet(ctx context.Context, rid int, _ json.RawMessage) (string, error) {
	auth, ok := boAuthFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("autenticación requerida")
	}
	if auth.ActiveRestaurantID != rid {
		return "", fmt.Errorf("restaurante activo no disponible")
	}
	st, err := s.buildBOFichajeState(ctx, auth)
	if err != nil {
		return "", err
	}
	return botJSON(map[string]any{"tool": "fichaje_state_get", "state": st}), nil
}

// assistantFichajeEntriesList returns time entries for a member (default: the
// caller's clock member) and an optional date (default: today).
func (s *Server) assistantFichajeEntriesList(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		Date     string `json:"date"`
		MemberID int    `json:"member_id"`
		Limit    int    `json:"limit"`
	}
	_ = json.Unmarshal(input, &in)
	memberID := in.MemberID
	if memberID <= 0 {
		auth, ok := boAuthFromContext(ctx)
		if !ok {
			return "", fmt.Errorf("autenticación requerida")
		}
		m, err := s.getBOClockMemberForUser(ctx, rid, auth.User.ID, "")
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return botJSON(map[string]any{"tool": "fichaje_entries_list", "member_id": 0, "entries": []any{}}), nil
			}
			return "", err
		}
		memberID = m.ID
	}
	date := strings.TrimSpace(in.Date)
	if date == "" {
		date = boTodayDate().Format("2006-01-02")
	}
	entries, err := s.listBOTimeEntriesByMemberAndDate(ctx, rid, memberID, date)
	if err != nil {
		return "", err
	}
	limit := in.Limit
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return botJSON(map[string]any{"tool": "fichaje_entries_list", "member_id": memberID, "date": date, "entries": entries}), nil
}
