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

// captured records one inbound HTTP request to the fake provider.
type captured struct {
	path  string
	query string
	body  map[string]any
}

func fakeProvider(t *testing.T, sink *captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.path = r.URL.Path
		sink.query = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		sink.body = map[string]any{}
		_ = json.Unmarshal(raw, &sink.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"connected"}`))
	}))
}

// Regression fence: the UAZAPI gateway must keep the exact wire format
// (?token= query + /send/text) after the abstraction refactor.
func TestUazapiGateway_SendText_WireFormat(t *testing.T) {
	var got captured
	srv := fakeProvider(t, &got)
	defer srv.Close()

	gw := &uazapiGateway{baseURL: srv.URL, instanceToken: "tok123"}
	if err := gw.SendText(context.Background(), "34600111222", "hola"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if got.path != "/send/text" {
		t.Errorf("path = %q, want /send/text", got.path)
	}
	if !strings.Contains(got.query, "token=tok123") {
		t.Errorf("query = %q, want token=tok123", got.query)
	}
	if got.body["number"] != "34600111222" || got.body["text"] != "hola" {
		t.Errorf("body = %v", got.body)
	}
}

func TestUazapiGateway_SendMenu_WireFormat(t *testing.T) {
	var got captured
	srv := fakeProvider(t, &got)
	defer srv.Close()

	gw := &uazapiGateway{baseURL: srv.URL, instanceToken: "t"}
	if err := gw.SendMenu(context.Background(), "34600111222", "Elige", []string{"A", "B"}); err != nil {
		t.Fatalf("SendMenu: %v", err)
	}
	if got.path != "/send/menu" || got.body["type"] != "button" {
		t.Errorf("path=%q type=%v, want /send/menu button", got.path, got.body["type"])
	}
}

// Interface conformance is enforced at compile time by var _ WhatsAppGateway in
// the adapter file; this asserts the parse delegation stays wired.
func TestUazapiGateway_ParseInbound_Delegates(t *testing.T) {
	gw := &uazapiGateway{}
	body := []byte(`{"token":"inst-tok","message":{"chatid":"34600111222@s.whatsapp.net","text":"hola","messageid":"m1"}}`)
	in, ok := gw.ParseInboundMessage(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if in.Sender != "34600111222" || in.Text != "hola" || in.MessageID != "m1" || in.SessionRef != "inst-tok" {
		t.Errorf("waInbound = %+v", in)
	}
}

func TestGatewayForInstance_SelectsByProvider(t *testing.T) {
	s := &Server{}
	evo := s.gatewayForInstance(uazapiInstanceRecord{Provider: "evolution", ServerBaseURL: "http://x", ServerAdminToken: "k", ProviderInstanceID: "nv_1"})
	if _, ok := evo.(*evolutionGateway); !ok {
		t.Errorf("provider=evolution → %T, want *evolutionGateway", evo)
	}
	uaz := s.gatewayForInstance(uazapiInstanceRecord{Provider: "uazapi", ServerBaseURL: "http://x", InstanceToken: "t"})
	if _, ok := uaz.(*uazapiGateway); !ok {
		t.Errorf("provider=uazapi → %T, want *uazapiGateway", uaz)
	}
	// gatewayForServer maps admin_token → the right credential slot.
	es := s.gatewayForServer(uazapiServerRecord{Provider: "evolution", BaseURL: "http://x", AdminToken: "gk"}, "nv_2").(*evolutionGateway)
	if es.apiKey != "gk" || es.instanceName != "nv_2" {
		t.Errorf("evolution server gw = %+v", es)
	}
}

func TestEvolutionConnState_PrefersBase64QR(t *testing.T) {
	state := (&evolutionGateway{}).evoConnState(map[string]any{
		"code":        "2@RAW-PROTOCOL-CODE",
		"base64":      "data:image/png;base64,REAL-PNG",
		"pairingCode": nil,
	})
	if state.QR != "data:image/png;base64,REAL-PNG" || state.PairCode != "" {
		t.Fatalf("state=%+v", state)
	}
}

func TestEvolutionGateway_SendMenu_UsesInteractiveButtons(t *testing.T) {
	var got captured
	srv := fakeProvider(t, &got)
	defer srv.Close()

	gw := &evolutionGateway{s: &Server{}, baseURL: srv.URL, apiKey: "key", instanceName: "nv-1"}
	if err := gw.SendMenu(context.Background(), "34600111222", "Elige", []string{"Reservar", "Ver carta"}); err != nil {
		t.Fatal(err)
	}
	if got.path != "/message/sendButtons/nv-1" {
		t.Fatalf("path=%q body=%v", got.path, got.body)
	}
	buttons, ok := got.body["buttons"].([]any)
	if !ok || len(buttons) != 2 {
		t.Fatalf("buttons=%v", got.body["buttons"])
	}
}
