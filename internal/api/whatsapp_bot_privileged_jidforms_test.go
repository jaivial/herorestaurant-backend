package api

import (
	"testing"

	"preactvillacarmen/internal/config"
)

// WhatsApp addresses the same 1:1 conversation by two forms: the opaque @lid
// and the phone-number JID. The provider lists the chat by one of them and
// delivers the inbound message addressed to the OTHER one.
//
// A selection made in the backoffice therefore has to match the form that
// arrives, or the privilege would silently never apply.
//
// Coordination id: wa_bot_privileged_contacts_v1
func TestBotPrivilegedContactMatchesBothJIDForms(t *testing.T) {
	const lid = "242554100576489@lid"
	const phone = "34692747052@s.whatsapp.net"

	s := &Server{cfg: config.Config{}}

	// The operator picked the chat as the provider listed it (the @lid) and
	// the message arrives addressed by the phone number.
	tenantLID := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{{JID: lid, Name: "Jaime Villanueva"}},
	}
	if !botConversationIsPrivileged(s, tenantLID, phone, lid) {
		t.Error("a selection made on the @lid must match a message addressed by the phone")
	}
	// And the other way round: the selection is the phone and the message
	// arrives by the @lid.
	tenantPhone := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{{JID: phone, Name: "Jaime Villanueva"}},
	}
	if !botConversationIsPrivileged(s, tenantPhone, lid, phone) {
		t.Error("a selection made on the phone must match a message addressed by the @lid")
	}
	// A contact that was NOT selected stays unprivileged in both forms.
	tenantOther := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{{JID: "34600000000@s.whatsapp.net"}},
	}
	if botConversationIsPrivileged(s, tenantOther, phone, lid) {
		t.Error("an unselected contact must not be privileged in any form")
	}
}

// A contact selected in the backoffice is NOT a group, so the membership gate
// does not apply to it even when the jid ends in @g.us by accident.
func TestBotPrivilegedConversationIsGroupForSelectedContact(t *testing.T) {
	tenant := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{
			{JID: "34692747052@s.whatsapp.net", IsGroup: false},
			{JID: "120363430262328476@g.us", IsGroup: true},
		},
	}
	if botPrivilegedConversationIsGroup(tenant, "34692747052@s.whatsapp.net") {
		t.Error("a selected contact must not be treated as a group")
	}
	if !botPrivilegedConversationIsGroup(tenant, "120363430262328476@g.us") {
		t.Error("a selected group must be treated as a group")
	}
}
