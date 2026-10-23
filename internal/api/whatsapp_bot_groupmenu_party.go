package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Coordination id: wa_bot_gmenu_party_v1
//
// booking_propose_reschedule refuses bookings with a group menu, and the
// group menu tool could not touch the party size, so "change this group-menu
// booking to 11 people, load 11 principales and add the allergies" had no
// path (prod 2026-10-23 18:15). booking_propose_set_group_menu now accepts an
// optional party_size: party, menu, principales and note travel in ONE
// proposal and apply with ONE OK (occupancy and history updated like the panel).

// botGMenuWithParty returns cur with the proposed party size (a copy).
func botGMenuWithParty(cur *botGMenuCurrent, party int) *botGMenuCurrent {
	if party <= 0 || party == cur.Party {
		return cur
	}
	c := *cur
	c.Party = party
	return &c
}

// botGMenuAddParty appends the party_size write when it changes.
func botGMenuAddParty(changes []botEditChange, orig, party int) []botEditChange {
	if party > 0 && party != orig {
		changes = append(changes, botEditChange{Col: "party_size", Val: party})
	}
	return changes
}

// botGMenuPartyText is the preview/notice sentence for a party change.
func botGMenuPartyText(orig, party int) string {
	if party <= 0 || party == orig {
		return ""
	}
	return fmt.Sprintf(" Comensales: %d → %d (se actualizan la ocupación y el historial).", orig, party)
}

// botGMenuApplyPartyDelta reconciles occupancy and records the history of a
// party change inside the proposal transaction.
func (s *Server) botGMenuApplyPartyDelta(ctx context.Context, tx *sql.Tx, rid int, bookingID int64, date string, orig, party int, name, phone string) error {
	var floor, salon sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT preferred_floor_number, preferred_salon_id FROM bookings WHERE restaurant_id = ? AND id = ?`, rid, bookingID).Scan(&floor, &salon); err != nil {
		return errors.New("error interno")
	}
	var f, sl any
	if floor.Valid {
		f = floor.Int64
	}
	if salon.Valid {
		sl = salon.Int64
	}
	if err := s.applyBookingLocationDelta(ctx, tx, rid, date, f, sl, orig, date, f, sl, party); err != nil {
		return errors.New("no se pudo actualizar la ocupación; no se aplicó nada")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO booking_modifications (restaurant_id, booking_id, original_reservation_date, field_modified, old_value, new_value, modified_by, modified_by_name, customer_name, contact_phone, modification_date) VALUES (?, ?, ?, 'party_size', ?, ?, 'whatsapp', 'Bot WhatsApp (dirección)', ?, ?, NOW())`,
		rid, bookingID, date, fmt.Sprint(orig), fmt.Sprint(party), name, phone); err != nil {
		return errors.New("no se pudo registrar el historial; no se aplicó nada")
	}
	return nil
}
