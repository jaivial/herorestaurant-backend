package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWhatsAppDisconnect_ReturnsBeforeProviderAndDeactivates_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()

	releaseProvider := make(chan struct{})
	uaz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseProvider
		w.WriteHeader(http.StatusOK)
	}))
	defer uaz.Close()
	_, cleanupInstance := seedUAZAPIInstance(t, s, rid, uaz.URL)
	defer cleanupInstance()

	req := httptest.NewRequest(http.MethodPost, "/api/admin/members/whatsapp/disconnect", bytes.NewBufferString(`{}`))
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: rid}))
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handleBOMembersWhatsAppDisconnect(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		close(releaseProvider)
		t.Fatal("disconnect waited for provider")
	}
	close(releaseProvider)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var status string
	var active int
	if err := db.QueryRow(`SELECT status, is_active FROM restaurant_uazapi_instances WHERE restaurant_id = ?`, rid).Scan(&status, &active); err != nil {
		t.Fatal(err)
	}
	if status != "disconnected" || active != 0 {
		t.Fatalf("status=%s active=%d", status, active)
	}
}

func TestPollRestaurantWhatsAppPairing_CapturesDelayedQR_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()

	requests := 0
	uaz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			_, _ = w.Write([]byte(`{"instance":{"status":"connecting","qrcode":""}}`))
			return
		}
		_, _ = w.Write([]byte(`{"instance":{"status":"connecting","qrcode":"data:image/png;base64,DELAYED"}}`))
	}))
	defer uaz.Close()
	_, cleanupInstance := seedUAZAPIInstance(t, s, rid, uaz.URL)
	defer cleanupInstance()
	// UAZAPI briefly reports the old disconnected state after /connect while it
	// starts generating the new QR. Active local state must keep watcher alive.
	_, _ = db.Exec(`UPDATE restaurant_uazapi_instances SET status='disconnected', qr_payload=NULL WHERE restaurant_id=?`, rid)

	if err := s.pollRestaurantWhatsAppPairing(context.Background(), rid, 3, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rec, found, err := s.loadRestaurantUAZAPIInstance(context.Background(), rid)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if rec.QRPayload != "data:image/png;base64,DELAYED" || requests != 2 {
		t.Fatalf("qr=%q requests=%d", rec.QRPayload, requests)
	}
}

func TestWhatsAppRuntimeUpdate_PreservesQRDuringConnecting_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	_, cleanupInstance := seedUAZAPIInstance(t, s, rid, "http://127.0.0.1")
	defer cleanupInstance()
	_, _ = db.Exec(`UPDATE restaurant_uazapi_instances SET qr_payload='data:image/png;base64,KEEP' WHERE restaurant_id=?`, rid)

	if err := s.updateRestaurantUAZAPIInstanceRuntime(context.Background(), rid, "connecting", "", "", ""); err != nil {
		t.Fatal(err)
	}
	var qr string
	if err := db.QueryRow(`SELECT COALESCE(qr_payload, '') FROM restaurant_uazapi_instances WHERE restaurant_id=?`, rid).Scan(&qr); err != nil {
		t.Fatal(err)
	}
	if qr != "data:image/png;base64,KEEP" {
		t.Fatalf("qr=%q", qr)
	}
}

func TestWhatsAppWebSocketOriginUsesForwardedHost(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://backend/admin/members/whatsapp/ws", nil)
	req.Header.Set("Origin", "https://localhost:3001")
	req.Header.Set("X-Forwarded-Host", "localhost:3001")
	if !s.allowBOWebSocketOrigin(req) {
		t.Fatal("same-origin proxy request rejected")
	}
	req.Header.Set("Origin", "https://evil.example")
	if s.allowBOWebSocketOrigin(req) {
		t.Fatal("cross-origin request accepted")
	}
	s.cfg.CORSAllowOrigins = "*"
	if s.allowBOWebSocketOrigin(req) {
		t.Fatal("CORS wildcard accepted for cookie WebSocket")
	}
}

func TestWhatsAppConnectionStatus_ReportsEntitlement_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)
	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()

	request := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/members/whatsapp/connection", nil)
		req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: rid}))
		rr := httptest.NewRecorder()
		s.handleBOMembersWhatsAppConnectionStatus(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	locked := request()
	if locked["entitled"] != false || locked["code"] != "NEEDS_SUBSCRIPTION" {
		t.Fatalf("locked response=%v", locked)
	}

	_, err := db.Exec(`
		INSERT INTO recurring_invoices
			(restaurant_id, feature_key, concept, amount, currency, frequency, next_run_at, is_active,
			 customer_name, customer_email, start_date)
		VALUES (?, 'whatsapp_pack', 'test', 0, 'EUR', 'monthly', NOW(), 1,
			'test', 'test@example.com', CURDATE())
	`, rid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`DELETE FROM recurring_invoices WHERE restaurant_id = ? AND feature_key = 'whatsapp_pack'`, rid)
	}()

	eligible := request()
	if eligible["entitled"] != true || eligible["connected"] != false {
		t.Fatalf("eligible response=%v", eligible)
	}
}

func TestWhatsAppConnectionHub_IsolatesRestaurants(t *testing.T) {
	hub := newBOWAConnectionHub()
	a, cancelA := hub.subscribe(1)
	defer cancelA()
	b, cancelB := hub.subscribe(2)
	defer cancelB()

	hub.broadcast(1, map[string]any{"type": "whatsapp.connection", "restaurantId": 1})

	select {
	case raw := <-a:
		if !json.Valid(raw) {
			t.Fatalf("invalid event: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("restaurant 1 did not receive event")
	}

	select {
	case raw := <-b:
		t.Fatalf("restaurant 2 received restaurant 1 event: %s", raw)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestWhatsAppConnectionPayload_HidesInfrastructure(t *testing.T) {
	payload := (&Server{}).whatsappConnectionPayload(uazapiInstanceRecord{
		InstanceName:       "internal-instance",
		ProviderInstanceID: "provider-id",
		ServerBaseURL:      "https://private.example",
		ServerAdminToken:   "admin-secret",
		InstanceToken:      "instance-secret",
		Status:             "pending",
		QRPayload:          "qr",
	})

	for _, key := range []string{"instance_name", "provider_instance_id", "server_base_url", "admin_token", "instance_token"} {
		if _, exists := payload[key]; exists {
			t.Errorf("payload exposes %s", key)
		}
	}
}

func TestWhatsAppDisconnectedStatusIsNotConnected(t *testing.T) {
	if got := normalizeUAZAPIConnectionStatus("disconnected"); got != "disconnected" {
		t.Fatalf("normalized=%q", got)
	}
	payload := (&Server{}).whatsappConnectionPayload(uazapiInstanceRecord{Status: "connected", IsActive: false})
	if anyToBool(payload["connected"]) || payload["status"] != "disconnected" {
		t.Fatalf("inactive payload=%v", payload)
	}
}

func TestWhatsAppConnectionHub_ReplacesStaleEvent(t *testing.T) {
	hub := newBOWAConnectionHub()
	events, cancel := hub.subscribe(1)
	defer cancel()

	hub.broadcast(1, map[string]any{"status": "pending"})
	hub.broadcast(1, map[string]any{"status": "connected"})

	select {
	case raw := <-events:
		var event map[string]any
		_ = json.Unmarshal(raw, &event)
		if event["status"] != "connected" {
			t.Fatalf("event=%s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("latest event not received")
	}
}
