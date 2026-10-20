package api

import (
	"context"
	"database/sql"
)

// Coordination id: guest_count_flags_v1
//
// Per-special-date toggles that decide whether the public booking wizard
// asks for the number of adults, high chairs (tronas) and baby strollers
// (carros de bebé). Dates without an active special_dates row keep the
// legacy behaviour: every counter is asked.

// guestCountFlags is the resolved set of "ask" toggles for one date.
type guestCountFlags struct {
	AskAdults        bool
	AskHighChairs    bool
	AskBabyStrollers bool
}

// guestCountFlagsAllOn is the default when the date has no active
// special_dates row (ordinary days always ask, matching legacy behaviour).
func guestCountFlagsAllOn() guestCountFlags {
	return guestCountFlags{AskAdults: true, AskHighChairs: true, AskBabyStrollers: true}
}

// resolveGuestCountFlags returns the effective guest-count toggles for a
// date: the active special_dates row wins; no row (or inactive) means all on.
func (s *Server) resolveGuestCountFlags(ctx context.Context, restaurantID int, date string) (guestCountFlags, error) {
	var askAdults, askHighChairs, askBabyStrollers int
	err := s.db.QueryRowContext(ctx, `
		SELECT ask_adults, ask_high_chairs, ask_baby_strollers
		FROM special_dates
		WHERE restaurant_id = ? AND date = ? AND is_active = 1
		LIMIT 1
	`, restaurantID, date).Scan(&askAdults, &askHighChairs, &askBabyStrollers)
	if err == sql.ErrNoRows {
		return guestCountFlagsAllOn(), nil
	}
	if err != nil {
		return guestCountFlags{}, err
	}
	return guestCountFlags{
		AskAdults:        askAdults != 0,
		AskHighChairs:    askHighChairs != 0,
		AskBabyStrollers: askBabyStrollers != 0,
	}, nil
}

// nilUnlessAsked returns nil (SQL NULL) when the counter was not asked, so
// the booking row records "not asked" instead of a fake 0.
func nilUnlessAsked(asked bool, v int) any {
	if !asked {
		return nil
	}
	return v
}
