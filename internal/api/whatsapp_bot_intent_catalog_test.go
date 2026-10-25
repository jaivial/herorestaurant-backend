package api

import "testing"

func TestBotIntentPrunedTools(t *testing.T) {
	booking := "@Alqueria Pues tienes que modificar esa reserva para que sean 11 personas y cargar los 11 principales y anadir a comentarios las alergias"
	drop := botIntentPrunedTools(booking)
	for _, n := range []string{"config_propose_day", "manage_bot_access", "booking_propose_notif_settings", "get_member_schedule", "list_campaigns"} {
		if !drop[n] {
			t.Errorf("%s should be pruned for a booking change request", n)
		}
	}
	for _, n := range []string{"booking_propose_set_group_menu", "staff_bookings_overview", "panel_change", "admin_call"} {
		if drop[n] {
			t.Errorf("%s must never be pruned", n)
		}
	}
	if d := botIntentPrunedTools("cierra el día 24 de diciembre"); d["config_propose_day"] {
		t.Error("config tools must stay when the message asks to close a day")
	}
	if len(botIntentPrunedTools("")) != 0 {
		t.Error("empty message must prune nothing")
	}
}
