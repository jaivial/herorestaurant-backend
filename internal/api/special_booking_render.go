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

// specialSummaryDish is one chosen principal with how many guests picked it.
type specialSummaryDish struct {
	Name  string
	Count int
}

// specialSummaryMenu is one booked menu line with its grouped principales and
// adelanto (per unit x count).
type specialSummaryMenu struct {
	Label       string
	Count       int
	Principales []specialSummaryDish
	Adelanto    float64
}

// specialBookingSummary is the channel-neutral view shared by the email and
// the WhatsApp confirmation. Coordination id: festive_prereserva_notifications_v1
type specialBookingSummary struct {
	Title          string
	IsPrereserva   bool
	Menus          []specialSummaryMenu
	HasPrincipales bool
	AdelantoTotal  float64
	AdelantoPaid   float64
	AdelantoStatus string
	QRURL          string
}

// buildSpecialBookingSummary resolves the summary from the booking `special`
// block. ok=false when the booking is not a special booking.
func buildSpecialBookingSummary(booking map[string]any) (specialBookingSummary, bool) {
	block, ok := specialBookingRenderModels(booking)
	if !ok || !isSpecialBookingFlag(booking) {
		return specialBookingSummary{}, false
	}
	out := specialBookingSummary{
		Title:          strings.TrimSpace(anyToString(block["title"])),
		IsPrereserva:   isPrereservaFlag(booking),
		AdelantoStatus: strings.TrimSpace(anyToString(block["adelanto_status"])),
	}
	if qr, ok := booking[bookingQRKey].(*bookingQR); ok && qr != nil {
		out.QRURL = qr.URL
	}
	if out.QRURL == "" {
		out.QRURL = strings.TrimSpace(anyToString(booking["qr_url"]))
	}
	out.AdelantoPaid, _ = numericField(block, "adelanto_paid_total")
	menus, _ := block["menus"].([]map[string]any)
	if menus == nil {
		if raw, ok := block["menus"].([]any); ok {
			for _, m := range raw {
				if mm, ok := m.(map[string]any); ok {
					menus = append(menus, mm)
				}
			}
		}
	}
	for _, menu := range menus {
		label := strings.TrimSpace(anyToString(menu["label"]))
		count, _ := anyToInt(menu["count"])
		if label == "" || count <= 0 {
			continue
		}
		perUnit, _ := numericField(menu, "adelanto_per_unit")
		line := specialSummaryMenu{Label: label, Count: count, Adelanto: round2(perUnit * float64(count))}
		idx := map[string]int{}
		for _, item := range specialMenuItems(menu["items"]) {
			name := strings.TrimSpace(anyToString(item["name"]))
			if name == "" {
				continue
			}
			if i, seen := idx[name]; seen {
				line.Principales[i].Count++
				continue
			}
			idx[name] = len(line.Principales)
			line.Principales = append(line.Principales, specialSummaryDish{Name: name, Count: 1})
		}
		if len(line.Principales) > 0 {
			out.HasPrincipales = true
		}
		out.AdelantoTotal += line.Adelanto
		out.Menus = append(out.Menus, line)
	}
	out.AdelantoTotal = round2(out.AdelantoTotal)
	return out, true
}

// specialMenuItems accepts both []map[string]any (in-process response) and
// []any (JSON round-trip) item lists.
func specialMenuItems(v any) []map[string]any {
	if items, ok := v.([]map[string]any); ok {
		return items
	}
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// hideArrozForSpecialBooking: special-date menus with principales replace the
// rice choice, so the Arroz row is omitted in email and WhatsApp.
func hideArrozForSpecialBooking(booking map[string]any) bool {
	sum, ok := buildSpecialBookingSummary(booking)
	return ok && sum.HasPrincipales
}

// bookingConfirmationTitle is "Confirmación de prereserva" for prereservas.
func bookingConfirmationTitle(booking map[string]any) string {
	if isSpecialBookingFlag(booking) && isPrereservaFlag(booking) {
		return "Confirmación de prereserva"
	}
	return "Confirmación de reserva"
}

// formatSpecialBookingWhatsApp renders the special-booking section used by
// buildBookingWhatsAppMessage: paid adelanto (total + per menu), booked menus
// and the principales chosen per menu.
func formatSpecialBookingWhatsApp(booking map[string]any) string {
	sum, ok := buildSpecialBookingSummary(booking)
	if !ok {
		return ""
	}
	var b strings.Builder
	if sum.AdelantoPaid > 0 {
		fmt.Fprintf(&b, "\n💶 *Adelanto pagado:* %.2f€\n", sum.AdelantoPaid)
		for _, m := range sum.Menus {
			if m.Adelanto > 0 {
				fmt.Fprintf(&b, "  • %s x%d: %.2f€\n", m.Label, m.Count, m.Adelanto)
			}
		}
	}
	if len(sum.Menus) > 0 {
		heading := "Reserva"
		if sum.IsPrereserva {
			heading = "Prereserva"
		}
		fmt.Fprintf(&b, "\n🎉 *%s %s*\n", heading, sum.Title)
		for _, m := range sum.Menus {
			fmt.Fprintf(&b, "  • %s x%d\n", m.Label, m.Count)
			for _, d := range m.Principales {
				fmt.Fprintf(&b, "    – %s x%d\n", d.Name, d.Count)
			}
		}
	}
	return b.String()
}

// renderSpecialBookingEmailBlock returns the centered container rendered below
// the details table: "Prereserva|Reserva {title}", booked menus with counts and
// principales, then the adelanto summary (total + per menu) and the QR.
func renderSpecialBookingEmailBlock(booking map[string]any) string {
	sum, ok := buildSpecialBookingSummary(booking)
	if !ok {
		return ""
	}
	heading := "Reserva"
	if sum.IsPrereserva {
		heading = "Prereserva"
	}
	var b strings.Builder
	b.WriteString(`<div data-testid="email-special-summary" style="background-color:#f8f9fa;border:1px solid #e9ecef;border-radius:8px;padding:20px;margin-bottom:30px;">` + "\n")
	fmt.Fprintf(&b, `<h2 style="margin:0 0 14px 0;text-align:center;color:#097969;font-size:20px;">%s</h2>`+"\n", htmlEscape(strings.TrimSpace(heading+" "+sum.Title)))
	b.WriteString(`<table role="presentation" style="width:100%;border-collapse:collapse;">` + "\n")
	for _, m := range sum.Menus {
		fmt.Fprintf(&b, `<tr><td style="padding:8px 0;font-weight:bold;">%s</td><td style="padding:8px 0;text-align:right;font-weight:bold;">x%d</td></tr>`+"\n", htmlEscape(m.Label), m.Count)
		for _, d := range m.Principales {
			fmt.Fprintf(&b, `<tr><td style="padding:2px 0 2px 18px;color:#555;">%s</td><td style="padding:2px 0;text-align:right;color:#555;">x%d</td></tr>`+"\n", htmlEscape(d.Name), d.Count)
		}
	}
	b.WriteString("</table>\n")
	if sum.AdelantoTotal > 0 {
		label := "Adelanto pagado"
		if sum.AdelantoStatus != "paid" {
			label = "Adelanto"
		}
		fmt.Fprintf(&b, `<h3 style="margin:18px 0 8px 0;text-align:center;color:#097969;font-size:16px;">%s</h3>`+"\n", label)
		b.WriteString(`<table role="presentation" style="width:100%;border-collapse:collapse;">` + "\n")
		for _, m := range sum.Menus {
			if m.Adelanto > 0 {
				fmt.Fprintf(&b, `<tr><td style="padding:4px 0;">%s x%d</td><td style="padding:4px 0;text-align:right;">%.2f€</td></tr>`+"\n", htmlEscape(m.Label), m.Count, m.Adelanto)
			}
		}
		fmt.Fprintf(&b, `<tr><td style="padding:8px 0;border-top:1px solid #ddd;font-weight:bold;">Total</td><td style="padding:8px 0;border-top:1px solid #ddd;text-align:right;font-weight:bold;">%.2f€</td></tr>`+"\n", sum.AdelantoTotal)
		b.WriteString("</table>\n")
	}
	if sum.QRURL != "" {
		fmt.Fprintf(&b, `<div style="text-align:center;margin-top:18px;"><img src="%s" alt="QR de la reserva" width="180" height="180" style="width:180px;height:180px;"><p style="margin:6px 0 0 0;font-size:12px;color:#666;">Presente este QR al llegar al restaurante.</p></div>`+"\n", htmlEscape(sum.QRURL))
	}
	b.WriteString("</div>\n")
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