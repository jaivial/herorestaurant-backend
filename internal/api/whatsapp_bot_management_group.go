package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_bot_management_group_v1
//
// Every case that used to send the manager's contact card (same day, extras,
// allergens, events, special-date bookings, anger, cannot-handle, repeated
// topic, send_contact tool) now:
//   1. forwards ONE message per customer issue to the restaurant's WhatsApp
//      group ("Bot Alquería"): customer phone, name, request, booking details
//      and request date/time;
//   2. tells the customer, in their language, that the request was sent to
//      the management team, who will contact them soon, and asks if there is
//      anything else the bot can do.
// Duplicate detection: the pipeline asks Jev whether the new message is the
// same issue as one already forwarded (management_requests); a repeat is not
// forwarded again and the customer gets a friendly "the team already has your
// request" reply instead. If the group cannot be reached, the old contact card
// is sent so the customer is never left without a human path.

const botDefaultManagementGroup = "Bot Alquería"

// botManagementDedupWindow bounds how long a forwarded issue counts as open.
const botManagementDedupWindow = 7 * 24 * time.Hour

var botGroupJIDCache = struct {
	sync.Mutex
	byRID map[int]botGroupJIDEntry
}{byRID: map[int]botGroupJIDEntry{}}

type botGroupJIDEntry struct {
	jid string
	at  time.Time
}

// botManagementGroupJID resolves the management group id for a restaurant:
// tenant config JID > cached lookup by name on the Evolution instance.
func (s *Server) botManagementGroupJID(ctx context.Context, restaurantID int, tenant botTenantConfig) string {
	if j := strings.TrimSpace(tenant.ManagementGroupJID); strings.HasSuffix(j, "@g.us") {
		return j
	}
	name := strings.TrimSpace(tenant.ManagementGroupName)
	if name == "" {
		name = botDefaultManagementGroup
	}
	botGroupJIDCache.Lock()
	if e, ok := botGroupJIDCache.byRID[restaurantID]; ok && time.Since(e.at) < 30*time.Minute {
		botGroupJIDCache.Unlock()
		return e.jid
	}
	botGroupJIDCache.Unlock()
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return ""
	}
	evo, ok := gw.(*evolutionGateway)
	if !ok {
		return ""
	}
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Evolution returns a JSON array; uazapiJSONRequest wraps non-object bodies.
	_, code, raw, err := s.uazapiJSONRequest(reqCtx, strings.TrimRight(evo.baseURL, "/")+"/group/fetchAllGroups/"+evo.instanceName+"?getParticipants=false", http.MethodGet, map[string]string{"apikey": evo.apiKey}, nil)
	if err != nil || code < 200 || code >= 300 {
		log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d group_lookup_failed code=%d err=%v", restaurantID, code, err)
		return ""
	}
	jid := botFindGroupJID(raw, name)
	if jid != "" {
		botGroupJIDCache.Lock()
		botGroupJIDCache.byRID[restaurantID] = botGroupJIDEntry{jid: jid, at: time.Now()}
		botGroupJIDCache.Unlock()
	} else {
		log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d group_not_found name=%q", restaurantID, name)
	}
	return jid
}

// botFindGroupJID picks the group whose subject matches name (accent- and
// case-insensitive) from the Evolution fetchAllGroups JSON array.
func botFindGroupJID(raw, name string) string {
	var groups []struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return ""
	}
	want := normalizeBotIntentText(name)
	for _, g := range groups {
		if normalizeBotIntentText(g.Subject) == want && strings.HasSuffix(g.ID, "@g.us") {
			return g.ID
		}
	}
	return ""
}

// botManagementReasonLabel is the human label of the handoff reason shown to
// the management team.
var botManagementReasonLabel = map[string]string{
	"same_day":             "Gestión de una reserva para HOY",
	"extras":               "Cambio de extras de la reserva",
	"allergens":            "Consulta de alérgenos / ingredientes",
	"event_booking":        "Reserva de evento / negociación",
	"special_date_booking": "Cambio en reserva de fecha especial",
	"human_anger":          "Cliente molesto / pide una persona",
	"human_cannot":         "Consulta que el asistente no puede resolver",
	"human_repeat":         "El cliente insiste en un tema derivado",
	"agent_contact":        "Derivado por el asistente",
}

// botManagementGroupText renders the group message.
func (s *Server) botManagementGroupText(ctx context.Context, restaurantID int, msg botWebhookMessage, reason, detail string) string {
	var b strings.Builder
	b.WriteString("🔔 *Nueva solicitud de cliente (WhatsApp bot)*\n")
	label := botManagementReasonLabel[reason]
	if label == "" {
		label = reason
	}
	fmt.Fprintf(&b, "*Motivo:* %s\n", label)
	fmt.Fprintf(&b, "*Cliente:* %s\n", strings.TrimSpace(msg.PushName))
	fmt.Fprintf(&b, "*Teléfono:* +%s (wa.me/%s)\n", digitsOnly(msg.Sender), digitsOnly(msg.Sender))
	fmt.Fprintf(&b, "*Fecha y hora:* %s\n", time.Now().In(boMadridTZ).Format("02/01/2006 15:04"))
	req := strings.TrimSpace(msg.Text)
	if msg.Transcribed {
		req = "🎤 " + req
	}
	fmt.Fprintf(&b, "*Petición:* %s\n", truncate(req, 700))
	if d := strings.TrimSpace(detail); d != "" {
		fmt.Fprintf(&b, "*Detalle:* %s\n", truncate(d, 300))
	}
	bookings, _ := s.botFindBookings(ctx, restaurantID, msg.Sender)
	if len(bookings) == 0 {
		b.WriteString("*Reservas:* sin reservas futuras con este teléfono\n")
	} else {
		b.WriteString("*Reservas:*\n")
		for _, bk := range bookings {
			fmt.Fprintf(&b, "  • #%d %s %s · %d pax · %s", bk.ID, botFormatISODateES(bk.Date), bk.Time, bk.People, strings.TrimSpace(bk.Name))
			if bk.SpecialDateTitle != "" {
				fmt.Fprintf(&b, " · %s", bk.SpecialDateTitle)
			}
			if bk.IsEvent {
				b.WriteString(" · EVENTO")
			}
			if c := strings.TrimSpace(bk.Commentary); c != "" {
				fmt.Fprintf(&b, " · Nota: %s", truncate(strings.ReplaceAll(c, "\n", " "), 160))
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// Contextual first sentence per reason, before the acknowledgement.
var botManagementPrefix = map[string][2]string{
	"same_day":             {"Soy un asistente de reservas con Inteligencia Artificial y no puedo crear, modificar ni cancelar reservas para el día de hoy.", "I'm an AI booking assistant and I can't create, change or cancel bookings for today."},
	"extras":               {"Soy un asistente de reservas con Inteligencia Artificial y no puedo añadir ni modificar los extras de la reserva: esa gestión la hace el restaurante.", "I'm an AI booking assistant and I can't add or change booking extras: the restaurant handles that."},
	"allergens":            {"Soy un asistente de reservas con Inteligencia Artificial y no dispongo de información fiable sobre ingredientes ni alérgenos; por seguridad alimentaria debe confirmarlo el restaurante.", "I'm an AI booking assistant and I don't have reliable ingredient or allergen information; for food safety the restaurant must confirm it."},
	"event_booking":        {"Como se trata de una reserva especial, los detalles se acuerdan directamente con la gestión del restaurante.", "As this is a special booking, the details are agreed directly with the restaurant management."},
	"special_date_booking": {"Tu reserva es para una fecha especial y, como asistente con Inteligencia Artificial, no puedo modificarla ni cancelarla.", "Your booking is for a special date and, as an AI assistant, I can't change or cancel it."},
	"human_anger":          {"Siento mucho que no te haya podido ayudar como esperabas 🙏.", "I'm really sorry I couldn't help you as you expected 🙏."},
	"human_cannot":         {"Esta consulta no la puedo resolver con seguridad y prefiero no darte una respuesta equivocada.", "I can't answer this safely and I'd rather not give you a wrong answer."},
}

func botManagementPrefixFor(reason, lang string) string {
	p, ok := botManagementPrefix[reason]
	if !ok {
		return ""
	}
	if lang == "en" || lang == "other" {
		return p[1]
	}
	return p[0]
}

// Customer-facing replies (wa_bot_management_group_v1).
const (
	botForwardedTextES = "Perfecto, he trasladado tu solicitud al equipo de gestión del restaurante y se pondrán en contacto contigo lo antes posible 🙌. Mientras tanto, ¿hay algo más en lo que pueda ayudarte?"
	botForwardedTextEN = "Done! I've passed your request on to the restaurant management team and they will get in touch with you as soon as possible 🙌. In the meantime, is there anything else I can help you with?"
	botAlreadyTextES   = "Tu solicitud ya está en manos del equipo de gestión del restaurante y se pondrán en contacto contigo muy pronto 😊. Sobre este tema yo ya no puedo hacer nada más, pero si necesitas otra cosa, aquí estoy."
	botAlreadyTextEN   = "Your request is already with the restaurant management team and they will contact you very soon 😊. There's nothing more I can do about this topic myself, but if you need anything else, I'm here."
)

// botForwardToManagement is the single replacement of the contact card.
// prefix is an optional contextual sentence (e.g. the same-day or allergen
// explanation) shown before the acknowledgement. duplicate=true means Jev
// judged the issue as already forwarded: no group message is sent.
// Returns whether the group message (or the fallback card) was delivered.
func (s *Server) botForwardToManagement(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, reason, detail, prefix, lang string, duplicate bool) bool {
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return false
	}
	en := lang == "en" || lang == "other"
	if duplicate {
		text := botAlreadyTextES
		if en {
			text = botAlreadyTextEN
		}
		_ = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, text, "management_already_forwarded")
		log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d sender=%s reason=%s duplicate=true", restaurantID, msg.Sender, reason)
		return true
	}
	groupSent := false
	if jid := s.botManagementGroupJID(ctx, restaurantID, tenant); jid != "" {
		groupText := s.botManagementGroupText(ctx, restaurantID, msg, reason, detail)
		if err := gw.SendText(ctx, jid, groupText); err != nil {
			log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d group_send_failed err=%v", restaurantID, err)
		} else {
			groupSent = true
		}
	}
	summary := strings.TrimSpace(msg.Text)
	if d := strings.TrimSpace(detail); d != "" {
		summary = d + ": " + summary
	}
	_ = s.botConversation.AddManagementRequest(ctx, restaurantID, msg.Sender, reason, summary, groupSent)
	text := botForwardedTextES
	if en {
		text = botForwardedTextEN
	}
	if p := strings.TrimSpace(prefix); p != "" {
		text = p + "\n\n" + text
	}
	if !groupSent {
		// Never leave the customer without a human path.
		_ = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, botContactIntroText(""), "management_handoff_notice")
		name, phone := s.botSameDayContactDetails(ctx, restaurantID, tenant)
		_, _ = s.botSendContactCardWith(ctx, restaurantID, msg, name, phone)
		log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d sender=%s reason=%s group_unavailable card_fallback=true", restaurantID, msg.Sender, reason)
		return false
	}
	_ = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, text, "management_forwarded")
	log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d sender=%s reason=%s forwarded=true", restaurantID, msg.Sender, reason)
	return true
}
