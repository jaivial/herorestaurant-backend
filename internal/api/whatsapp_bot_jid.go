package api

import (
	"strconv"
	"strings"
)

// Coordination id: wa_bot_group_mention_v1
//
// WhatsApp addresses the same person in several forms and Baileys appends a
// device suffix to the ones it owns:
//
//	phone  "34960255536"     "34960255536@s.whatsapp.net"   "34960255536:28@s.whatsapp.net"
//	LID    "262096671481918" "262096671481918@lid"          "262096671481918:0@lid"
//
// Comparing two forms of the SAME identity as raw strings is what silenced
// the bot inside its own group: the provider listed the bot with a device
// suffix and the mention arrived without it, so nothing matched and the bot
// stayed quiet.
//
// These helpers are the one place where an identity becomes comparable, so
// every caller (own JIDs, mentions, participants) speaks the same language.

// botJIDStripDeviceSuffix removes the Baileys device suffix of a JID
// ("34960255536:28@s.whatsapp.net" -> "34960255536@s.whatsapp.net").
//
// Only a trailing ":<digits>" is removed: the colon of a name or of a group id
// is part of the id, not a device suffix.
func botJIDStripDeviceSuffix(v string) string {
	v = strings.TrimSpace(v)
	at := strings.Index(v, "@")
	head, tail := v, ""
	if at > 0 {
		head, tail = v[:at], v[at:]
	}
	colon := strings.LastIndex(head, ":")
	if colon <= 0 {
		return v
	}
	if _, err := strconv.Atoi(head[colon+1:]); err != nil {
		return v
	}
	return head[:colon] + tail
}

// botWhatsappIDDigits returns the identity digits of a WhatsApp id in any
// form: a plain number ("34960255536"), a phone JID
// ("34960255536:28@s.whatsapp.net") or a LID ("262096671481918:0@lid").
//
// It is used to compare the bot's own identities with the ones a mention
// carries. The strict per-kind helpers (botGroupPhoneDigits,
// botGroupLidDigits) are the ones to use when a form must not be mixed with
// another one (membership, customer identity).
func botWhatsappIDDigits(v string) string {
	return digitsOnly(botJIDStripDeviceSuffix(v))
}
