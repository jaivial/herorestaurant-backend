package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: forky_bot_special_full_v1
//
// PATCH /admin/config/special-dates: the partial write of one special date.
// POST replaces the whole date (every omitted field falls back to its default
// and the menus are rewritten), so a model changing one field had to rebuild
// the entire payload by hand. This handler reads the stored date through the
// GET handler, overlays only the fields present in the body and saves through
// the POST handler: same validation, same transaction, same broadcast.
func (s *Server) handleBOSpecialDatesPatch(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var patch map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &patch) != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	var date string
	_ = json.Unmarshal(patch["date"], &date)
	date = strings.TrimSpace(date)
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}

	body, code, err := s.assistantCallHandler(r.Context(), s.handleBOSpecialDatesGet, assistantHandlerInput{Query: map[string]string{"date": date}})
	var current struct {
		SpecialDate *boSpecialDateSaveRequest `json:"special_date"`
	}
	if err != nil || code != http.StatusOK || json.Unmarshal(body, &current) != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo la fecha especial")
		return
	}
	req := boSpecialDateSaveRequest{}
	if current.SpecialDate != nil {
		req = *current.SpecialDate
	}
	// GET returns the RESOLVED mobility flag: only an explicit patch may turn
	// it into a per-day override.
	req.MobilityEnabled = nil
	// A present menus list replaces the stored one; decoding into the old
	// slice would merge stale fields into the new entries.
	if _, ok := patch["menus"]; ok {
		req.Menus = nil
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Campo inválido: " + err.Error()})
		return
	}
	req.Date = date
	merged, _ := json.Marshal(req)
	fields := make([]string, 0, len(patch))
	for k := range patch {
		fields = append(fields, k)
	}
	slog.Default().Info("special_date.patch", "coord_id", "forky_bot_special_full_v1", "date", date, "created", current.SpecialDate == nil, "fields", strings.Join(fields, ","))

	r.Body = io.NopCloser(bytes.NewReader(merged))
	r.ContentLength = int64(len(merged))
	s.handleBOSpecialDatesSave(w, r)
}
