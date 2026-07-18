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

// TestBotWebhook_ConnectionEvent_UpdatesInstance_DB verifies that a UAZAPI
// connection/qr lifecycle event posted to /bot/webhook is mapped to the tenant
// and updates the provisioning row (so the QR onboarding UI stays live).
func TestBotWebhook_ConnectionEvent_UpdatesInstance_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)

	uaz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer uaz.Close()

	rid, cleanup := seedBotRestaurant(t, s)
	defer cleanup()
	token, cleanupInst := seedUAZAPIInstance(t, s, rid, uaz.URL)
	defer cleanupInst()

	// A QR event should move the instance to a pending state and store the QR.
	qrBody, _ := json.Marshal(map[string]any{
		"event":  "qrcode",
		"token":  token,
		"qrcode": "data:image/png;base64,QQQQ",
		"status": "connecting",
	})
	req := httptest.NewRequest(http.MethodPost, "/bot/webhook", bytes.NewReader(qrBody))
	rec := httptest.NewRecorder()
	s.handleBotWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("qr event code = %d body=%s", rec.Code, rec.Body.String())
	}

	instRec, found, err := s.loadRestaurantUAZAPIInstance(context.Background(), rid)
	if err != nil || !found {
		t.Fatalf("load after qr event: found=%v err=%v", found, err)
	}
	if instRec.QRPayload == "" {
		t.Errorf("expected QR payload stored, got empty")
	}

	// A connected event should clear the QR and record the phone.
	connBody, _ := json.Marshal(map[string]any{
		"EventType": "connection",
		"token":     token,
		"status":    "connected",
		"phone":     "34699888777",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/bot/webhook", bytes.NewReader(connBody))
	rec2 := httptest.NewRecorder()
	s.handleBotWebhook(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("connected event code = %d", rec2.Code)
	}
	instRec2, _, _ := s.loadRestaurantUAZAPIInstance(context.Background(), rid)
	if instRec2.Status != "connected" {
		t.Errorf("status after connected event = %q", instRec2.Status)
	}
	if instRec2.QRPayload != "" {
		t.Errorf("expected QR cleared after connect, got %q", instRec2.QRPayload)
	}
	if instRec2.ConnectedPhone != "34699888777" {
		t.Errorf("connected phone = %q", instRec2.ConnectedPhone)
	}
}

// TestBotWebhook_TenantIsolation_DB verifies two restaurants with distinct
// instance tokens resolve to their own restaurant_id and never cross over.
func TestBotWebhook_TenantIsolation_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := newTestServer(t, db)

	uaz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer uaz.Close()

	ridA, cleanupA := seedBotRestaurant(t, s)
	defer cleanupA()
	ridB, cleanupB := seedBotRestaurant(t, s)
	defer cleanupB()

	suffix := time.Now().Format("150405.000000")
	srvRes, _ := db.Exec(`INSERT INTO uazapi_servers (name, base_url, admin_token) VALUES (?, ?, 'a')`, "iso-"+suffix, uaz.URL)
	serverID, _ := srvRes.LastInsertId()
	defer func() { _, _ = db.Exec(`DELETE FROM uazapi_servers WHERE id = ?`, serverID) }()

	seed := func(rid int, token string) {
		if _, err := db.Exec(`
			INSERT INTO restaurant_uazapi_instances (restaurant_id, server_id, instance_name, instance_token, is_active, status)
			VALUES (?, ?, ?, ?, 1, 'connected')
		`, rid, serverID, "inst-"+token, token); err != nil {
			t.Fatalf("seed instance %s: %v", token, err)
		}
	}
	tokenA := "tok-A-" + suffix
	tokenB := "tok-B-" + suffix
	seed(ridA, tokenA)
	seed(ridB, tokenB)
	defer func() {
		_, _ = db.Exec(`DELETE FROM restaurant_uazapi_instances WHERE restaurant_id IN (?, ?)`, ridA, ridB)
	}()

	gotA, okA := s.resolveBotRestaurant(context.Background(), tokenA, "")
	gotB, okB := s.resolveBotRestaurant(context.Background(), tokenB, "")
	if !okA || !okB {
		t.Fatalf("resolve failed: A=%v B=%v", okA, okB)
	}
	if gotA != ridA || gotB != ridB {
		t.Fatalf("cross-tenant leak: gotA=%d wantA=%d gotB=%d wantB=%d", gotA, ridA, gotB, ridB)
	}
	if gotA == gotB {
		t.Fatal("two tenants resolved to the same restaurant")
	}
}
