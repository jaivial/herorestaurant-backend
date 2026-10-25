package api

import "strings"

// Coordination id: wa_bot_intent_catalog_v1
//
// The staff turn still advertised every group (bookings, config, extras
// catalog, notifications, bot control...) on every model call. Groups that
// are clearly unrelated to the message are dropped BEFORE the loop: fewer
// tokens per call and fewer wrong turns. The meta trio (admin_catalog /
// admin_describe / admin_call) and panel_change always stay, so nothing
// becomes unreachable; the executor still accepts every name.
type botIntentGroup struct {
	keywords []string
	tools    []string
}

var botIntentGroups = []botIntentGroup{
	{ // reservation configuration: open/close days, limits, tables, salons
		keywords: []string{"abrir", "abre", "cerrar", "cierra", "cerrado", "límite", "limite", "aforo", "mesas de", "salón condesa", "salon condesa", "condesa", "configuración", "configuracion"},
		tools:    []string{"config_propose_day", "config_propose_daily_limit", "config_propose_mesas", "config_propose_salon_condesa", "get_booking_config"},
	},
	{ // extras catalogue and booking notification settings
		keywords: []string{"catálogo de extras", "catalogo de extras", "nuevo extra", "crear extra", "borrar extra", "eliminar extra", "notificaci", "aviso de confirmación", "aviso de confirmacion", "whatsapp de confirmación", "whatsapp de confirmacion"},
		tools:    []string{"booking_propose_catalog_extra_create", "booking_propose_catalog_extra_delete", "booking_propose_notif_settings"},
	},
	{ // bot control plane
		keywords: []string{"bot", "acceso", "permiso", "autoriza", "chat permitido", "grupo permitido", "herramienta"},
		tools:    []string{"manage_bot_access", "get_bot_status"},
	},
	{ // staff schedules and attendance
		keywords: []string{"horario", "turno", "fichaje", "fichar", "asistencia", "empleado", "trabajador", "personal", "plantilla", "miembro"},
		tools:    []string{"get_member_schedule", "get_member_attendance", "get_member_access"},
	},
	{ // campaigns
		keywords: []string{"campaña", "campana", "newsletter", "mailing", "envío masivo", "envio masivo"},
		tools:    []string{"list_campaigns"},
	},
}

// botIntentPrunedTools returns the tool names to drop for this message: every
// group none of whose keywords appears. An empty message drops nothing.
func botIntentPrunedTools(text string) map[string]bool {
	t := strings.ToLower(strings.TrimSpace(text))
	drop := map[string]bool{}
	if t == "" {
		return drop
	}
	for _, g := range botIntentGroups {
		hit := false
		for _, k := range g.keywords {
			if strings.Contains(t, k) {
				hit = true
				break
			}
		}
		if !hit {
			for _, n := range g.tools {
				drop[n] = true
			}
		}
	}
	return drop
}
