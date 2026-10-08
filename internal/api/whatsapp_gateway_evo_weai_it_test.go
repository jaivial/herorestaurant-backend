package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestEvoWeaiIT_GatewayLifecycle drives the Evolution gateway against a real
// evo-weai server. It never links a phone: it provisions a throwaway instance,
// asks for a QR, checks the state and the webhook ownership guard, and deletes
// the instance.
//
//	EVO_WEAI_IT_URL=http://127.0.0.1:8119 EVO_WEAI_IT_KEY=<global key> go test ./internal/api -run EvoWeaiIT
//
// Coordination id: wa_evo_weai_v1
func TestEvoWeaiIT_GatewayLifecycle(t *testing.T) {
	base, key := os.Getenv("EVO_WEAI_IT_URL"), os.Getenv("EVO_WEAI_IT_KEY")
	if base == "" || key == "" {
		t.Skip("EVO_WEAI_IT_URL / EVO_WEAI_IT_KEY not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := &Server{}
	name := fmt.Sprintf("nv-it-%d", time.Now().UnixNano())
	admin := s.gatewayForServer(uazapiServerRecord{Provider: "evolution", BaseURL: base, AdminToken: key}, name)

	prov, err := admin.Provision(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	gw := s.gatewayForInstance(uazapiInstanceRecord{Provider: "evolution", ServerBaseURL: base, ServerAdminToken: key, ProviderInstanceID: prov.ProviderInstanceID})
	evo := gw.(*evolutionGateway)
	defer func() {
		if err := gw.Delete(context.Background()); err != nil {
			t.Errorf("delete: %v", err)
		}
		if _, code, _ := evo.request(context.Background(), http.MethodGet, "/instance/connectionState/"+name, nil); code != http.StatusNotFound {
			t.Errorf("instance still there after delete: %d", code)
		}
	}()
	if prov.ProviderInstanceID != name || prov.SessionRef == "" || prov.SessionRef == name {
		t.Fatalf("provision=%+v (the instance token is the session ref)", prov)
	}

	st, err := gw.Connect(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.QR, "data:image/") || st.PairCode != "" {
		t.Fatalf("connect: qr=%.30q pair=%q", st.QR, st.PairCode)
	}
	st, err = gw.Status(ctx)
	if err != nil || isUAZAPIConnected(st.Status) || st.Status == "" {
		t.Fatalf("status=%+v err=%v", st, err)
	}

	// Webhook: set, then the ownership guard refuses another host and lets
	// the same host (and an explicit claim) through. evo-weai resolves the
	// host when the webhook is set (it refuses internal targets), so these are
	// real public names; nothing is ever delivered to them in this test.
	const own = "https://example.com/bot/webhook/evolution/s3cret"
	if err := gw.RegisterWebhook(ctx, own, nil); err != nil {
		t.Fatal(err)
	}
	found, _, err := evo.request(ctx, http.MethodGet, "/webhook/find/"+name, nil)
	if err != nil || found["url"] != own || found["enabled"] != true || found["webhookBase64"] != true {
		t.Fatalf("webhook/find=%v err=%v", found, err)
	}
	events, _ := found["events"].([]any)
	if len(events) != 3 {
		t.Fatalf("events=%v", events)
	}
	if err := gw.RegisterWebhook(ctx, "https://example.org/bot/webhook/evolution/x", nil); err != errWebhookOwnedElsewhere {
		t.Fatalf("another host must not take the webhook: %v", err)
	}
	if err := gw.RegisterWebhook(withWebhookClaim(ctx), "https://example.org/bot/webhook/evolution/x", nil); err != nil {
		t.Fatalf("an explicit connect claims it: %v", err)
	}

	// Sending without a linked phone is refused by the server, and surfaces
	// as an error (the outbox retries it later), not as a silent success.
	if err := gw.SendText(ctx, "34600111222", "hola"); err == nil {
		t.Fatal("sending through an unlinked instance must fail")
	}
}
