package api

import (
	"fmt"
	"sort"
	"strings"
)

// Special booking render helpers shared by WhatsApp, email and reminder.
//
// Coordination id: special_booking_v1
//
// The booking payloads arrive either as a precomputed `special` map (response
// shape from buildSpecialBookingResponse) or via the raw `special_json` string.
// These helpers accept both and resolve dish names from the cached `items`
// entries (already filled by the response helper) — we never re-query the DB
// from these template helpers because notifications fire from contexts that
// may not have an active SQL connection.

// isSpecialBookingFlag returns true when the booking row carries the
// is_special_booking flag (either as bool or int from the DB scan).
func isSpecialBookingFlag(booking map[string]any) bool {
	return toBool(booking["is_special_booking"])
}

// isPrereservaFlag returns true when the booking is a pre-reserva (open before
// the regular window opens).
func isPrereservaFlag(booking map[string]any) bool {
	return toBool(booking["is_prereserva"])
}

// specialBookingRenderModels resolves the rich `special` map from either the
// precomputed block or the raw `special_json` JSON. The block is the
// canonical source because it always carries the dish names; the JSON is a
// last-resort fallback for callers that only persisted the raw payload.
func specialBookingRenderModels(booking map[string]any) (map[string]any, bool) {
	if raw, ok := booking["special"].(map[string]any); ok && len(raw) > 0 {
		return raw, true
	}
	return nil, false
}

// formatSpecialBookingWhatsApp renders the special-booking lines used by
// buildBookingWhatsAppMessage (per-menu, principales names, adelanto totals).
func formatSpecialBookingWhatsApp(booking map[string]any) string {
	block, ok := specialBookingRenderModels(booking)
	if !ok {
		return ""
	}
	var b strings.Builder
	title := strings.TrimSpace(anyToString(block["title"]))
	if title != "" {
		fmt.Fprintf(&b, "🎉 *Menú especial:* %s\n", title)
	}
	menus, _ := block["menus"].([]any)
	for _, raw := range menus {
		menu, _ := raw.(map[string]any)
		if menu == nil {
			continue
		}
		label := strings.TrimSpace(anyToString(menu["label"]))
		count, _ := anyToInt(menu["count"])
		if label != "" && count > 0 {
			fmt.Fprintf(&b, "  • %s x%d\n", label, count)
		}
		if items, _ := menu["items"].([]any); len(items) > 0 {
			for _, itemRaw := range items {
				item, _ := itemRaw.(map[string]any)
				if item == nil {
					continue
				}
				name := strings.TrimSpace(anyToString(item["name"]))
				if name != "" {
					fmt.Fprintf(&b, "    – %s\n", name)
				}
			}
		}
	}
	if req, ok := numericField(block, "adelanto_required_total"); ok && req > 0 {
		pending := 0.0
		if p, ok := numericField(block, "adelanto_pending_total"); ok {
			pending = p
		}
		fmt.Fprintf(&b, "💶 *Adelanto:* %.2f€\n", req)
		if pending > 0 {
			fmt.Fprintf(&b, "   Pendiente: %.2f€\n", pending)
		} else {
			fmt.Fprintf(&b, "   Pagado\n")
		}
	}
	if isPrereservaFlag(booking) {
		b.WriteString("ℹ️ *Pre-reserva:* se confirmará al abrirse el calendario regular\n")
	}
	return b.String()
}

// renderSpecialBookingEmailRows returns the HTML <tr> rows used by
// buildBookingEmailHTML when is_special_booking is true.
func renderSpecialBookingEmailRows(booking map[string]any) string {
	block, ok := specialBookingRenderModels(booking)
	if !ok {
		return ""
	}
	var b strings.Builder
	title := strings.TrimSpace(anyToString(block["title"]))
	if title != "" {
		fmt.Fprintf(&b, `<tr><td colspan="2" style="padding-top:14px;font-size:16px;font-weight:600;color:#7c3aed;">🎉 %s</td></tr>`, htmlEscape(title))
	}
	menus, _ := block["menus"].([]any)
	for _, raw := range menus {
		menu, _ := raw.(map[string]any)
		if menu == nil {
			continue
		}
		label := strings.TrimSpace(anyToString(menu["label"]))
		count, _ := anyToInt(menu["count"])
		if label != "" && count > 0 {
			fmt.Fprintf(&b, `<tr><td style="padding:4px 0;color:#555;">%s</td><td style="padding:4px 0;text-align:right;font-weight:600;">x%d</td></tr>`, htmlEscape(label), count)
		}
		if items, _ := menu["items"].([]any); len(items) > 0 {
			for _, itemRaw := range items {
				item, _ := itemRaw.(map[string]any)
				if item == nil {
					continue
				}
				name := strings.TrimSpace(anyToString(item["name"]))
				if name != "" {
					fmt.Fprintf(&b, `<tr><td style="padding:2px 0 2px 18px;color:#888;font-size:13px;">– %s</td><td></td></tr>`, htmlEscape(name))
				}
			}
		}
	}
	if req, ok := numericField(block, "adelanto_required_total"); ok && req > 0 {
		pending := 0.0
		if p, ok := numericField(block, "adelanto_pending_total"); ok {
			pending = p
		}
		fmt.Fprintf(&b, `<tr><td style="padding-top:10px;color:#555;">Adelanto</td><td style="padding-top:10px;text-align:right;font-weight:600;">%.2f€</td></tr>`, req)
		if pending > 0 {
			fmt.Fprintf(&b, `<tr><td style="color:#c2410c;">Pendiente</td><td style="text-align:right;color:#c2410c;font-weight:600;">%.2f€</td></tr>`, pending)
		}
	}
	if isPrereservaFlag(booking) {
		b.WriteString(`<tr><td colspan="2" style="padding-top:8px;color:#7c3aed;font-size:13px;">Pre-reserva: se confirmará al abrirse el calendario regular.</td></tr>`)
	}
	return b.String()
}

// formatSpecialBookingReminderLines renders the single-line summary used by
// the n8n reminder: menu lines + pending adelanto line when > 0.
func formatSpecialBookingReminderLines(booking map[string]any) string {
	block, ok := specialBookingRenderModels(booking)
	if !ok {
		return ""
	}
	var b strings.Builder
	title := strings.TrimSpace(anyToString(block["title"]))
	if title != "" {
		fmt.Fprintf(&b, "🎉 %s\n", title)
	}
	menus, _ := block["menus"].([]any)
	for _, raw := range menus {
		menu, _ := raw.(map[string]any)
		if menu == nil {
			continue
		}
		label := strings.TrimSpace(anyToString(menu["label"]))
		count, _ := anyToInt(menu["count"])
		if label != "" && count > 0 {
			fmt.Fprintf(&b, "  • %s x%d\n", label, count)
		}
	}
	if req, ok := numericField(block, "adelanto_required_total"); ok && req > 0 {
		if pending, ok := numericField(block, "adelanto_pending_total"); ok && pending > 0 {
			fmt.Fprintf(&b, "💶 Pendiente %.2f€\n", pending)
		}
	}
	if isPrereservaFlag(booking) {
		b.WriteString("ℹ️ Pre-reserva\n")
	}
	return b.String()
}

// numericField pulls a float64 out of a JSON map field. The response helpers
// always emit numbers, but the JSON marshaler may surface them as float64.
func numericField(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// sortedSpecialBookingMethodKeys returns the method keys ordered alphabetically
// so the email / reminder render is deterministic.
func sortedSpecialBookingMethodKeys(m map[string]any) []string {
	keys := make([]string, 0)
	if v, ok := m["adelanto_by_method"].([]any); ok {
		for _, raw := range v {
			if row, _ := raw.(map[string]any); row != nil {
				if k := strings.TrimSpace(anyToString(row["method"])); k != "" {
					keys = append(keys, k)
				}
			}
		}
	}
	sort.Strings(keys)
	return keys
}