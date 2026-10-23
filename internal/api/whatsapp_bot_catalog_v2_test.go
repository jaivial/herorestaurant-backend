package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBotStaffCatalogV2_DropsPhoneScopedTools(t *testing.T) {
	bot := botToolDefsForChat(botTenantConfig{}, true)
	bot = append(bot, botSpecialAdminToolDefs()...)
	bot = append(bot, botToolDefsAdminReads()...)
	before := botMergeToolDefs(bot, botToolDefsPrivilegedControl(), botForkyToolDefs())
	after := botMergeToolDefs(botWithoutTools(bot, botStaffPhoneScopedTools), botToolDefsPrivilegedControl(), botForkyToolDefs())
	size := func(ds []botToolDef) int {
		n := 0
		for _, d := range ds {
			raw, _ := json.Marshal(d)
			n += len(raw)
		}
		return n
	}
	t.Logf("tools %d -> %d, bytes %d -> %d", len(before), len(after), size(before), size(after))
	names := map[string]bool{}
	for _, d := range after {
		names[d.Name] = true
	}
	for n := range botStaffPhoneScopedTools {
		if names[n] {
			t.Errorf("%s still advertised to the staff turn", n)
		}
	}
	for _, n := range []string{"staff_bookings_overview", "panel_change", "booking_propose_edit", "booking_propose_set_group_menu", "admin_call"} {
		if !names[n] {
			t.Errorf("%s missing from the staff catalog", n)
		}
	}
}

func TestBotStaffPrompt_NoPhoneScopingRules(t *testing.T) {
	sys := renderBotSystemPrompt(botPromptData{InGroup: true, PrivilegedGroup: true, SpecialAdminTools: true})
	if strings.Contains(sys, "Cada mensaje del grupo menciona un tel") {
		t.Error("staff prompt still tells the model to work by customer phone")
	}
	if strings.Contains(sys, "`get_booking_menu`") {
		t.Error("staff prompt references a tool the staff catalog no longer advertises")
	}
	cust := renderBotSystemPrompt(botPromptData{InGroup: true})
	if !strings.Contains(cust, "Cada mensaje del grupo menciona un tel") {
		t.Error("non-privileged group prompt must keep the phone rule")
	}
}
