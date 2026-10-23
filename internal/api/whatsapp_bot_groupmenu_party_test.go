package api

import (
	"encoding/json"
	"testing"
)

func TestBotGMenuPartySizeInSchema(t *testing.T) {
	for _, d := range botSpecialAdminToolDefs() {
		if d.Name != "booking_propose_set_group_menu" {
			continue
		}
		var s struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		if err := json.Unmarshal(d.InputSchema, &s); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Properties["party_size"]; !ok {
			t.Error("party_size missing from booking_propose_set_group_menu")
		}
		if len(s.Required) != 1 || s.Required[0] != "booking_id" {
			t.Errorf("required = %v, want only booking_id (menu defaults to the current one)", s.Required)
		}
		return
	}
	t.Fatal("booking_propose_set_group_menu not registered")
}

func TestBotGMenuPartyHelpers(t *testing.T) {
	cur := &botGMenuCurrent{Party: 12}
	if eff := botGMenuWithParty(cur, 11); eff.Party != 11 || cur.Party != 12 {
		t.Errorf("eff=%d cur=%d, want 11 and untouched 12", eff.Party, cur.Party)
	}
	if ch := botGMenuAddParty(nil, 12, 11); len(ch) != 1 || ch[0].Col != "party_size" || ch[0].Val != 11 {
		t.Errorf("changes = %+v", ch)
	}
	if len(botGMenuAddParty(nil, 12, 0)) != 0 || len(botGMenuAddParty(nil, 12, 12)) != 0 {
		t.Error("no party change must add no write")
	}
	if !botGMenuColumns["party_size"] {
		t.Error("party_size must be an allowed column of set_group_menu")
	}
	if botGMenuPartyText(12, 12) != "" || botGMenuPartyText(12, 11) == "" {
		t.Error("party text")
	}
}
