package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// WhatsApp bot read-only tools for the special dates feature.
//
// Coordination id: special_booking_v1
//
// Two tools are exposed to the bot LLM:
//   - get_special_date_info:        inspector (date -> full settings)
//   - get_special_date_bookings:    bookings lookup (date -> reservations)
//
// Both tools are tenant-scoped via the restaurantID passed by the executor
// closure, mirroring the check_day_capacity / list_menus convention.

// botToolGetSpecialDateInfo returns the active special-date settings + menus
// for the given date, or a structured error when the date is not active.
func (s *Server) botToolGetSpecialDateInfo(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "parámetros inválidos"}), nil
	}
	date, ok := botNormaliseToolDate(in.Date)
	if !ok {
		return botJSON(map[string]any{"error": "fecha inválida; usa YYYY-MM-DD o dd/MM/yyyy"}), nil
	}
	settings, menus, err := s.loadSpecialDateSettings(ctx, restaurantID, date)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo consultar la fecha especial"}), nil
	}
	if settings == nil {
		return botJSON(map[string]any{"error": "no hay fecha especial activa para " + date}), nil
	}
	menuPayload := make([]map[string]any, 0, len(menus))
	for _, m := range menus {
		entry := map[string]any{
			"special_date_menu_id": m.ID,
			"position":             m.Position,
			"label":                firstNonEmpty(strings.TrimSpace(m.CustomTitle.String), strings.TrimSpace(m.MenuTitle)),
			"unit_price":           m.MenuPrice,
			"is_custom":            !m.MenuID.Valid,
		}
		if m.MenuID.Valid {
			entry["menu_id"] = m.MenuID.Int64
		}
		if m.AdelantoAmount.Valid {
			entry["adelanto_amount"] = m.AdelantoAmount.Float64
		}
		if m.CustomImageURL.Valid && strings.TrimSpace(m.CustomImageURL.String) != "" {
			entry["image_url"] = strings.TrimSpace(m.CustomImageURL.String)
		}
		// Coordination id: special_date_section_menus_v1 - special menus are
		// priced and charged per section, not with a menu-level price.
		if m.MenuID.Valid && m.MenuType == "special" {
			delete(entry, "unit_price")
			delete(entry, "adelanto_amount")
			entry["sections"] = s.loadSpecialDateMenuSections(ctx, restaurantID, m.ID, m.MenuID.Int64, true)
		}
		menuPayload = append(menuPayload, entry)
	}
	payload := map[string]any{
		"date":                     date,
		"title":                    settings.Title,
		"is_active":                settings.IsActive,
		"prereserva_enabled":       settings.PrereservaEnabled,
		"requires_adelanto":        settings.RequiresAdelanto,
		"adelanto_payment_methods": settings.AdelantoPaymentMethods,
		"adelanto_unified":         settings.AdelantoUnified,
		"menus":                    menuPayload,
	}
	if settings.AdelantoUnifiedAmount != nil {
		payload["adelanto_unified_amount"] = *settings.AdelantoUnifiedAmount
	}
	return botJSON(payload), nil
}

// botToolGetSpecialDateBookings lists the special bookings for a date with
// their `special` block + is_prereserva flag.
func (s *Server) botToolGetSpecialDateBookings(ctx context.Context, restaurantID int, input json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "parámetros inválidos"}), nil
	}
	date, ok := botNormaliseToolDate(in.Date)
	if !ok {
		return botJSON(map[string]any{"error": "fecha inválida; usa YYYY-MM-DD o dd/MM/yyyy"}), nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, customer_name, TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
		       party_size, COALESCE(is_special_booking, 0), COALESCE(is_prereserva, 0), COALESCE(special_json, '')
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ? AND COALESCE(is_special_booking, 0) = 1
		ORDER BY reservation_time ASC, id ASC
	`, restaurantID, date)
	if err != nil {
		return botJSON(map[string]any{"error": "no se pudo consultar las reservas especiales"}), nil
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id           int
			customer     string
			timeDisp     string
			party        int
			isSpecial    int
			isPrereserva int
			specialRaw   sql.NullString
		)
		if err := rows.Scan(&id, &customer, &timeDisp, &party, &isSpecial, &isPrereserva, &specialRaw); err != nil {
			return botJSON(map[string]any{"error": "scan error"}), nil
		}
		block := s.buildSpecialBookingResponse(ctx, restaurantID, isSpecial != 0, isPrereserva != 0, specialRaw.String)
		out = append(out, map[string]any{
			"id":               id,
			"customer_name":    customer,
			"reservation_time": timeDisp,
			"party_size":       party,
			"is_prereserva":    isPrereserva != 0,
			"special":          block,
		})
	}
	if err := rows.Err(); err != nil {
		return botJSON(map[string]any{"error": "rows error"}), nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		return anyToString(out[i]["reservation_time"]) < anyToString(out[j]["reservation_time"])
	})
	return botJSON(map[string]any{
		"date":     date,
		"bookings": out,
	}), nil
}

// botNormaliseToolDate accepts both YYYY-MM-DD and dd/MM/yyyy and returns the
// canonical YYYY-MM-DD. Empty input is rejected.
func botNormaliseToolDate(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}

// firstNonEmpty is provided by comida.go.