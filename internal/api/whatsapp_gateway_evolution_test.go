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
	method, path, apikey string
	body                 map[string]any
}

func fakeEvolution(t *testing.T, reqs *[]evoReq, responder func(path string) (int, string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		b := map[string]any{}
		_ = json.Unmarshal(raw, &b)
		*reqs = append(*reqs, evoReq{method: r.Method, path: r.URL.Path, apikey: r.Header.Get("apikey"), body: b})
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
		t.Errorf("buttons=%v", buttons)
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
