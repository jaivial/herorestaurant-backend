package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBotWebhook_EndToEnd_DB drives a full turn: UAZAPI webhook → tenant
// resolution → subscription gate → agent loop (fake MiniMax) → send/text
// (fake UAZAPI) → transcript persisted.
func TestBotWebhook_EndToEnd_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	// Fake UAZAPI server capturing /send/text calls.
	var mu sync.Mutex
	var sentTexts []map[string]any
	uaz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/send/") {
			raw, _ := io.ReadAll(r.Body)
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			mu.Lock()
			sentTexts = append(sentTexts, p)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer uaz.Close()

	// Fake MiniMax: first call → send_message tool; second → end_turn.
	llmCalls := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalls++
		if llmCalls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stop_reason": "tool_use",
				"content": []map[string]any{
					{"type": "tool_use", "id": "tu_1", "name": "send_message",
						"input": map[string]any{"message": "¡Hola Jaime! ¿En qué te ayudo?"}},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []map[string]any{},
		})
	}))
	defer llm.Close()

	s := newTestServer(t, db)
	s.cfg.MiniMaxAPIKey = "test-key"
	s.cfg.MiniMaxBaseURL = llm.URL
	s.cfg.BotModel = "MiniMax-M3"
	s.cfg.BotTimeout = 5 * time.Second
	s.cfg.BotMaxTokens = 512
	s.cfg.BotMaxIterations = 5

	rid, cleanup := seedRestaurant(t, db, "bot-e2e-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer func() {
		_, _ = db.Exec(`DELETE FROM whatsapp_bot_sessions WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM restaurant_uazapi_instances WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM uazapi_servers WHERE base_url = ?`, uaz.URL)
		_, _ = db.Exec(`DELETE FROM recurring_invoices WHERE restaurant_id = ?`, rid)
	}()

	// Provision UAZAPI instance for the tenant.
	res, err := db.Exec(`INSERT INTO uazapi_servers (name, base_url, admin_token) VALUES (?, ?, ?)`,
		"e2e", uaz.URL, "admin-tok")
	if err != nil {
		t.Fatalf("seed uazapi server: %v", err)
	}
	serverID, _ := res.LastInsertId()
	instanceToken := "e2e-token-" + time.Now().Format("150405.000")
	if _, err := db.Exec(`
		INSERT INTO restaurant_uazapi_instances (restaurant_id, server_id, instance_name, instance_token, is_active)
		VALUES (?, ?, ?, ?, 1)
	`, rid, serverID, "inst-"+instanceToken, instanceToken); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	webhookBody := func() []byte {
		raw, _ := json.Marshal(map[string]any{
			"EventType": "messages",
			"token":     instanceToken,
			"message": map[string]any{
				"chatid":    "34612345678@s.whatsapp.net",
				"text":      "hola",
				"fromMe":    false,
				"pushname":  "Jaime",
				"messageid": "E2E-" + time.Now().Format("150405.000000"),
			},
		})
		return raw
	}

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/bot/webhook", bytes.NewReader(webhookBody()))
		rec := httptest.NewRecorder()
		s.handleBotWebhook(rec, req)
		return rec
	}

	// 1. Without subscription: NEEDS_SUBSCRIPTION.
	rec := post()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "NEEDS_SUBSCRIPTION") {
		t.Fatalf("expected NEEDS_SUBSCRIPTION, got %d %s", rec.Code, rec.Body.String())
	}

	// 2. Activate WhatsApp Pack and retry.
	if _, err := db.Exec(`
		INSERT INTO recurring_invoices
			(restaurant_id, feature_key, is_active, amount, currency, customer_name, customer_email, start_date)
		VALUES (?, ?, 1, 49.0, 'EUR', 'Bot E2E', 'bot@e2e.test', CURDATE())
	`, rid, boPremiumWhatsAppFeatureKey); err != nil {
		t.Fatalf("seed subscription: %v", err)
	}

	rec = post()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"processed":true`) {
		t.Fatalf("expected processed, got %d %s", rec.Code, rec.Body.String())
	}

	// Wait for the async agent turn.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sentTexts)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	if len(sentTexts) != 1 {
		mu.Unlock()
		t.Fatalf("sentTexts = %v", sentTexts)
	}
	payload := sentTexts[0]
	mu.Unlock()
	if payload["number"] != "34612345678" || payload["text"] != "¡Hola Jaime! ¿En qué te ayudo?" {
		t.Errorf("payload = %v", payload)
	}

	// Transcript persisted in SQLite: user + assistant messages.
	var history []botMessage
	for time.Now().Before(deadline) {
		history, _ = s.botConversation.History(context.Background(), rid, "34612345678")
		if len(history) == 2 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Errorf("SQLite transcript = %+v", history)
	}

	// 3. Unknown instance token → 401.
	badBody, _ := json.Marshal(map[string]any{
		"token":   "bogus-token",
		"message": map[string]any{"chatid": "34612345678@s.whatsapp.net", "text": "hola", "messageid": "X1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/bot/webhook", bytes.NewReader(badBody))
	recBad := httptest.NewRecorder()
	s.handleBotWebhook(recBad, req)
	if recBad.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unknown instance, got %d", recBad.Code)
	}
}
