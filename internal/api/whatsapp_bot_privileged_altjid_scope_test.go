package api

import (
	"testing"

	"preactvillacarmen/internal/config"
)

// La identidad alterna de una conversación 1:1 (WhatsApp usa el @lid opaco y
// el teléfono para la misma conversación) no puede convertir un 1:1 en un
// grupo.
//
// La vía de grupo privileged es la que comprueba la pertenencia al grupo y
// la que no exige mención. Si un 1:1 pudiera servirse como grupo, recibiría
// el catálogo root sin comprobación de pertenencia: la escalada sería
// completa.
//
// Coordination id: wa_bot_privileged_contacts_v1
func TestAltJIDCannotSmuggleAGroupIntoA1to1(t *testing.T) {
	const group = "120363430262328476@g.us"
	const phone = "34600000000@s.whatsapp.net"

	tenant := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{{JID: group, IsGroup: true}},
	}
	s := &Server{cfg: config.Config{}}

	// A 1:1 whose alternate identity is a privileged group.
	if botConversationIsPrivileged(s, tenant, phone, group) {
		t.Error("a 1:1 with a group as its alternate identity must not be privileged")
	}
	// And it must not be treated as a group either, so the membership gate
	// is not the thing that would have caught it.
	if botPrivilegedConversationIsGroup(tenant, phone) {
		t.Error("a 1:1 must never be treated as a group")
	}

	// The other way round is refused as well: a group whose alternate
	// identity is a contact.
	tenantContact := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{{JID: phone, IsGroup: false}},
	}
	if botConversationIsPrivileged(s, tenantContact, group, phone) {
		t.Error("a group with a contact as its alternate identity must not match a contact selection")
	}
}

// A selected one-to-one contact is still privileged in both of its forms:
// the fix above must not break the case the alternate identity exists for.
func TestAltJIDStillMatchesTheSameOneToOneSelection(t *testing.T) {
	const lid = "242554100576489@lid"
	const phone = "34692747052@s.whatsapp.net"

	tenant := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{{JID: lid, Name: "Jaime"}},
	}
	s := &Server{cfg: config.Config{}}

	if !botConversationIsPrivileged(s, tenant, phone, lid) {
		t.Error("a 1:1 selected by its @lid must match a message addressed by the phone")
	}
	if !botConversationIsPrivileged(s, tenant, lid, phone) {
		t.Error("a 1:1 selected by its phone must match a message addressed by the @lid")
	}
}
