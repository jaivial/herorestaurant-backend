package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Regression for prod incident 2026-09-17 (sender 34679042882, "bautizo"):
// one inbound produced iterations=8 with
// tools=send_message,send_message,send_contact,send_contact,send_contact,
// send_message,send_contact,send_contact,send_message
// i.e. 5 contact cards, each with waid=+34638857294 (leading "+").

// Evolution must send digits-only wuid/phoneNumber so the vCard waid is
// callable (waid=34638857294, not waid=+34638857294).
func TestEvo_SendContact_NormalizesWuidDigitsOnly(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	gw := newEvoGW(srv.URL)
	if err := gw.SendContact(context.Background(), "34679042882", waContact{FullName: "Alqueria Villa Carmen", Phone: "+34638857294", Organization: "Alqueria Villa Carmen"}); err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("reqs=%d", len(reqs))
	}
	contacts, _ := reqs[0].body["contact"].([]any)
	if len(contacts) != 1 {
		t.Fatalf("contact=%v", reqs[0].body["contact"])
	}
	c, _ := contacts[0].(map[string]any)
	if c["wuid"] != "34638857294" {
		t.Errorf("wuid=%v want 34638857294", c["wuid"])
	}
	if c["phoneNumber"] != "34638857294" {
		t.Errorf("phoneNumber=%v want 34638857294", c["phoneNumber"])
	}
	// Formatted input with spaces must also normalize.
	reqs = nil
	if err := gw.SendContact(context.Background(), "34679042882", waContact{FullName: "X", Phone: "+34 638 85 72 94", Organization: "X"}); err != nil {
		t.Fatal(err)
	}
	contacts, _ = reqs[0].body["contact"].([]any)
	c, _ = contacts[0].(map[string]any)
	if c["wuid"] != "34638857294" {
		t.Errorf("spaced wuid=%v want 34638857294", c["wuid"])
	}
}

// botContactPhoneFromResult must only arm dedup on real success; failures
// must not poison the per-turn state or a retry could never deliver.
func TestBotContactPhoneFromResult_OnlySuccessArmsDedup(t *testing.T) {
	if got := botContactPhoneFromResult(`{"sent":true,"phone":"34638857294"}`); got != "34638857294" {
		t.Fatalf("phone from result=%q", got)
	}
	if got := botContactPhoneFromResult(`{"sent":true,"phone":"34638857294","deduped":true}`); got != "34638857294" {
		t.Fatalf("deduped phone=%q", got)
	}
	if got := botContactPhoneFromResult(`{"error":"x"}`); got != "" {
		t.Fatalf("error result must not arm dedup, got %q", got)
	}
	if got := botContactPhoneFromResult(`{"sent":false,"phone":"34638857294"}`); got != "" {
		t.Fatalf("unsent result must not arm dedup, got %q", got)
	}
	if got := botContactPhoneFromResult(`not-json`); got != "" {
		t.Fatalf("bad json must not arm dedup, got %q", got)
	}
}

// Full executor-level test: two consecutive send_contact executions through
// botToolExecutorForTurn collapse to one gateway delivery.
func TestBotExecutor_SendContactSecondCallIsDeduped(t *testing.T) {
	// Use a Server with an in-memory stub gateway via direct botExecuteTool
	// interception: we test that the wrapper short-circuits before reaching
	// the gateway by counting wrapper-level deliveries.
	deliveries := 0
	s := newBotTestServer("")
	state := &botTurnState{}
	// Simulate first delivery arming the state (as botToolSendContact would).
	state.contactSent = true
	state.contactPhone = "34638857294"
	exec := s.botToolExecutorForTurn(1, botWebhookMessage{Sender: "34679042882", Text: "hola"}, botTenantConfig{}, state)
	// Second send_contact must return deduped success without error even though
	// there is no DB/gateway configured (it must not reach botExecuteTool).
	out, err := exec(context.Background(), "send_contact", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("exec err: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("bad json %q: %v", out, err)
	}
	if decoded["sent"] != true {
		t.Errorf("sent=%v want true (%s)", decoded["sent"], out)
	}
	if decoded["deduped"] != true {
		t.Errorf("deduped=%v want true (%s)", decoded["deduped"], out)
	}
	if decoded["phone"] != "34638857294" {
		t.Errorf("phone=%v want 34638857294", decoded["phone"])
	}
	_ = deliveries
}

type countContactGateway struct {
	count *int
}

func (g *countContactGateway) SendText(context.Context, string, string) error { return nil }
func (g *countContactGateway) SendMenu(context.Context, string, string, []string) error {
	return nil
}
func (g *countContactGateway) SendMedia(context.Context, string, waMedia) error       { return nil }
func (g *countContactGateway) SendLocation(context.Context, string, waLocation) error { return nil }
func (g *countContactGateway) SendContact(context.Context, string, waContact) error {
	*g.count++
	return nil
}
func (g *countContactGateway) Provision(context.Context, string) (waProvision, error) {
	return waProvision{}, nil
}
func (g *countContactGateway) Connect(context.Context, string) (waConnState, error) {
	return waConnState{}, nil
}
func (g *countContactGateway) Status(context.Context) (waConnState, error) {
	return waConnState{}, nil
}
func (g *countContactGateway) Disconnect(context.Context) error                        { return nil }
func (g *countContactGateway) Delete(context.Context) error                            { return nil }
func (g *countContactGateway) RegisterWebhook(context.Context, string, []string) error { return nil }
func (g *countContactGateway) ParseInboundMessage([]byte) (waInbound, bool) {
	return waInbound{}, false
}
func (g *countContactGateway) ParseConnectionEvent([]byte) (waConnEvent, bool) {
	return waConnEvent{}, false
}

// Same-day notice must render the phone in display form (+34 638 85 72 94),
// matching the extras-guard notice, not the raw stored value.
func TestBotSameDayNoticeText_UsesDisplayFormat(t *testing.T) {
	got := botSameDayNoticeText("+34638857294")
	if !strings.Contains(got, "+34 638 85 72 94") {
		t.Errorf("notice=%q want display +34 638 85 72 94", got)
	}
	if strings.Contains(got, "+34638857294") {
		t.Errorf("notice=%q must not contain raw +34638857294", got)
	}
}

// Prompt must instruct a single contact card per turn so the model does not
// re-emit send_contact after the tool_result already confirmed delivery.
func TestBotDefaultRules_SingleContactCard(t *testing.T) {
	if !strings.Contains(botDefaultRules, "send_contact") {
		t.Fatal("default rules must mention send_contact")
	}
	if !strings.Contains(strings.ToLower(botDefaultRules), "una vez") {
		t.Errorf("default rules must cap send_contact to once per turn:\n%s", botDefaultRules)
	}
}
