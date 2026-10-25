package api

// Coordination id: wa_bot_catalog_v2
//
// The privileged staff turn advertised 76 tools (~53 KB, ~18k tokens) on every
// model call. Ten of them are CUSTOMER tools scoped to the sender's phone: in
// the group the sender is the group JID, so they always answer "reserva no
// encontrada para este teléfono" (prod 2026-10-23 18:15: get_booking_details
// x2, special_booking_get misses) or are refused as direct writes. They only
// cost tokens and iterations. When the staff admin tools are registered, the
// staff equivalents (staff_bookings_overview, *_propose_*) replace them.
var botStaffPhoneScopedTools = map[string]bool{
	"get_bookings":           true,
	"get_booking_details":    true,
	"get_booking_menu":       true,
	"add_booking_note":       true,
	"create_booking":         true,
	"modify_booking":         true,
	"cancel_booking":         true,
	"create_special_booking": true,
	"modify_special_booking": true,
	"cancel_special_booking": true,
}

// botWithoutTools returns defs minus the names in drop.
func botWithoutTools(defs []botToolDef, drop map[string]bool) []botToolDef {
	out := make([]botToolDef, 0, len(defs))
	for _, d := range defs {
		if !drop[d.Name] {
			out = append(out, d)
		}
	}
	return out
}
