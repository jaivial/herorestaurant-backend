package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// evo-weai (github.com/jaivial/evo-weai) is the Rust server that replaces the
// Evolution API fork. It speaks the same REST API and webhook payloads, so the
// Evolution gateway drives it unchanged. Coordination id: wa_evo_weai_v1
//
// testdata/evo_weai_webhooks.json holds webhook bodies produced by evo-weai
// 0.2.4's own serializer (the fork's field names), not hand-written JSON: these
// tests pin the bot's parsers to what that server actually sends.
func evoWeaiWebhook(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/evo_weai_webhooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	body, ok := all[name]
	if !ok {
		t.Fatalf("no fixture %q", name)
	}
	return body
}

func TestEvoWeai_InboundLIDChatRepliesToThePhone(t *testing.T) {
	// A 1:1 chat WhatsApp addresses by LID: the customer's phone is
	// remoteJidAlt, the identity the bot books under and replies to.
	in, ok := (&evolutionGateway{}).ParseInboundMessage(evoWeaiWebhook(t, "upsert_lid_1to1"))
	if !ok {
		t.Fatal("a LID chat with remoteJidAlt must be served")
	}
	if in.Sender != "34692747052" || in.ChatJID != "34692747052@s.whatsapp.net" {
		t.Fatalf("sender=%q chat=%q", in.Sender, in.ChatJID)
	}
	if in.Text != "hola" || in.PushName != "Ana" || in.MessageID != "3EB0ABC" || in.SessionRef != "nv-1-x" || in.FromMe {
		t.Fatalf("in=%+v", in)
	}
}

func TestEvoWeai_InboundPhoneChat(t *testing.T) {
	in, ok := (&evolutionGateway{}).ParseInboundMessage(evoWeaiWebhook(t, "upsert_pn_1to1"))
	if !ok || in.Sender != "34600111222" || in.Text != "hola" {
		t.Fatalf("ok=%v in=%+v", ok, in)
	}
}

func TestEvoWeai_GroupMentionResolvesTheMembersPhone(t *testing.T) {
	// The member writes as a LID; participantAlt carries the phone the booking
	// tools key on. The bot is mentioned by its own number.
	gw := &evolutionGateway{ownJIDs: []string{"34960255536"}}
	in, ok := gw.ParseInboundMessage(evoWeaiWebhook(t, "upsert_group_lid"))
	if !ok {
		t.Fatal("a mention of the bot in a group must be served")
	}
	if in.Sender != "120363000000000001@g.us" || in.ParticipantJID != "34692747052@s.whatsapp.net" {
		t.Fatalf("sender=%q participant=%q", in.Sender, in.ParticipantJID)
	}
	if !strings.Contains(in.Text, "hola") || strings.Contains(in.Text, "34960255536") {
		t.Fatalf("the mention chip is stripped, the request kept: %q", in.Text)
	}
	// Not mentioned: skipped.
	if _, ok := (&evolutionGateway{ownJIDs: []string{"34111111111"}}).ParseInboundMessage(evoWeaiWebhook(t, "upsert_group_lid")); ok {
		t.Fatal("a group message that does not mention the bot is not a turn")
	}
}

func TestEvoWeai_ConnectionEvents(t *testing.T) {
	gw := &evolutionGateway{}
	if _, ok := gw.ParseInboundMessage(evoWeaiWebhook(t, "qr")); ok {
		t.Fatal("a QR is not a message")
	}

	qr, ok := gw.ParseConnectionEvent(evoWeaiWebhook(t, "qr"))
	if !ok || qr.SessionRef != "nv-1-x" || qr.Status != "pending" {
		t.Fatalf("qr: ok=%v ev=%+v", ok, qr)
	}
	// The panel renders the image as is: evo-weai sends an SVG data URL.
	if !strings.HasPrefix(qr.QR, "data:image/svg+xml;base64,") || normalizeQRImage(qr.QR) != qr.QR {
		t.Fatalf("qr image=%.40q", qr.QR)
	}
	if qr.PairCode != "" {
		t.Fatalf("the raw QR payload is not a pairing code: %q", qr.PairCode)
	}

	pair, ok := gw.ParseConnectionEvent(evoWeaiWebhook(t, "pairing"))
	if !ok || pair.PairCode != formatEvolutionPairingCode("ABCD1234") || pair.QR != "" {
		t.Fatalf("pairing: ok=%v ev=%+v", ok, pair)
	}

	open, ok := gw.ParseConnectionEvent(evoWeaiWebhook(t, "open"))
	if !ok || !isUAZAPIConnected(open.Status) {
		t.Fatalf("open: ok=%v ev=%+v", ok, open)
	}

	paired, ok := gw.ParseConnectionEvent(evoWeaiWebhook(t, "paired"))
	if !ok || !isUAZAPIConnected(paired.Status) || digitsOnly(paired.ConnectedPhone) != "34960255536" {
		t.Fatalf("paired: ok=%v ev=%+v", ok, paired)
	}
}

func TestEvoWeai_LIDChatWithoutAlternateIsDropped(t *testing.T) {
	// evo-weai before 0.2.4 sent no remoteJidAlt: the opaque LID is not a
	// phone, so the bot must not answer it as one (it would book and reply
	// under the wrong identity). This is why the server must be >= 0.2.4.
	var env map[string]any
	if err := json.Unmarshal(evoWeaiWebhook(t, "upsert_lid_1to1"), &env); err != nil {
		t.Fatal(err)
	}
	delete(env["data"].(map[string]any)["key"].(map[string]any), "remoteJidAlt")
	body, _ := json.Marshal(env)
	if in, ok := (&evolutionGateway{}).ParseInboundMessage(body); ok {
		t.Fatalf("an unresolved LID was served: %+v", in)
	}
}
