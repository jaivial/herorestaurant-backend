package api

import (
	"context"
	"database/sql"
)

// execQueryer is satisfied by both *sql.DB and *sql.Tx so occupancy bookkeeping
// can run either standalone or inside an existing transaction.
type execQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// adjustOccupancy applies a signed delta to the per-(date, scope, target)
// headcount in reservation_location_occupancy. Positive deltas upsert; negative
// deltas decrement but never below zero. targetID <= 0 is a no-op.
func (s *Server) adjustOccupancy(ctx context.Context, ex execQueryer, restaurantID int, date string, scope string, targetID int, delta int) error {
	if targetID <= 0 || delta == 0 {
		return nil
	}
	if delta > 0 {
		_, err := ex.ExecContext(ctx, `
			INSERT INTO reservation_location_occupancy (restaurant_id, date, scope, target_id, count)
			VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE count = count + VALUES(count)
		`, restaurantID, date, scope, targetID, delta)
		return err
	}
	_, err := ex.ExecContext(ctx, `
		UPDATE reservation_location_occupancy
		SET count = GREATEST(count + ?, 0)
		WHERE restaurant_id = ? AND date = ? AND scope = ? AND target_id = ?
	`, delta, restaurantID, date, scope, targetID)
	return err
}

// applyBookingLocationOccupancy records a booking's floor/salon headcount.
// sign is +1 on insert / -1 on cancel. The floor target is the canonical
// global floor row for the floor_number (mirrors migration backfill).
func (s *Server) applyBookingLocationOccupancy(ctx context.Context, ex execQueryer, restaurantID int, date string, floorNum any, salonID any, partySize int, sign int) error {
	delta := partySize * sign
	if raw, ok := anyIntValue(floorNum); ok && raw >= 0 {
		floorID, err := s.floorIDByNumber(ctx, ex, restaurantID, int(raw))
		if err != nil {
			return err
		}
		if err := s.adjustOccupancy(ctx, ex, restaurantID, date, "floor", floorID, delta); err != nil {
			return err
		}
	}
	if raw, ok := anyIntValue(salonID); ok && raw > 0 {
		if err := s.adjustOccupancy(ctx, ex, restaurantID, date, "salon", int(raw), delta); err != nil {
			return err
		}
	}
	return nil
}

// applyBookingLocationDelta is used on modify: reconcile old vs new location
// (date/party_size/floor/salon). It subtracts the old contribution and adds
// the new one.
func (s *Server) applyBookingLocationDelta(ctx context.Context, ex execQueryer, restaurantID int, oldDate string, oldFloor, oldSalon any, oldParty int, newDate string, newFloor, newSalon any, newParty int) error {
	if err := s.applyBookingLocationOccupancy(ctx, ex, restaurantID, oldDate, oldFloor, oldSalon, oldParty, -1); err != nil {
		return err
	}
	return s.applyBookingLocationOccupancy(ctx, ex, restaurantID, newDate, newFloor, newSalon, newParty, 1)
}

// floorIDByNumber resolves a floor_number to its canonical (global) floor row
// id for the occupancy ledger.
func (s *Server) floorIDByNumber(ctx context.Context, ex execQueryer, restaurantID int, floorNumber int) (int, error) {
	var id int
	err := ex.QueryRowContext(ctx, `
		SELECT id FROM restaurant_floors
		WHERE restaurant_id = ? AND floor_number = ? AND specific_date IS NULL
		LIMIT 1
	`, restaurantID, floorNumber).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// anyIntValue extracts an int from the boxed values used for optional booking
// columns (which may be nil, int, int64, or a custom NullInt64 alias).
func anyIntValue(v any) (int64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case int:
		return int64(t), true
	case int64:
		return t, true
	case int32:
		return int64(t), true
	case sql.NullInt64:
		if !t.Valid {
			return 0, false
		}
		return t.Int64, true
	case *int:
		if t == nil {
			return 0, false
		}
		return int64(*t), true
	default:
		return 0, false
	}
}

// reconcileBookingOccupancyAfterPatch adjusts the ledger when a booking's
// date, party_size or floor changed (the patch flow does not edit the salon).
func (s *Server) reconcileBookingOccupancyAfterPatch(ctx context.Context, restaurantID, bookingID int, old map[string]any, next boNormalizedBooking) error {
	oldDate, _ := old["reservation_date"].(string)
	oldParty := 0
	if v, ok := old["party_size"].(int64); ok {
		oldParty = int(v)
	}
	var oldFloor, oldSalon any
	if v, ok := old["preferred_floor_number"].(int64); ok {
		oldFloor = v
	}
	if v, ok := old["preferred_salon_id"].(int64); ok {
		oldSalon = v
	}

	newFloor := any(nil)
	if next.PreferredFloorNumber.Valid {
		newFloor = next.PreferredFloorNumber.Int64
	}
	// Salon is not editable through the patch flow; carry the old value.
	return s.applyBookingLocationDelta(ctx, s.db, restaurantID, oldDate, oldFloor, oldSalon, oldParty, next.ReservationDate, newFloor, oldSalon, next.PartySize)
}

// bookingLocationSnapshot reads a booking's headcount/location for occupancy
// reconciliation. floor/salon are returned boxed (int64) or nil.
func (s *Server) bookingLocationSnapshot(ctx context.Context, restaurantID, bookingID int) (date string, party int, floor, salon any, err error) {
	var d sql.NullString
	var p sql.NullInt64
	var f, sl sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT DATE_FORMAT(reservation_date, '%Y-%m-%d'), party_size, preferred_floor_number, preferred_salon_id
		FROM bookings WHERE restaurant_id = ? AND id = ?
	`, restaurantID, bookingID).Scan(&d, &p, &f, &sl)
	if err != nil {
		return "", 0, nil, nil, err
	}
	if d.Valid {
		date = d.String
	}
	if p.Valid {
		party = int(p.Int64)
	}
	if f.Valid {
		floor = f.Int64
	}
	if sl.Valid {
		salon = sl.Int64
	}
	return date, party, floor, salon, nil
}

// loadOccupancyForDate builds scope -> target_id -> count for a given date.
// Used to gate availability in the public day-context endpoint.
func (s *Server) loadOccupancyForDate(ctx context.Context, restaurantID int, date string) (map[string]map[int]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT scope, target_id, count
		FROM reservation_location_occupancy
		WHERE restaurant_id = ? AND date = ?
	`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[int]int{
		"floor": {},
		"salon": {},
	}
	for rows.Next() {
		var scope string
		var targetID, count int
		if err := rows.Scan(&scope, &targetID, &count); err != nil {
			return nil, err
		}
		if _, ok := out[scope]; !ok {
			out[scope] = map[int]int{}
		}
		out[scope][targetID] = count
	}
	return out, rows.Err()
}
