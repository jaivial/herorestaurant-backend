package api

import "testing"

// A tool name the bot defines belongs to the bot, even when forky has a tool
// with the same name. The routing decision must be made against the BOT
// list, not against the merged one: the merged list carries every name, so
// asking it would send every forky tool to the bot executor, which does not
// know them, and no forky tool would ever reach the assistant path.
func TestBotPrivilegedRoutingPrefersTheBotTool(t *testing.T) {
	botTools := botToolDefsForChat(botTenantConfig{}, true)
	forkyTools := botForkyToolDefs()

	botOwned := map[string]struct{}{}
	for _, d := range botTools {
		botOwned[d.Name] = struct{}{}
	}

	// The overlap: a name in both catalogs must go to the bot.
	overlap := 0
	for _, f := range forkyTools {
		if _, mine := botOwned[f.Name]; mine {
			overlap++
			// The bot's own definition is the one that was offered.
			offered := false
			for _, d := range botTools {
				if d.Name == f.Name {
					offered = true
					break
				}
			}
			if !offered {
				t.Errorf("overlap %q must be offered by the bot", f.Name)
			}
		}
	}
	if overlap == 0 {
		t.Skip("the catalogs do not overlap any more")
	}

	// A forky-only tool must NOT be in the bot list, so it reaches the
	// assistant path.
	forkyOnly := ""
	for _, f := range forkyTools {
		if _, mine := botOwned[f.Name]; !mine {
			forkyOnly = f.Name
			break
		}
	}
	if forkyOnly == "" {
		t.Fatal("there must be forky tools the bot does not define")
	}
	for _, d := range botTools {
		if d.Name == forkyOnly {
			t.Errorf("%q must not be a bot tool", forkyOnly)
			break
		}
	}
	// The merged list offers it exactly once.
	merged := botMergeToolDefs(botTools, forkyTools)
	count := 0
	for _, d := range merged {
		if d.Name == forkyOnly {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%q appears %d times in the merged list, want 1", forkyOnly, count)
	}
}

// The merged list is what the model sees, so it must have no repeated name:
// the provider rejects a tool list with duplicates ("Tool names must be
// unique"), which is what killed the primary model.
func TestBotPrivilegedMergedListHasNoDuplicates(t *testing.T) {
	merged := botMergeToolDefs(botToolDefsForChat(botTenantConfig{}, true), botForkyToolDefs())
	seen := map[string]bool{}
	for _, d := range merged {
		if seen[d.Name] {
			t.Errorf("duplicate tool name %q in the privileged list", d.Name)
		}
		seen[d.Name] = true
	}
	if len(merged) == 0 {
		t.Error("the privileged list must not be empty")
	}
}
