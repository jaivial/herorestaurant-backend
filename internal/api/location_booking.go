package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Location booking toggles ("Permitir reserva de planta" / "Permitir reserva
// de salón"). Global flags live on restaurant_reservation_defaults (columns
// allow_floor_reservation / allow_salon_reservation); per-date overrides in
// location_booking_override. NULL columns inherit the default.
// Mirrors the hour_split.go resolve/set/clear pattern.

type locationBookingFlag struct {
	Value  bool `json:"value"`
	Global bool `json:"global"`
}

type locationBookingFlags struct {
	Floor locationBookingFlag `json:"floor"`
	Salon locationBookingFlag `json:"salon"`
}

// resolveLocationBooking returns the effective flags for a date plus the
// global values. Missing rows/columns inherit the global value from
// restaurant_reservation_defaults.
func (s *Server) resolveLocationBooking(ctx context.Context, restaurantID int, date string) (locationBookingFlags, error) {
	defaults, err := s.loadReservationDefaults(ctx, restaurantID)
	if err != nil {
		return locationBookingFlags{}, err
	}

	out := locationBookingFlags{
		Floor: locationBookingFlag{Value: defaults.AllowFloorReservation, Global: defaults.AllowFloorReservation},
		Salon: locationBookingFlag{Value: defaults.AllowSalonReservation, Global: defaults.AllowSalonReservation},
	}

	var floorRaw, salonRaw sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT allow_floor_reservation, allow_salon_reservation
		FROM location_booking_override
		WHERE restaurant_id = ? AND reservationDate = ?
		LIMIT 1
	`, restaurantID, date).Scan(&floorRaw, &salonRaw)
	if err == nil {
		if floorRaw.Valid {
			out.Floor.Value = floorRaw.Int64 != 0
		}
		if salonRaw.Valid {
			out.Salon.Value = salonRaw.Int64 != 0
		}
		return out, nil
	}
	if err != sql.ErrNoRows {
		return out, err
	}
	return out, nil
}

// locationBookingOverrideRow is the raw stored override state; NULL = inherit.
type locationBookingOverrideRow struct {
	Floor sql.NullInt64
	Salon sql.NullInt64
	Found bool
}

func (s *Server) loadLocationBookingOverrideRow(ctx context.Context, restaurantID int, date string) (locationBookingOverrideRow, error) {
	var row locationBookingOverrideRow
	err := s.db.QueryRowContext(ctx, `
		SELECT allow_floor_reservation, allow_salon_reservation
		FROM location_booking_override
		WHERE restaurant_id = ? AND reservationDate = ?
		LIMIT 1
	`, restaurantID, date).Scan(&row.Floor, &row.Salon)
	if err == sql.ErrNoRows {
		return row, nil
	}
	if err != nil {
		return row, err
	}
	row.Found = true
	return row, nil
}

// toggleOverrideUpdate expresses one flag write: Set=false leaves the
// column untouched; Set=true with Inherit=true clears it to NULL (inherit);
// Set=true with Inherit=false pins it to Value.
type locationOverrideUpdate struct {
	Set     bool
	Inherit bool
	Value   bool
}

// applyLocationBookingOverride upserts the per-date override. When both
// columns end up NULL the row is deleted so the date fully inherits the
// defaults.
func (s *Server) applyLocationBookingOverride(ctx context.Context, restaurantID int, date string, floor, salon locationOverrideUpdate) error {
	existing, err := s.loadLocationBookingOverrideRow(ctx, restaurantID, date)
	if err != nil {
		return err
	}

	nextFloor, nextSalon := existing.Floor, existing.Salon
	if floor.Set {
		nextFloor = pinOverrideColumn(floor)
	}
	if salon.Set {
		nextSalon = pinOverrideColumn(salon)
	}

	if !nextFloor.Valid && !nextSalon.Valid {
		if !existing.Found {
			return nil
		}
		_, err = s.db.ExecContext(ctx, `
			DELETE FROM location_booking_override
			WHERE restaurant_id = ? AND reservationDate = ?
		`, restaurantID, date)
		return err
	}

	if existing.Found {
		_, err = s.db.ExecContext(ctx, `
			UPDATE location_booking_override
			SET allow_floor_reservation = ?, allow_salon_reservation = ?
			WHERE restaurant_id = ? AND reservationDate = ?
		`, nullBoolToAny(nextFloor), nullBoolToAny(nextSalon), restaurantID, date)
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO location_booking_override (restaurant_id, reservationDate, allow_floor_reservation, allow_salon_reservation)
		VALUES (?, ?, ?, ?)
	`, restaurantID, date, nullBoolToAny(nextFloor), nullBoolToAny(nextSalon))
	return err
}

func pinOverrideColumn(u locationOverrideUpdate) sql.NullInt64 {
	if u.Inherit {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(boolToInt(u.Value)), Valid: true}
}

func nullBoolToAny(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return int(v.Int64)
}

// handleBOConfigLocationBookingGet returns, for a date, the global defaults,
// the per-date overrides (null = inherit) and the effective values.
func (s *Server) handleBOConfigLocationBookingGet(w http.ResponseWriter, r *http.Request) {
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

	flags, override, err := s.locationBookingState(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de ubicación")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, locationBookingResponse(date, flags, override))
}

type boLocationBookingSetRequest struct {
	Date                  string `json:"date"`
	AllowFloorReservation *bool  `json:"allowFloorReservation"`
	AllowSalonReservation *bool  `json:"allowSalonReservation"`
}

// valueToUpdate maps a plain toggle write onto a column update. When the
// requested value matches the restaurant default the override is cleared so
// the date inherits the default (same pattern as hour_split).
func valueToUpdate(requested *bool, global bool) locationOverrideUpdate {
	if requested == nil {
		return locationOverrideUpdate{}
	}
	if *requested == global {
		return locationOverrideUpdate{Set: true, Inherit: true}
	}
	return locationOverrideUpdate{Set: true, Value: *requested}
}

// handleBOConfigLocationBookingSet writes per-date overrides from plain
// toggle switches. Toggling to the same value as the global default clears
// the override (the date inherits the default again).
func (s *Server) handleBOConfigLocationBookingSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boLocationBookingSetRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}
	if req.AllowFloorReservation == nil && req.AllowSalonReservation == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Nada que actualizar"})
		return
	}

	// Resolve the global defaults so equal values can fall back to inherit.
	globalFlags, err := s.resolveLocationBooking(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de ubicación")
		return
	}
	floorUpdate := valueToUpdate(req.AllowFloorReservation, globalFlags.Floor.Global)
	salonUpdate := valueToUpdate(req.AllowSalonReservation, globalFlags.Salon.Global)

	if err := s.applyLocationBookingOverride(r.Context(), a.ActiveRestaurantID, date, floorUpdate, salonUpdate); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando configuración de ubicación")
		return
	}

	flags, override, err := s.locationBookingState(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de ubicación")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, locationBookingResponse(date, flags, override))
}

// locationBookingState resolves effective flags plus the raw override row.
func (s *Server) locationBookingState(ctx context.Context, restaurantID int, date string) (locationBookingFlags, locationBookingOverrideRow, error) {
	flags, err := s.resolveLocationBooking(ctx, restaurantID, date)
	if err != nil {
		return flags, locationBookingOverrideRow{}, err
	}
	override, err := s.loadLocationBookingOverrideRow(ctx, restaurantID, date)
	if err != nil {
		return flags, override, err
	}
	return flags, override, nil
}

func locationBookingResponse(date string, flags locationBookingFlags, override locationBookingOverrideRow) map[string]any {
	var floorOverride, salonOverride any
	if override.Floor.Valid {
		floorOverride = override.Floor.Int64 != 0
	}
	if override.Salon.Valid {
		salonOverride = override.Salon.Int64 != 0
	}
	return map[string]any{
		"success": true,
		"date":    date,
		"global": map[string]any{
			"allowFloorReservation": flags.Floor.Global,
			"allowSalonReservation": flags.Salon.Global,
		},
		"override": map[string]any{
			"allowFloorReservation": floorOverride,
			"allowSalonReservation": salonOverride,
		},
		"effective": map[string]any{
			"allowFloorReservation": flags.Floor.Value,
			"allowSalonReservation": flags.Salon.Value,
		},
	}
}
