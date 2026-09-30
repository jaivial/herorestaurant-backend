package api

import "testing"

// Coordination id: special_date_section_online_v1
func TestOnlineSpecialDateMenuSections(t *testing.T) {
	in := []specialDateMenuSection{
		{ID: 1, Title: "Adultos", OnlineEnabled: true},
		{ID: 2, Title: "Infantil", OnlineEnabled: false},
		{ID: 3, Title: "Vegano", OnlineEnabled: true},
	}
	out := onlineSpecialDateMenuSections(in)
	if len(out) != 2 || out[0].ID != 1 || out[1].ID != 3 {
		t.Fatalf("expected sections 1 and 3, got %+v", out)
	}
	if got := onlineSpecialDateMenuSections(nil); got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil slice, got %#v", got)
	}
}
