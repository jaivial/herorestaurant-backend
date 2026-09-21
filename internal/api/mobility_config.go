package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// "Problemas de movilidad" question toggle for ANY day. The global flag lives
// on restaurant_reservation_defaults.mobility_enabled; per-day overrides in
// mobility_day_override (NULL column = inherit the global default), mirroring
// the location_booking_override pattern (location_booking.go).
//
// Coordination id: mobility_day_override_v1

// loadMobilityDayOverride returns the raw per-day override; NULL = inherit.
func (s *Server) loadMobilityDayOverride(ctx context.Context, restaurantID int, date string) (sql.NullInt64, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT mobility_enabled
		FROM mobility_day_override
		WHERE restaurant_id = ? AND reservationDate = ?
		LIMIT 1
	`, restaurantID, date).Scan(&v)
	if err == sql.ErrNoRows {
		return sql.NullInt64{}, nil
	}
	return v, err
}

// mobilityDayState returns the raw override (NULL = inherit) plus the resolved
// flag: the override when explicitly set, otherwise the global default.
func (s *Server) mobilityDayState(ctx context.Context, restaurantID int, date string) (sql.NullInt64, bool, error) {
	defaults, err := s.loadReservationDefaults(ctx, restaurantID)
	if err != nil {
		return sql.NullInt64{}, false, err
	}
	override, err := s.loadMobilityDayOverride(ctx, restaurantID, date)
	if err != nil {
		return sql.NullInt64{}, false, err
	}
	if override.Valid {
		return override, override.Int64 != 0, nil
	}
	return override, defaults.MobilityEnabled, nil
}

// resolveMobilityEnabled returns the effective mobility question flag for a
// date: mobility_day_override.mobility_enabled when the row sets it, else
// restaurant_reservation_defaults.mobility_enabled. Every read path resolves
// through this helper (single source of truth).
//
// Coordination id: mobility_day_override_v1
func (s *Server) resolveMobilityEnabled(restaurantID int, date string) (bool, error) {
	_, effective, err := s.mobilityDayState(context.Background(), restaurantID, date)
	return effective, err
}

// mobilityDayResponse renders GET/POST /admin/config/mobility-day: the stored
// override (null = inherits the global default) plus the resolved flag.
func mobilityDayResponse(date string, override sql.NullInt64, effective bool) map[string]any {
	var stored any
	if override.Valid {
		stored = override.Int64 != 0
	}
	return map[string]any{
		"success":          true,
		"date":             date,
		"mobility_enabled": stored,
		"effective":        effective,
	}
}

// handleBOConfigMobilityDayGet returns the per-day override (null = inherit)
// and the effective mobility flag for a date.
//
// Coordination id: mobility_day_override_v1
func (s *Server) handleBOConfigMobilityDayGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}

	override, effective, err := s.mobilityDayState(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de movilidad")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, mobilityDayResponse(date, override, effective))
}

type boConfigMobilityDaySetRequest struct {
	Date            string `json:"date"`
	MobilityEnabled *bool  `json:"mobility_enabled"`
}

// handleBOConfigMobilityDaySet stores/updates the per-day override and returns
// the same shape as the GET.
//
// Coordination id: mobility_day_override_v1
func (s *Server) handleBOConfigMobilityDaySet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boConfigMobilityDaySetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}
	if req.MobilityEnabled == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Nada que actualizar"})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO mobility_day_override (restaurant_id, reservationDate, mobility_enabled)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE mobility_enabled = VALUES(mobility_enabled)
	`, a.ActiveRestaurantID, date, boolToInt(*req.MobilityEnabled)); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando configuración de movilidad")
		return
	}

	override, effective, err := s.mobilityDayState(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de movilidad")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, mobilityDayResponse(date, override, effective))
}
