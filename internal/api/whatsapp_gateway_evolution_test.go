package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type evoReq struct {
	method, path, query, apikey, origin string
	body                                map[string]any
}

func fakeEvolution(t *testing.T, reqs *[]evoReq, responder func(path string) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		b := map[string]any{}
		_ = json.Unmarshal(raw, &b)
		*reqs = append(*reqs, evoReq{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, apikey: r.Header.Get("apikey"), origin: r.Header.Get("Origin"), body: b})
		code, resp := http.StatusOK, `{}`
		if responder != nil {
			code, resp = responder(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(resp))
	}))
}

func newEvoGW(url string) *evolutionGateway {
	return &evolutionGateway{s: &Server{}, baseURL: url, apiKey: "owa_k1", instanceName: "nv_1"}
}

func TestEvo_ConnectPairing_UsesNumberQueryAndParsesCode(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) {
		return 200, `{"state":"connecting","qrcode":{"base64":"data:image/png;base64,QR","code":"123-456-789"}}`
	})
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Connect(context.Background(), "+34 600/111222")
	if err != nil {
		t.Fatal(err)
	}
	if reqs[0].path != "/instance/connect/nv_1" || reqs[0].query != "number=%2B34+600%2F111222" {
		t.Fatalf("request=%+v", reqs[0])
	}
	if reqs[0].origin != strings.TrimRight(srv.URL, "/") {
		t.Fatalf("origin=%q, want %q", reqs[0].origin, srv.URL)
	}
	if st.QR == "" || st.PairCode != "123-456-789" || st.Status != "connecting" {
		t.Errorf("state=%+v", st)
	}
}

func TestEvo_ConnectPairing_TopLevelPairingCode(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) { return 200, `{"pairingCode":"ABC123"}` })
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Connect(context.Background(), "34600111222")
	if err != nil || st.PairCode != "ABC123" {
		t.Fatalf("state=%+v err=%v", st, err)
	}
}

func TestEvo_Connect_ErrorOnNon2xx(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) { return 400, `{"message":"bad number"}` })
	defer srv.Close()
	if _, err := newEvoGW(srv.URL).Connect(context.Background(), "34600111222"); err == nil {
		t.Fatal("expected connect error")
	}
}

func TestEvo_SendText_BuildRequest(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	if err := newEvoGW(srv.URL).SendText(context.Background(), "34600111222", "hola"); err != nil {
		t.Fatal(err)
	}
	r := reqs[0]
	if r.path != "/message/sendText/nv_1" || r.apikey != "owa_k1" {
		t.Errorf("path=%q apikey=%q", r.path, r.apikey)
	}
	if r.body["number"] != "34600111222" || r.body["text"] != "hola" {
		t.Errorf("body=%v", r.body)
	}
}

func TestEvo_MenuUsesReplyButtons(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	if err := newEvoGW(srv.URL).SendMenu(context.Background(), "34600111222", "Elige", []string{"Reservar", "Carta"}); err != nil {
		t.Fatal(err)
	}
	r := reqs[0]
	if !strings.HasPrefix(r.path, "/message/sendButtons/") {
		t.Fatalf("path=%q, want sendButtons", r.path)
	}
	buttons, _ := r.body["buttons"].([]any)
	if len(buttons) != 2 {
		t.Fatalf("buttons=%v", buttons)
	}
	for i, raw := range buttons {
		b, _ := raw.(map[string]any)
		if b["type"] != "reply" {
			t.Errorf("button %d type=%v, want reply", i, b["type"])
		}
	}
}

// Evolution renders the interactive body as "*<title>*\n\n<description>".
// The booking confirmation previously set title AND description to the full
// text, so the customer received the message content twice in one bubble.
func TestEvo_MenuTitleAndDescriptionDoNotDuplicate(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	text := "*Confirmación de Reserva - Alqueria Villa Carmen*\n\n" +
		"Hola jaime,\n\nGracias por elegirnos. Su reserva ha sido confirmada."
	if err := newEvoGW(srv.URL).SendMenu(context.Background(), "34600111222", text, []string{"CONDICIONES|https://example.com/policies"}); err != nil {
		t.Fatal(err)
	}
	title, _ := reqs[0].body["title"].(string)
	desc, _ := reqs[0].body["description"].(string)
	if title != "Confirmación de Reserva - Alqueria Villa Carmen" {
		t.Errorf("title=%q, want header without markdown", title)
	}
	if desc != "Hola jaime,\n\nGracias por elegirnos. Su reserva ha sido confirmada." {
		t.Errorf("description=%q, want the body without the header line", desc)
	}
	if title == desc {
		t.Error("title and description are identical, the message will be shown twice")
	}
}

func TestEvo_MenuSingleLinePutsTextInTitle(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	if err := newEvoGW(srv.URL).SendMenu(context.Background(), "34600111222", "Elige una opción", []string{"Si", "No"}); err != nil {
		t.Fatal(err)
	}
	title, _ := reqs[0].body["title"].(string)
	desc, _ := reqs[0].body["description"].(string)
	if title != "Elige una opción" || desc != "" {
		t.Errorf("title=%q description=%q", title, desc)
	}
}

// Booking confirmations pass choices as "label|url"; dropping the URL would
// leave the customer without the policies/cancel links.
func TestEvo_MenuMapsPipeChoicesToURLButtons(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	choices := []string{
		"CONDICIONES|https://example.com/booking-policies",
		"Cancelar Reserva|https://example.com/cancel?id=123",
		"Responder",
	}
	if err := newEvoGW(srv.URL).SendMenu(context.Background(), "34600111222", "Reserva", choices); err != nil {
		t.Fatal(err)
	}
	buttons, _ := reqs[0].body["buttons"].([]any)
	if len(buttons) != 3 {
		t.Fatalf("buttons=%v", buttons)
	}
	want := []struct{ typ, text, url string }{
		{"url", "CONDICIONES", "https://example.com/booking-policies"},
		{"url", "Cancelar Reserva", "https://example.com/cancel?id=123"},
		{"reply", "Responder", ""},
	}
	for i, w := range want {
		b, _ := buttons[i].(map[string]any)
		if b["type"] != w.typ || b["displayText"] != w.text {
			t.Errorf("button %d = %+v, want type=%q displayText=%q", i, b, w.typ, w.text)
		}
		if w.url != "" && b["url"] != w.url {
			t.Errorf("button %d url=%v, want %q", i, b["url"], w.url)
		}
	}
}

// A pipe with a non-HTTP payload is not a link; it must not become a url
// button pointing nowhere.
func TestEvo_MenuKeepsNonURLPipeChoicesAsReply(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	if err := newEvoGW(srv.URL).SendMenu(context.Background(), "34600111222", "Elige", []string{"Mesa|interior"}); err != nil {
		t.Fatal(err)
	}
	buttons, _ := reqs[0].body["buttons"].([]any)
	if len(buttons) != 1 {
		t.Fatalf("buttons=%v", buttons)
	}
	b, _ := buttons[0].(map[string]any)
	if b["type"] != "reply" {
		t.Errorf("button=%+v, want type=reply", b)
	}
}

func TestEvo_MenuCloudApiUsesButtons(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	gw := newEvoGW(srv.URL)
	gw.cloudAPI = true
	if err := gw.SendMenu(context.Background(), "34600111222", "Elige", []string{"Si", "No"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reqs[0].path, "/message/sendButtons/") {
		t.Errorf("path=%q, want sendButtons", reqs[0].path)
	}
}

func TestEvo_Provision_Sequence(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) {
		if strings.HasPrefix(path, "/instance/create") {
			return 201, `{"hash":"inst-token-abc","instance":{"instanceName":"nv_1","status":"created"}}`
		}
		return 200, `{}`
	})
	defer srv.Close()
	gw := newEvoGW(srv.URL)
	p, err := gw.Provision(context.Background(), "nv_1")
	if err != nil {
		t.Fatal(err)
	}
	if p.SessionRef != "inst-token-abc" || p.ProviderInstanceID != "nv_1" {
		t.Errorf("provision=%+v", p)
	}
	if reqs[0].path != "/instance/create" || reqs[0].body["integration"] != "WHATSAPP-BAILEYS" {
		t.Errorf("create req=%+v", reqs[0])
	}
}

func TestEvo_ConnectWithPhoneRequestsPairingCodeAndPreservesQR(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) {
		return 200, `{"pairingCode":"ABCD-1234","base64":"data:image/png;base64,QR"}`
	})
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Connect(context.Background(), "34600111222")
	if err != nil {
		t.Fatal(err)
	}
	if st.PairCode != "ABCD-1234" || st.QR == "" {
		t.Fatalf("state=%+v", st)
	}
	if reqs[0].path != "/instance/connect/nv_1" {
		t.Fatalf("path=%q", reqs[0].path)
	}
}

func TestEvo_ConnectEscapesPhoneQuery(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	_, err := newEvoGW(srv.URL).Connect(context.Background(), "34600+111222")
	if err != nil {
		t.Fatal(err)
	}
	// fakeEvolution stores only Path; inspect the query by using a dedicated
	// server below would be overkill—the escaped plus must not become a space.
	if reqs[0].query != "number=34600%2B111222" {
		t.Fatalf("query=%q", reqs[0].query)
	}
}

func TestEvo_ConnectAcceptsProviderCodeAlias(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) {
		return 200, `{"code":"ABCD1234","base64":"QR"}`
	})
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Connect(context.Background(), "34600111222")
	if err != nil || st.PairCode != "ABCD-1234" {
		t.Fatalf("state=%+v err=%v", st, err)
	}
}

func TestEvo_Status_MapsOpen(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) {
		return 200, `{"instance":{"state":"open"}}`
	})
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "connected" {
		t.Errorf("status=%q, want connected", st.Status)
	}
}

func TestEvo_ErrorOnNon2xx(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) { return 500, `{"error":"boom"}` })
	defer srv.Close()
	if err := newEvoGW(srv.URL).SendText(context.Background(), "34600111222", "x"); err == nil {
		t.Error("expected error on 500")
	}
}

func TestEvo_RegisterWebhookRequestsBase64QR(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, nil)
	defer srv.Close()
	if err := newEvoGW(srv.URL).RegisterWebhook(context.Background(), "https://example.com/hook", nil); err != nil {
		t.Fatal(err)
	}
	webhook, _ := reqs[0].body["webhook"].(map[string]any)
	if webhook["webhookBase64"] != true {
		t.Fatalf("webhook=%v", webhook)
	}
}

func TestEvo_ParseInbound_MessagesUpsert(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv_1","data":{"key":{"remoteJid":"34600111222@s.whatsapp.net","fromMe":false,"id":"MID1"},"pushName":"Ana","message":{"conversation":"hola"}}}`)
	in, ok := (&evolutionGateway{}).ParseInboundMessage(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if in.Sender != "34600111222" || in.Text != "hola" || in.MessageID != "MID1" || in.SessionRef != "nv_1" || in.PushName != "Ana" {
		t.Errorf("in=%+v", in)
	}
	// group + fromMe dropped
	if _, ok := (&evolutionGateway{}).ParseInboundMessage([]byte(`{"event":"messages.upsert","instance":"x","data":{"key":{"remoteJid":"123@g.us"}}}`)); ok {
		t.Error("group should be dropped")
	}
}

func TestEvo_ParseInbound_ListReply(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv_1","data":{"key":{"remoteJid":"34600111222@s.whatsapp.net","id":"m2"},"message":{"listResponseMessage":{"title":"Reservar","singleSelectReply":{"selectedRowId":"opt_0"}}}}}`)
	in, ok := (&evolutionGateway{}).ParseInboundMessage(body)
	if !ok || in.Text != "Reservar" {
		t.Errorf("list reply parse: ok=%v in=%+v", ok, in)
	}
}

func TestEvo_ParseConnection_ConnectionUpdate(t *testing.T) {
	in, ok := (&evolutionGateway{}).ParseConnectionEvent([]byte(`{"event":"connection.update","instance":"nv_1","data":{"state":"open","instance":{"wuid":"34692747052@s.whatsapp.net"}}}`))
	if !ok || in.Status != "connected" || in.SessionRef != "nv_1" || in.ConnectedPhone != "34692747052@s.whatsapp.net" {
		t.Errorf("conn parse: ok=%v ev=%+v", ok, in)
	}
	qr, ok := (&evolutionGateway{}).ParseConnectionEvent([]byte(`{"event":"qrcode.updated","instance":"nv_1","data":{"qrcode":{"base64":"data:image/png;base64,AAA"}}}`))
	if !ok || qr.Status != "pending" || qr.QR == "" {
		t.Errorf("qr parse: ok=%v ev=%+v", ok, qr)
	}
	// non-connection event ignored
	if _, ok := (&evolutionGateway{}).ParseConnectionEvent([]byte(`{"event":"messages.upsert","instance":"x","data":{}}`)); ok {
		t.Error("messages event should not parse as connection")
	}
}

func TestEvo_ConnectWithPhoneReturnsPairCodeAndQR(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) {
		return 200, `{"pairingCode":"ABCD-1234","base64":"data:image/png;base64,QR"}`
	})
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Connect(context.Background(), "34600111222")
	if err != nil {
		t.Fatal(err)
	}
	if st.PairCode != "ABCD-1234" || st.QR == "" {
		t.Fatalf("state=%+v", st)
	}
	if reqs[0].query != "number=34600111222" {
		t.Fatalf("query=%q", reqs[0].query)
	}
}

func TestEvo_ConnectEscapesPhoneAndAcceptsCodeAlias(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) { return 200, `{"code":"ABCD1234"}` })
	defer srv.Close()
	st, err := newEvoGW(srv.URL).Connect(context.Background(), "34600+111222")
	if err != nil || st.PairCode != "ABCD-1234" {
		t.Fatalf("state=%+v err=%v", st, err)
	}
	if reqs[0].query != "number=34600%2B111222" {
		t.Fatalf("query=%q", reqs[0].query)
	}
}

func TestEvo_ProvisionDisablesQRCodeForPhonePairing(t *testing.T) {
	var reqs []evoReq
	srv := fakeEvolution(t, &reqs, func(path string) (int, string) { return 201, `{"hash":"h1"}` })
	defer srv.Close()
	gw := newEvoGW(srv.URL)
	if _, err := gw.Provision(context.Background(), "nv_1"); err != nil {
		t.Fatal(err)
	}
	if got, ok := reqs[0].body["qrcode"].(bool); !ok || got {
		t.Fatalf("qrcode=%v", reqs[0].body["qrcode"])
	}
}

func TestEvo_ConnectionEventStatusCodeDoesNotRemainConnecting(t *testing.T) {
	ev, ok := (&evolutionGateway{}).ParseConnectionEvent([]byte(`{"event":"connection.update","instance":"nv_1","data":{"statusCode":401}}`))
	if !ok || ev.Status != "failed_401" {
		t.Fatalf("event=%+v ok=%v", ev, ok)
	}
}
