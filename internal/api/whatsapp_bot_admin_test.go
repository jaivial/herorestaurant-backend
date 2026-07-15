package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/config"
)

// TestBotLLMCall_ModelOverride asserts the per-tenant model wins over config.
func TestBotLLMCall_ModelOverride(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotReq)
		_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []map[string]any{}})
	}))
	defer srv.Close()

	s := newBotTestServer(srv.URL)
	if _, err := s.botLLMCall(context.Background(), "MiniMax-M2", "sys", []botMessage{botUserText("x")}, nil); err != nil {
		t.Fatal(err)
	}
	if gotReq["model"] != "MiniMax-M2" {
		t.Errorf("model = %v, want MiniMax-M2", gotReq["model"])
	}

	// Empty override falls back to BotModel.
	if _, err := s.botLLMCall(context.Background(), "", "sys", []botMessage{botUserText("x")}, nil); err != nil {
		t.Fatal(err)
	}
	if gotReq["model"] != "MiniMax-M3" {
		t.Errorf("model = %v, want MiniMax-M3", gotReq["model"])
	}
}

func TestParseBotTenantConfig_Model(t *testing.T) {
	cfg := parseBotTenantConfig(`{"model":"MiniMax-M2"}`)
	if cfg.Model != "MiniMax-M2" {
		t.Errorf("Model = %q", cfg.Model)
	}
	if parseBotTenantConfig("").Model != "" {
		t.Error("empty config must not set a model")
	}
}

// TestBOBotSettings_DB drives GET/PUT /api/admin/bot/settings/{restaurantId}:
// save config with model, read it back with prompt preview, editable rules and
// live restaurant data (contact phone prefilled from restaurant_info).
func TestBOBotSettings_DB(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	s := NewServer(db, config.Config{BotModel: "MiniMax-M3"})
	rid, cleanup := seedRestaurant(t, db, "bot-admin-"+time.Now().Format("150405.000"))
	defer cleanup()
	defer func() {
		_, _ = db.Exec(`DELETE FROM whatsapp_bot_config WHERE restaurant_id = ?`, rid)
		_, _ = db.Exec(`DELETE FROM restaurant_info WHERE restaurant_id = ?`, rid)
	}()
	if _, err := db.Exec(`
		INSERT INTO restaurant_info (restaurant_id, direccion, telefono, email)
		VALUES (?, 'Calle Bot 5', '+34 900 111 222', 'bot@test.es')
	`, rid); err != nil {
		t.Fatalf("seed restaurant_info: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/api/admin/bot/settings/{restaurantId}", s.handleBOBotSettingsGet)
	r.Put("/api/admin/bot/settings/{restaurantId}", s.handleBOBotSettingsPut)
	r.Post("/api/admin/bot/settings/{restaurantId}/preview", s.handleBOBotSettingsPreview)

	ridStr := jsonInt(int64(rid))

	// PUT: save model + tone.
	body := strings.NewReader(`{"model":"MiniMax-M2","tone":"formal","language_default":"es"}`)
	req := withAdmin(httptest.NewRequest(http.MethodPut, "/api/admin/bot/settings/"+ridStr, body), rid, 1)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}

	// GET: config round-trips and promptPreview is rendered.
	req = withAdmin(httptest.NewRequest(http.MethodGet, "/api/admin/bot/settings/"+ridStr, nil), rid, 1)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Success       bool            `json:"success"`
		Config        botTenantConfig `json:"config"`
		PromptPreview string          `json:"promptPreview"`
		DefaultModel  string          `json:"defaultModel"`
		DefaultRules  string          `json:"defaultRules"`
		Restaurant    struct {
			BrandName  string   `json:"brandName"`
			Phone      string   `json:"phone"`
			Address    string   `json:"address"`
			RiceTypes  []string `json:"riceTypes"`
			DailyLimit int      `json:"dailyLimit"`
		} `json:"restaurant"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.Config.Model != "MiniMax-M2" || out.Config.Tone != "formal" {
		t.Errorf("config = %+v", out.Config)
	}
	if out.DefaultModel != "MiniMax-M3" {
		t.Errorf("defaultModel = %q", out.DefaultModel)
	}
	if !strings.Contains(out.PromptPreview, "ASISTENTE DE RESERVAS POR WHATSAPP") {
		t.Errorf("promptPreview missing header: %.120s", out.PromptPreview)
	}
	if !strings.Contains(out.PromptPreview, "formal") {
		t.Error("promptPreview must reflect the saved tone")
	}
	if !strings.Contains(out.DefaultRules, "send_message") {
		t.Errorf("defaultRules missing: %.80s", out.DefaultRules)
	}
	// Contact phone prefilled from restaurant_info.telefono when override empty.
	if out.Config.ContactPhone != "+34 900 111 222" {
		t.Errorf("contact_phone = %q, want prefill from restaurant_info", out.Config.ContactPhone)
	}
	if out.Restaurant.Phone != "+34 900 111 222" || out.Restaurant.Address != "Calle Bot 5" {
		t.Errorf("restaurant data = %+v", out.Restaurant)
	}

	// Draft preview (POST /preview): rules override reflected WITHOUT saving.
	body = strings.NewReader(`{"language_default":"es","rules":"REGLA-DRAFT-999"}`)
	req = withAdmin(httptest.NewRequest(http.MethodPost, "/api/admin/bot/settings/"+ridStr+"/preview", body), rid, 1)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", rec.Code, rec.Body.String())
	}
	var prev struct {
		PromptPreview string `json:"promptPreview"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &prev)
	if !strings.Contains(prev.PromptPreview, "REGLA-DRAFT-999") {
		t.Error("draft preview must contain the draft rules")
	}
	// Saved config untouched by preview.
	req = withAdmin(httptest.NewRequest(http.MethodGet, "/api/admin/bot/settings/"+ridStr, nil), rid, 1)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Config.Rules != "" {
		t.Errorf("preview must not persist rules, got %q", out.Config.Rules)
	}

	// Invalid restaurant id → 400.
	req = withAdmin(httptest.NewRequest(http.MethodGet, "/api/admin/bot/settings/0", nil), rid, 1)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET rid=0 status = %d", rec.Code)
	}
}
