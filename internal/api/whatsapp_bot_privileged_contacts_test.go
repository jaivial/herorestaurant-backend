package api

import (
	"testing"
)

// The selection is read from the tenant config, and the jids are trimmed.
//
// Coordination id: wa_bot_privileged_boundary_v1: the selection is ADVISORY.
// It is shown in the backoffice, but it does not grant the privileged
// catalogue: the privilege comes from the authorized group of the environment
// and from the current membership of it.
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
}
