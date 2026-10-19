package api

import "testing"

// The privileged turn offers the bot's tools plus the whole forky catalog.
// The two overlap (create_booking exists in both) and the provider rejects a
// tool list with duplicate names, so the merge must drop the repeats.
func TestBotMergeToolDefsDropsDuplicates(t *testing.T) {
	bot := []botToolDef{
		{Name: "send_message"},
		{Name: "create_booking", Description: "la del bot"},
		{Name: "get_bookings"},
	}
	forky := []botToolDef{
		{Name: "create_booking", Description: "la de forky"},
		{Name: "bookings_summary"},
		{Name: "admin_call"},
	}
	merged := botMergeToolDefs(bot, forky)

	names := map[string]botToolDef{}
	for _, d := range merged {
		if _, dup := names[d.Name]; dup {
			t.Fatalf("duplicate tool name %q in the merged list", d.Name)
		}
		names[d.Name] = d
	}
	if len(merged) != 5 {
		t.Errorf("merged list has %d tools, want 5: %v", len(merged), names)
	}
	// The bot definition wins: its schema is the one the bot's executor knows.
	if got := names["create_booking"].Description; got != "la del bot" {
		t.Errorf("create_booking description = %q, want the bot's one", got)
	}
	// The forky-only tools are still there.
	for _, n := range []string{"bookings_summary", "admin_call"} {
		if _, ok := names[n]; !ok {
			t.Errorf("%q must survive the merge", n)
		}
	}
}

func TestBotMergeToolDefsKeepsTheRealCatalogsApart(t *testing.T) {
	// The two live catalogs really do overlap: that is the bug this merge
	// fixes, so a future rename must not silently reintroduce it unnoticed.
	bot := botToolDefsForChat(botTenantConfig{}, false)
	forky := botForkyToolDefs()
	overlap := map[string]bool{}
	for _, d := range bot {
		for _, f := range forky {
			if d.Name == f.Name {
				overlap[d.Name] = true
			}
		}
	}
	if len(overlap) == 0 {
		t.Skip("the catalogs do not overlap any more: the merge is still correct but this check has nothing to assert")
	}
	merged := botMergeToolDefs(bot, forky)
	seen := map[string]bool{}
	for _, d := range merged {
		if seen[d.Name] {
			t.Fatalf("duplicate tool name %q after merging the real catalogs", d.Name)
		}
		seen[d.Name] = true
	}
}
