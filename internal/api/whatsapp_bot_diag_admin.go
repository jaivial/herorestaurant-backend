package api

import (
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/diag"
	"preactvillacarmen/internal/httpx"
)

// Coordination id: wa_bot_diag_endpoint_v1
//
// GET /api/admin/bot/diag — read-only dump of the in-memory diagnostic ring
// buffer (package internal/diag, wa_diag_pkg_v1) so a whole bot turn can be
// reconstructed in prod (kraken) without touching the container: query by the
// correlation id of the turn (the bot session id, "wa-<restaurant>-<phone>",
// the same id printed in every line as correlation_id=...).
//
// Access control mirrors the root-only bot IA tab (bot settings, AI routing):
// the buffer can contain bounded fragments of customer conversation, so it
// gets the same gating as the rest of the bot diagnostics, never less.
//
// Parameters:
//   - coordination=<id>  exact correlation id (e.g. wa-5-34600000000)
//   - q=<substring>      free grep over the rendered line (e.g. wa_bot_tool_result_v1)
//   - limit=<n>          max events returned (default 500)
//   - capacity=<n>       optional runtime resize of the ring (operator lever)
//   - format=text        plain-text lines instead of JSON
func (s *Server) handleBOBotDiagGet(w http.ResponseWriter, r *http.Request) {
	if v := strings.TrimSpace(r.URL.Query().Get("capacity")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			diag.SetCapacity(n)
		}
	}
	limit := 500
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	coordination := strings.TrimSpace(r.URL.Query().Get("coordination"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	events := diag.Snapshot()
	if coordination != "" {
		events = diag.Coordination(coordination)
	}
	if q != "" {
		filtered := events[:0]
		for _, e := range events {
			if strings.Contains(e.Line(), q) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}
	returned := len(events)
	if limit < len(events) {
		events = events[len(events)-limit:] // newest slice
	}

	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		var b strings.Builder
		for _, e := range events {
			b.WriteString(e.Time.Format("2006-01-02T15:04:05.000Z07:00"))
			b.WriteString(" ")
			b.WriteString(e.Line())
			b.WriteString("\n")
		}
		_, _ = w.Write([]byte(b.String()))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"endpoint":     "wa_bot_diag_endpoint_v1",
		"capacity":     diag.Capacity(),
		"buffered":     len(diag.Snapshot()),
		"returned":     returned,
		"coordination": coordination,
		"q":            q,
		"events":       events,
	})
}
