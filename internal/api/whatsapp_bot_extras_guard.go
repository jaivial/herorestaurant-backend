package api

import (
	"context"
	"log"
	"regexp"
	"strings"
)

// Coordination id: booking-extras-handoff.
//
// The AI booking assistant may READ a booking's extras (they are part of
// get_bookings / get_booking_menu) but must never add, remove or change them:
// extras are managed by the restaurant staff. When a customer asks for an
// extras change the guard answers server-side (AI notice + management contact
// card) before the model runs, so the request can never be fulfilled by the
// LLM and the customer always gets a human handoff.

const botExtrasContactName = "Gestión del restaurante"

// botExtrasMutationRe matches the verbs that express an intent to change
// something. It is combined with an extras mention so a plain question
// ("¿qué extras tenéis?") still gets answered by the agent.
var botExtrasMutationRe = regexp.MustCompile(`\b(?:a[nñ]ad\w*|agreg\w*|sumar|sumo|suma|sumas|poner|pongo|pones|pone|ponme|ponerle|pongas|incluir|incluye|incluyo|incluyes|quit\w*|elimin\w*|borr\w*|sacar|saco|saca|sacas|retir\w*|cambi\w*|modific\w*|actualiz\w*|gestion\w*|quier\w*|gustar\w*|pued\w*|podr\w*|traer|traes|trae)\b`)

var botExtrasWordRe = regexp.MustCompile(`\bextras?\b`)

var botExtrasAccentReplacer = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n",
)

// botExtrasGenericTerms are the add-ons that read as "extras" even when the
// customer does not use the word "extra".
var botExtrasGenericTerms = []string{
	"cafe incluido", "bebida ilimitada", "bebida incluida", "botella de cava",
	"botella de whisky", "copa de vino", "refresco incluido", "cerveza incluida",
	"champan", "champagne", "botella de vino", "tarta",
}

func normalizeBotIntentText(raw string) string {
	return botExtrasAccentReplacer.Replace(strings.ToLower(strings.TrimSpace(raw)))
}

// botExtrasNoticeText is the mandatory reply when a customer asks to change the
// extras of a booking. It states the caller is an AI booking manager and that
// the change must be handled by the restaurant, then points at the contact.
func botExtrasNoticeText(phone string) string {
	msg := "Hola 👋🏼\n" +
		"Soy un gestor de reservas por Inteligencia Artificial.\n" +
		"No puedo añadir ni modificar los extras de su reserva: esa gestión la realiza el restaurante."
	if p := strings.TrimSpace(phone); p != "" {
		msg += "\n📞 " + botFormatPhoneDisplay(p)
	}
	msg += "\nLe dejo la tarjeta de contacto de " + botExtrasContactName + " 👇"
	return msg
}

// botFormatPhoneDisplay renders a stored E.164-ish phone as +34 638 85 72 94.
func botFormatPhoneDisplay(raw string) string {
	digits := digitsOnly(raw)
	if len(digits) == 11 && strings.HasPrefix(digits, "34") {
		return "+34 " + digits[2:5] + " " + digits[5:7] + " " + digits[7:9] + " " + digits[9:11]
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "+") {
		return strings.TrimSpace(raw)
	}
	if digits != "" {
		return "+" + digits
	}
	return strings.TrimSpace(raw)
}

// botTextMentionsExtras reports whether the normalized text refers to a booking
// extra (the word itself, a known add-on, or one of the restaurant's catalog
// entries).
func botTextMentionsExtras(normalized string, extraNames []string) bool {
	if botExtrasWordRe.MatchString(normalized) {
		return true
	}
	for _, term := range botExtrasGenericTerms {
		if strings.Contains(normalized, term) {
			return true
		}
	}
	for _, name := range extraNames {
		candidate := normalizeBotIntentText(name)
		// Short names would match too much; 4+ chars keeps it to real words.
		if len([]rune(candidate)) >= 4 && strings.Contains(normalized, candidate) {
			return true
		}
	}
	return false
}

// botExtrasMutationIntent reports whether the inbound text asks to change the
// booking extras (add / remove / modify), as opposed to only asking what exists.
func botExtrasMutationIntent(text string, extraNames []string) bool {
	normalized := normalizeBotIntentText(text)
	if normalized == "" {
		return false
	}
	if !botTextMentionsExtras(normalized, extraNames) {
		return false
	}
	return botExtrasMutationRe.MatchString(normalized)
}

// botExtrasIntentGuard enforces the "extras are managed by the restaurant"
// policy from the raw inbound text, before the model can promise something it
// cannot do. When the customer asks for an extras change, the AI notice and the
// management contact card are delivered immediately and the agent turn is
// skipped. Returns true when the message was handled here.
func (s *Server) botExtrasIntentGuard(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig) bool {
	extraNames := make([]string, 0, 8)
	if extras, err := s.loadRestaurantBookingExtras(restaurantID); err == nil {
		for _, extra := range extras {
			extraNames = append(extraNames, extra.Name)
		}
	}
	if !botExtrasMutationIntent(msg.Text, extraNames) {
		return false
	}
	log.Printf("[bot] checkpoint booking_extras_change_blocked restaurant_id=%d sender=%s", restaurantID, msg.Sender)
	s.botBlockExtrasChange(ctx, restaurantID, msg, tenant)
	return true
}

// botBlockExtrasChange delivers the AI notice + management contact card for an
// extras change request. The requested change is never performed.
func (s *Server) botBlockExtrasChange(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig) {
	_, phone := s.botContactDetails(ctx, restaurantID, tenant)
	if phone == "" {
		_, phone = s.botSameDayContactDetails(ctx, restaurantID, tenant)
	}

	noticeSent := false
	if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
		if err := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, msg.Sender, botExtrasNoticeText(phone), "extras_notice"); err == nil {
			noticeSent = true
		}
	}
	cardSent := false
	if phone != "" {
		if _, err := s.botSendContactCardWith(ctx, restaurantID, msg, botExtrasContactName, phone); err != nil {
			log.Printf("[bot] restaurant=%d extras contact card failed: %v", restaurantID, err)
		} else {
			cardSent = true
		}
	}
	log.Printf("[bot] checkpoint booking_extras_change_notified restaurant_id=%d sender=%s notice_sent=%t card_sent=%t", restaurantID, msg.Sender, noticeSent, cardSent)
}
