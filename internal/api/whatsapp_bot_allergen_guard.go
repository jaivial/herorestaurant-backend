package api

import (
	"context"
	"log"
	"regexp"
	"strings"
)

// Coordination id: wa_bot_allergen_handoff_v1.
//
// The bot has no ingredient, stock or allergen data (get_rice_menu only returns
// dish names). Guessing here is a food-safety risk (prod incident 2026-09-24,
// sender 34630762921: the model claimed a rice was made with beef stock). Any
// allergen/intolerance/ingredient question is answered server-side with an AI
// notice + the restaurant contact card, before the model runs.

var botAllergenIntentRe = regexp.MustCompile(`\b(?:alerg\w*|intoleran\w*|celiac\w*|gluten|lactosa|ingredientes?|caldos?|proteinas? de (?:la )?(?:vaca|leche|huevo)|trazas|frutos secos|allerg\w*|ingredients?|broth)\b`)

// botBookingNoteRe matches a request to write the allergy into the booking
// comments: that follow-up belongs to the agent (it has the tools), otherwise
// the guard's own offer would loop back into the guard.
var botBookingNoteRe = regexp.MustCompile(`\b(?:apunt\w*|anot\w*|coment\w*|nota)\b`)

// botAllergenIntent reports whether the inbound text asks about allergens,
// intolerances, ingredients or how a dish is made.
func botAllergenIntent(text string) bool {
	normalized := normalizeBotIntentText(text)
	return normalized != "" && botAllergenIntentRe.MatchString(normalized) && !botBookingNoteRe.MatchString(normalized)
}

// botAllergenNoticeText is the fixed, safe reply to an allergen question.
func botAllergenNoticeText(phone string) string {
	msg := "Hola 👋🏼\n" +
		"Soy un asistente de reservas con Inteligencia Artificial y no dispongo de información sobre ingredientes, elaboración ni alérgenos de los platos.\n" +
		"Por seguridad alimentaria, esta consulta debe confirmarla directamente el restaurante"
	if p := strings.TrimSpace(phone); p != "" {
		msg += ": 📞 " + botFormatPhoneDisplay(p)
	}
	msg += ".\nLe dejo la tarjeta de contacto 👇\n" +
		"Si lo desea, puedo anotar la alergia o intolerancia en los comentarios de su reserva."
	return msg
}

// botAllergenIntentGuard answers allergen/ingredient questions with the AI
// notice + contact card and skips the agent turn. Returns true when handled.
func (s *Server) botAllergenIntentGuard(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig) bool {
	if !botAllergenIntent(msg.Text) {
		return false
	}
	name, phone := s.botContactDetails(ctx, restaurantID, tenant)
	if phone == "" {
		name, phone = s.botSameDayContactDetails(ctx, restaurantID, tenant)
	}
	noticeSent, cardSent := false, false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		noticeSent = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, botAllergenNoticeText(phone), "allergen_notice") == nil
	}
	if phone != "" {
		if _, err := s.botSendContactCardWith(ctx, restaurantID, msg, name, phone); err != nil {
			log.Printf("[bot] restaurant=%d allergen contact card failed: %v", restaurantID, err)
		} else {
			cardSent = true
		}
	}
	log.Printf("[bot] checkpoint wa_bot_allergen_handoff_v1 restaurant_id=%d sender=%s notice_sent=%t card_sent=%t", restaurantID, msg.Sender, noticeSent, cardSent)
	return true
}
