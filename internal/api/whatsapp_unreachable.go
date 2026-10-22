package api

import (
	"context"
	"log"
	"strings"
)

// Coordination id: wa_unreachable_suppression_v1
//
// A WhatsApp number that the provider reports as having no devices is a
// number that cannot receive: it is not on WhatsApp, it is blocked, or it
// never existed. Sending a campaign to it fails every time, so the number
// goes to the restaurant's do-not-contact list (`campaign_suppressions`,
// channel `whatsapp`) and the audience builder skips it from then on.
//
// The evidence is the provider's own words, not the status code: a 503 also
// means "the instance is not connected" or "the socket is down", and those
// are transient. Only the message that names the recipient's devices is
// treated as definitive, so an outage never blacklists a customer.

// whatsappRecipientUnreachable reports whether the provider said the
// recipient has no WhatsApp devices.
//
// The phrases are the ones the providers in use say:
//   - evo-weai: "346538444289 has no WhatsApp devices"
//   - evo-weai (profile lookup): "346538444289 is not on WhatsApp"
//   - Evolution: "Number is not on WhatsApp"
func whatsappRecipientUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, phrase := range []string{
		"has no whatsapp devices",
		"no whatsapp devices",
		"is not on whatsapp",
		"not on whatsapp",
		"not a valid whatsapp",
	} {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// suppressUnreachableWhatsappTarget adds a WhatsApp target to the
// restaurant's do-not-contact list so no future campaign writes to it.
//
// The target is normalised with the same rule the audience builder and the
// suppression filter use (`campaignCanonicalTarget`), so the row written here
// is the row the filter looks up. `booking_id` is the booking the contact
// came from when there is one; the list keeps the contact either way.
func (s *Server) suppressUnreachableWhatsappTarget(ctx context.Context, restaurantID int, target string, bookingID int64, reason string) {
	// A group JID ("1203...@g.us") is not a campaign target and its digits
	// would land in the list as a number that matches nothing.
	if strings.Contains(target, "@") {
		return
	}
	canonical := campaignCanonicalTarget("whatsapp", target)
	if len(canonical) < 8 || len(canonical) > 15 {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "no esta en WhatsApp"
	}
	var booking any
	if bookingID > 0 {
		booking = bookingID
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
		VALUES (?, 'whatsapp', ?, ?, ?, 'invalid')
	`, restaurantID, canonical, booking, reason); err != nil {
		log.Printf("[whatsapp][obs][CP-UNREACHABLE-SUPPRESS-FAILED] restaurant=%d target=%s err=%v", restaurantID, canonical, err)
		return
	}
	log.Printf("[whatsapp][obs][CP-UNREACHABLE-SUPPRESSED] restaurant=%d target=%s reason=%s", restaurantID, canonical, reason)
}
