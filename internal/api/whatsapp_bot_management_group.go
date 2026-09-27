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

// botManagementReasonLabel is the human title of the request shown to the
// management team (icon + text, no internal codes).
var botManagementReasonLabel = map[string]string{
	"same_day":             "📅 Gestión de una reserva para HOY",
	"extras":               "🍾 Cambio de extras de la reserva",
	"allergens":            "⚠️ Consulta de alérgenos o ingredientes",
	"event_booking":        "🎉 Reserva de evento",
	"special_date_booking": "🎄 Cambio en una reserva de fecha especial",
	"human_anger":          "😠 Cliente molesto o que pide hablar con alguien",
	"human_cannot":         "❓ Consulta que el asistente no puede resolver",
	"human_repeat":         "🔁 El cliente insiste en un tema ya derivado",
	"agent_contact":        "🙋 El cliente necesita que le atienda una persona",
}

// botOperationLabel turns the internal operation / intent code into plain
// Spanish for the group message; unknown codes are dropped (never shown raw).
func botOperationLabel(op string) string {
	switch strings.TrimSpace(op) {
	case "create_booking":
		return "Quiere hacer una reserva nueva"
	case "modify_booking":
		return "Quiere modificar su reserva"
	case "cancel_booking":
		return "Quiere cancelar su reserva"
	case "special_needs_request":
		return "Pide una necesidad especial para la reserva"
	case "extras":
		return "Pide cambiar los extras de la reserva"
	case "rice":
		return "Consulta sobre el arroz de la reserva"
	case "booking_status":
		return "Pregunta por su reserva"
	case "invoice_payment":
		return "Factura, pago o devolución"
	case "lost_item":
		return "Ha perdido u olvidado un objeto"
	case "gift_voucher":
		return "Pregunta por tarjetas o vales regalo"
	case "job_application":
		return "Busca trabajo"
	case "supplier":
		return "Proveedor o comercial"
	case "event_inquiry", "group_booking":
		return "Quiere organizar un evento o grupo"
	case "complaint":
		return "Queja"
	case "human":
		return "Quiere hablar con una persona"
	}
	return ""
}

// botGroupDivider separates the blocks of the group message.
const botGroupDivider = "━━━━━━━━━━━━━━━"

// botManagementGroupText renders a readable, WhatsApp-formatted group
// message: title, customer, request, bookings and time, in separate blocks.
func (s *Server) botManagementGroupText(ctx context.Context, restaurantID int, msg botWebhookMessage, reason, detail string) string {
	var b strings.Builder
	label := botManagementReasonLabel[reason]
	if label == "" {
		label = "🙋 Solicitud de cliente"
	}
	b.WriteString("🔔 *NUEVA SOLICITUD DE CLIENTE*\n")
	b.WriteString(label + "\n")
	b.WriteString(botGroupDivider + "\n\n")

	b.WriteString("👤 *Cliente*\n")
	name := strings.TrimSpace(msg.PushName)
	if name == "" || name == "Cliente" {
		name = "Sin nombre en WhatsApp"
	}
	fmt.Fprintf(&b, "%s\n", name)
	fmt.Fprintf(&b, "📞 %s\n\n", botFormatPhoneDisplay(msg.Sender))

	b.WriteString("💬 *Lo que pide*\n")
	req := strings.TrimSpace(msg.Text)
	if msg.Transcribed {
		b.WriteString("_(nota de voz transcrita)_\n")
	}
	fmt.Fprintf(&b, "“%s”\n", truncate(req, 700))
	if d := strings.TrimSpace(detail); d != "" {
		fmt.Fprintf(&b, "\n📝 %s\n", truncate(d, 300))
	}
	b.WriteString("\n")

	bookings, _ := s.botFindBookings(ctx, restaurantID, msg.Sender)
	if len(bookings) == 0 {
		b.WriteString("📋 *Reservas*\nNo tiene reservas próximas con este teléfono.\n\n")
	} else {
		if len(bookings) == 1 {
			b.WriteString("📋 *Su reserva*\n")
		} else {
			fmt.Fprintf(&b, "📋 *Sus reservas (%d)*\n", len(bookings))
		}
		for i, bk := range bookings {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "▫️ *%s* a las *%s*\n", botCapitalize(botFormatISODateES(bk.Date)), bk.Time)
			people := fmt.Sprintf("%d personas", bk.People)
			if bk.People == 1 {
				people = "1 persona"
			}
			fmt.Fprintf(&b, "      👥 %s · a nombre de %s\n", people, strings.TrimSpace(bk.Name))
			if bk.SpecialDateTitle != "" {
				fmt.Fprintf(&b, "      🎄 %s\n", bk.SpecialDateTitle)
			}
			if bk.IsEvent {
				b.WriteString("      🎉 Reserva de evento\n")
			}
			if c := strings.TrimSpace(bk.Commentary); c != "" {
				fmt.Fprintf(&b, "      🗒️ %s\n", truncate(strings.Join(strings.Fields(c), " "), 160))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(botGroupDivider + "\n")
	fmt.Fprintf(&b, "🕐 %s\n", botCapitalize(time.Now().In(boMadridTZ).Format("02/01/2006 · 15:04")))
	b.WriteString("_Os dejo su tarjeta de contacto a continuación 👇_")
	return b.String()
}

func botCapitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
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
			// The customer's own contact card, so the team can call or save
			// the number with one tap.
			cardName := strings.TrimSpace(msg.PushName)
			if cardName == "" || cardName == "Cliente" {
				cardName = "Cliente " + botFormatPhoneDisplay(msg.Sender)
			}
			if err := gw.SendContact(ctx, jid, waContact{FullName: cardName, Phone: digitsOnly(msg.Sender), Organization: "Cliente · WhatsApp bot"}); err != nil {
				log.Printf("[bot] checkpoint wa_bot_management_group_v1 restaurant_id=%d customer_card_failed err=%v", restaurantID, err)
			}
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
