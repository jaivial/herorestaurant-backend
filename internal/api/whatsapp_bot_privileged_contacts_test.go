package api

import (
	"testing"

	"preactvillacarmen/internal/config"
)

// A conversation the operator selected in the backoffice is privileged, and
// everything else keeps the regular limits.
func TestBotGroupIsPrivilegedAcceptsSelectedContacts(t *testing.T) {
	const group = "120363430262328476@g.us"
	const contact = "34692747052@s.whatsapp.net"

	tenant := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{
			{JID: group, Name: "Bot Alquería", IsGroup: true},
			{JID: contact, Name: "Chef", IsGroup: false},
		},
	}
	s := &Server{cfg: config.Config{}}

	cases := []struct {
		name string
		jid  string
		want bool
	}{
		{"the selected group is privileged", group, true},
		{"the selected contact is privileged", contact, true},
		{"another group is not", "999999999999@g.us", false},
		{"another contact is not", "34600000000@s.whatsapp.net", false},
		{"empty is not", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.botGroupIsPrivileged(tc.jid, tenant); got != tc.want {
				t.Errorf("botGroupIsPrivileged(%q) = %v, want %v", tc.jid, got, tc.want)
			}
		})
	}
}

// The env list keeps working for an installation that has not been migrated,
// and the selected contacts only ADD conversations.
func TestBotGroupIsPrivilegedCombinesSources(t *testing.T) {
	const envGroup = "111111111111@g.us"
	const selected = "222222222222@g.us"

	s := &Server{cfg: config.Config{BotPrivilegedGroupJIDs: []string{envGroup}}}
	tenant := botTenantConfig{PrivilegedContacts: []botPrivilegedContact{{JID: selected, IsGroup: true}}}

	if !s.botGroupIsPrivileged(envGroup, tenant) {
		t.Error("the env list must still apply")
	}
	if !s.botGroupIsPrivileged(selected, tenant) {
		t.Error("the selected contact must apply")
	}
	if s.botGroupIsPrivileged("333333333333@g.us", tenant) {
		t.Error("an unlisted conversation must not be privileged")
	}
}

// A privileged GROUP is privileged for its members, so the membership check
// applies. A privileged CONTACT is the conversation itself, so there is nobody
// else to check and the membership check does NOT apply.
func TestBotPrivilegedConversationIsGroup(t *testing.T) {
	tenant := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{
			{JID: "120363430262328476@g.us", IsGroup: true},
			{JID: "34692747052@s.whatsapp.net", IsGroup: false},
		},
	}
	if !botPrivilegedConversationIsGroup(tenant, "120363430262328476@g.us") {
		t.Error("a selected group must be treated as a group")
	}
	if botPrivilegedConversationIsGroup(tenant, "34692747052@s.whatsapp.net") {
		t.Error("a selected contact must not be treated as a group")
	}
}

// The selection is read from the tenant config, and the jids are trimmed.
func TestBotPrivilegedContactsFromConfig(t *testing.T) {
	tenant := botTenantConfig{
		PrivilegedContacts: []botPrivilegedContact{
			{JID: "  34692747052@s.whatsapp.net  ", Name: " Chef "},
			{JID: "   ", Name: "vacio"},
			{JID: "120363430262328476@g.us", IsGroup: true},
		},
	}
	got := botPrivilegedContactsFromConfig(tenant)
	if len(got) != 2 {
		t.Fatalf("expected 2 contacts, got %d: %v", len(got), got)
	}
	if got[0].JID != "34692747052@s.whatsapp.net" {
		t.Errorf("the jid must be trimmed: %q", got[0].JID)
	}
	if got[0].Name != "Chef" {
		t.Errorf("the name must be trimmed: %q", got[0].Name)
	}
	if !got[1].IsGroup {
		t.Error("the group flag must survive")
	}
	jids := botPrivilegedContactJIDs(tenant)
	if _, ok := jids["120363430262328476@g.us"]; !ok {
		t.Error("the group must be in the jid set")
	}
}
