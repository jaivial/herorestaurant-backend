package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/config"
)

func evoWebhookRouter(secret string) http.Handler {
	s := &Server{cfg: config.Config{EvolutionWebhookSecret: secret}}
	r := chi.NewRouter()
	r.Post("/bot/webhook/evolution/{secret}", s.handleBotWebhookEvolution)
	return r
}

func TestEvoWebhook_SecretGate(t *testing.T) {
	msg := `{"event":"messages.upsert","instance":"nv_1","data":{"key":{"remoteJid":"34600111222@s.whatsapp.net","id":"m1"},"message":{"conversation":"hola"}}}`

	// Wrong secret → rejected before any DB access.
	rr := httptest.NewRecorder()
	evoWebhookRouter("right").ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bot/webhook/evolution/wrong", strings.NewReader(msg)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"processed":false`) {
		t.Errorf("wrong secret: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// Empty configured secret → always rejected (no open webhook).
	rr = httptest.NewRecorder()
	evoWebhookRouter("").ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bot/webhook/evolution/anything", strings.NewReader(msg)))
	if !strings.Contains(rr.Body.String(), `"processed":false`) {
		t.Errorf("empty secret should reject: %s", rr.Body.String())
	}
}

// With the right secret, a payload that parses as neither a message nor a
// connection event is a no-op (no DB touched).
func TestEvoWebhook_RightSecret_UnparseableIsNoop(t *testing.T) {
	rr := httptest.NewRecorder()
	evoWebhookRouter("sek").ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bot/webhook/evolution/sek", strings.NewReader(`{"event":"presence.update","instance":"x","data":{}}`)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"processed":false`) {
		t.Errorf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
