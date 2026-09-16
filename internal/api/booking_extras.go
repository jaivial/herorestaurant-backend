package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// coordination id: booking_extras_v1
// (REST extras catalog -> bookings.extras_json -> email/WhatsApp/bot).
//
// Extras are the non-group-menu add-ons a booking can carry. They mirror the
// beverage options pattern: a restaurant-scoped catalog (with custom entries
// created from the extras modal) plus a per-booking JSON snapshot.

type bookingExtra struct {
	ID     int64  `json:"id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Custom bool   `json:"is_custom"`
	Active bool   `json:"active"`
}

func (s *Server) loadRestaurantBookingExtras(restaurantID int) ([]bookingExtra, error) {
	rows, err := s.db.Query(`
		SELECT id, slug, name, is_custom, active
		FROM restaurant_booking_extras
		WHERE restaurant_id = ? AND active = 1
		ORDER BY is_custom ASC, id ASC
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]bookingExtra, 0, 12)
	for rows.Next() {
		var (
			extra  bookingExtra
			custom int
			active int
		)
		if err := rows.Scan(&extra.ID, &extra.Slug, &extra.Name, &custom, &active); err != nil {
			return nil, err
		}
		extra.Custom = custom != 0
		extra.Active = active != 0
		out = append(out, extra)
	}
	return out, rows.Err()
}

func (s *Server) createRestaurantBookingExtra(restaurantID int, name string) (bookingExtra, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return bookingExtra{}, sql.ErrNoRows
	}
	slug := beverageOptionSlug(name)
	if slug == "" {
		return bookingExtra{}, sql.ErrNoRows
	}
	_, err := s.db.Exec(`
		INSERT INTO restaurant_booking_extras (restaurant_id, slug, name, is_custom, active)
		VALUES (?, ?, ?, 1, 1)
		ON DUPLICATE KEY UPDATE name = VALUES(name), active = 1
	`, restaurantID, slug, name)
	if err != nil {
		return bookingExtra{}, err
	}
	var extra bookingExtra
	var custom, active int
	err = s.db.QueryRow(`
		SELECT id, slug, name, is_custom, active
		FROM restaurant_booking_extras
		WHERE restaurant_id = ? AND slug = ? LIMIT 1
	`, restaurantID, slug).Scan(&extra.ID, &extra.Slug, &extra.Name, &custom, &active)
	extra.Custom = custom != 0
	extra.Active = active != 0
	return extra, err
}

func (s *Server) deleteRestaurantBookingExtra(restaurantID int, extraID int64) error {
	_, err := s.db.Exec(`
		UPDATE restaurant_booking_extras
		SET active = 0
		WHERE id = ? AND restaurant_id = ? AND is_custom = 1
	`, extraID, restaurantID)
	return err
}

// resolveBookingExtras maps the selected ids to the restaurant's active extras,
// dropping unknown/inactive ids and de-duplicating while keeping catalog order.
func (s *Server) resolveBookingExtras(restaurantID int, ids []int64) ([]bookingExtra, error) {
	if len(ids) == 0 {
		return []bookingExtra{}, nil
	}
	all, err := s.loadRestaurantBookingExtras(restaurantID)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id > 0 {
			wanted[id] = true
		}
	}
	out := make([]bookingExtra, 0, len(wanted))
	for _, extra := range all {
		if wanted[extra.ID] {
			out = append(out, extra)
		}
	}
	return out, nil
}

// bookingExtrasSnapshotJSON stores the selected extras as a JSON array of
// {id, slug, name}. Returns nil for an empty selection so the column stays
// NULL and notifications can skip it.
func bookingExtrasSnapshotJSON(extras []bookingExtra) any {
	if len(extras) == 0 {
		return nil
	}
	type snapshot struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	rows := make([]snapshot, 0, len(extras))
	for _, extra := range extras {
		rows = append(rows, snapshot{ID: extra.ID, Slug: extra.Slug, Name: extra.Name})
	}
	return mustJSON(rows, []snapshot{})
}

// parseBookingExtrasSnapshot decodes the stored snapshot into a list of maps so
// the booking API can return it to the editor.
func parseBookingExtrasSnapshot(raw string) []map[string]any {
	raw = strings.TrimSpace(raw)
	out := []map[string]any{}
	if raw == "" || raw == "null" {
		return out
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return out
	}
	for _, row := range rows {
		name := strings.TrimSpace(anyToString(row["name"]))
		if name == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":   row["id"],
			"slug": strings.TrimSpace(anyToString(row["slug"])),
			"name": name,
		})
	}
	return out
}

// bookingExtraNames returns the names from a stored snapshot (or a plain JSON
// array of strings), used by the notification templates and the bot.
func bookingExtraNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	out := []string{}
	if raw == "" || raw == "null" {
		return out
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err == nil {
		for _, row := range rows {
			if name := strings.TrimSpace(anyToString(row["name"])); name != "" {
				out = append(out, name)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	var plain []string
	if err := json.Unmarshal([]byte(raw), &plain); err == nil {
		for _, name := range plain {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

// --- REST endpoints ---------------------------------------------------------

func (s *Server) handleBOBookingExtrasList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	extras, err := s.loadRestaurantBookingExtras(a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando extras")
		return
	}
	echoCorrelationID(w, r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "extras": extras})
}

func (s *Server) handleBOBookingExtrasCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSONBody(r, &in); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	extra, err := s.createRestaurantBookingExtra(a.ActiveRestaurantID, in.Name)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se pudo crear el extra"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "extra": extra})
}

func (s *Server) handleBOBookingExtrasDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	extraID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || extraID <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid extra id"})
		return
	}
	if err := s.deleteRestaurantBookingExtra(a.ActiveRestaurantID, extraID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error eliminando el extra")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// bookingExtrasFromMap extracts the selected extra names from a booking payload
// map, tolerating the several shapes it arrives in: []string (notification
// data), []any (JSON-decoded) or a JSON string (raw extras_json).
func bookingExtrasFromMap(booking map[string]any) []string {
	raw, ok := booking["extras"]
	if !ok || raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case []string:
		out := make([]string, 0, len(value))
		for _, name := range value {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			switch entry := item.(type) {
			case string:
				if trimmed := strings.TrimSpace(entry); trimmed != "" {
					out = append(out, trimmed)
				}
			case map[string]any:
				if trimmed := strings.TrimSpace(anyToString(entry["name"])); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	case string:
		return bookingExtraNames(value)
	default:
		return nil
	}
}

// botToolListBookingExtras exposes the restaurant extras catalog to the
// WhatsApp bot so it can answer what add-ons a non-group-menu booking can have.
// Coordination id: booking_extras_v1 (catalog -> bot tool).
func (s *Server) botToolListBookingExtras(ctx context.Context, restaurantID int) (string, error) {
	extras, err := s.loadRestaurantBookingExtras(restaurantID)
	if err != nil {
		return botJSON(map[string]any{"error": "error consultando los extras"}), nil
	}
	out := make([]map[string]any, 0, len(extras))
	for _, extra := range extras {
		out = append(out, map[string]any{
			"extra_id": extra.ID,
			"slug":     extra.Slug,
			"name":     extra.Name,
		})
	}
	return botJSON(map[string]any{"count": len(out), "extras": out}), nil
}
