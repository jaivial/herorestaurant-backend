package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: wa_bot_dspy_pipeline_v1
//
// Every inbound turn is routed through the DSPy decision pipeline (sidecar
// service "bot-pipeline", pipeline/app.py). Go computes the deterministic DB
// facts (same-day booking, extras, allergen regex) and the sidecar combines
// them with Jev categorization into an explicit if/else tree. The result picks
// a handoff guard or the agent route, the RAG knowledge tags, and a short
// route directive. The visited node ids are stored per turn so the backoffice
// can draw the real path taken (/app/config -> Pipeline).
//
// If the sidecar is unreachable the legacy regex guards run unchanged, so the
// bot never goes silent because of the pipeline.

type botPipelineDecision struct {
	Intent     string          `json:"intent"`
	Confidence float64         `json:"confidence"`
	Classifier string          `json:"classifier"`
	Jev        json.RawMessage `json:"jev,omitempty"`
	Action     string          `json:"action"`
	Node       string          `json:"node"`
	Routes     []string        `json:"routes"`
	Directive  string          `json:"directive"`
	Path       []string        `json:"path"`
	ElapsedMS  int             `json:"elapsed_ms"`
}

type botPipelineLM struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

func (s *Server) botPipelineURL() string {
	u := strings.TrimRight(strings.TrimSpace(s.cfg.BotPipelineURL), "/")
	if u == "" {
		u = "http://127.0.0.1:18195"
	}
	return u
}

// botPipelineFacts evaluates the deterministic, DB-backed checks once so the
// pipeline and the handoff actions agree on the same facts.
func (s *Server) botPipelineFacts(ctx context.Context, restaurantID int, msg botWebhookMessage) map[string]any {
	extraNames := make([]string, 0, 8)
	if extras, err := s.loadRestaurantBookingExtras(restaurantID); err == nil {
		for _, extra := range extras {
			extraNames = append(extraNames, extra.Name)
		}
	}
	normalized := normalizeBotIntentText(msg.Text)
	return map[string]any{
		"same_day_intent":       s.botSameDayIntentApplies(ctx, restaurantID, msg) != "",
		"regex_extras_mutation": botExtrasMutationIntent(msg.Text, extraNames),
		"mentions_extras":       botTextMentionsExtras(normalized, extraNames),
		"regex_allergen":        normalized != "" && botAllergenIntentRe.MatchString(normalized),
		"regex_booking_note":    botBookingNoteRe.MatchString(normalized),
		"regex_intent":          botBookingIntentFromText(msg.Text),
	}
}

// botPipelineHistory renders the last turns as short "Rol: texto" lines.
func botPipelineHistory(history []botMessage, max int) []string {
	out := make([]string, 0, max)
	start := len(history) - max
	if start < 0 {
		start = 0
	}
	for _, m := range history[start:] {
		if len(m.Content) == 0 {
			continue
		}
		role := "Cliente"
		if m.Role == "assistant" {
			role = "Restaurante"
		}
		out = append(out, role+": "+truncate(strings.TrimSpace(m.Content[0].Text), 400))
	}
	return out
}

// botRunPipeline asks the DSPy sidecar for the route. ok=false means the
// caller must fall back to the legacy guards.
func (s *Server) botRunPipeline(ctx context.Context, restaurantID int, msg botWebhookMessage, routing botAIRouting, history []botMessage, facts map[string]any) (botPipelineDecision, bool) {
	lms := make([]botPipelineLM, 0, 2)
	for _, ref := range []string{routing.PrimaryModel, routing.FallbackModel} {
		provider, model := botSplitModelRef(ref)
		if key := s.botProviderKey(ctx, restaurantID, provider); key != "" {
			lms = append(lms, botPipelineLM{Provider: provider, Model: model, APIKey: key})
		}
	}
	// History already contains the inbound message as its last user turn.
	hist := history
	if n := len(hist); n > 0 && hist[n-1].Role == "user" {
		hist = hist[:n-1]
	}
	body, _ := json.Marshal(map[string]any{
		"restaurant_id": restaurantID,
		"session_id":    botSessionID(restaurantID, msg.Sender),
		"text":          msg.Text,
		"history":       botPipelineHistory(hist, 6),
		"facts":         facts,
		"jev_api_key":   s.botProviderKey(ctx, restaurantID, botProviderJev),
		"lms":           lms,
	})
	reqCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.botPipelineURL()+"/decide", bytes.NewReader(body))
	if err != nil {
		return botPipelineDecision{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[bot] checkpoint wa_bot_dspy_pipeline_v1 restaurant_id=%d sender=%s unavailable err=%v", restaurantID, msg.Sender, err)
		return botPipelineDecision{}, false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var d botPipelineDecision
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &d) != nil || d.Action == "" {
		log.Printf("[bot] checkpoint wa_bot_dspy_pipeline_v1 restaurant_id=%d sender=%s bad_response status=%d", restaurantID, msg.Sender, resp.StatusCode)
		return botPipelineDecision{}, false
	}
	log.Printf("[bot] checkpoint wa_bot_dspy_pipeline_v1 restaurant_id=%d sender=%s intent=%s confidence=%.2f classifier=%s node=%s ms=%d",
		restaurantID, msg.Sender, d.Intent, d.Confidence, d.Classifier, d.Node, d.ElapsedMS)
	return d, true
}

// botSessionID is the stable per-conversation id shared by the LLM provider
// session header, the pipeline log and the stored decision trace.
func botSessionID(restaurantID int, sender string) string {
	return fmt.Sprintf("wa-%d-%s", restaurantID, digitsOnly(sender))
}

// ---------- backoffice: graph + recent decisions ----------

// GET /api/admin/bot/pipeline/{restaurantId}
func (s *Server) handleBOBotPipelineGet(w http.ResponseWriter, r *http.Request) {
	rid, ok := botSettingsRestaurantID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "restaurantId inválido")
		return
	}
	var graph json.RawMessage
	online := false
	reqCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.botPipelineURL()+"/graph", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && json.Valid(raw) {
				graph, online = raw, true
			}
		}
	}
	decisions, err := s.botConversation.RecentDecisions(r.Context(), rid, 50)
	if err != nil {
		log.Printf("[bot] checkpoint wa_bot_dspy_pipeline_v1 restaurant_id=%d decisions_error=%v", rid, err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "online": online, "graph": graph, "decisions": decisions})
}
