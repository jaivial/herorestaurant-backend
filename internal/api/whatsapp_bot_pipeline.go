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
	// v2 meters and handoff metadata (wa_bot_dspy_pipeline_v2).
	Anger          float64 `json:"anger"`
	CanHandle      float64 `json:"can_handle"`
	HandoffReason  string  `json:"handoff_reason,omitempty"`
	HandoffText    string  `json:"handoff_text,omitempty"`
	HandoffTopic   string  `json:"handoff_topic,omitempty"`
	HandoffCleared bool    `json:"handoff_cleared,omitempty"`
	SpecialDate    string  `json:"special_date,omitempty"`
	Language       string  `json:"language,omitempty"`
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
		"bookings":              s.botPipelineBookingFacts(ctx, restaurantID, msg.Sender),
		"special_dates":         s.botPipelineSpecialDateFacts(ctx, restaurantID),
		"handoff":               s.botPipelineHandoffFact(ctx, restaurantID, msg.Sender),
	}
}

// botPipelineBookingFacts: the customer's upcoming bookings with the staff
// commentary signals, event and special-date flags (wa_bot_booking_context_v2).
func (s *Server) botPipelineBookingFacts(ctx context.Context, restaurantID int, sender string) []map[string]any {
	bookings, err := s.botFindBookings(ctx, restaurantID, sender)
	if err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(bookings))
	for _, b := range bookings {
		out = append(out, map[string]any{
			"booking_id": b.ID, "date": b.Date, "time": b.Time, "people": b.People,
			"is_event": b.IsEvent, "is_special_booking": b.IsSpecialBooking || b.SpecialDateTitle != "",
			"special_date_title": b.SpecialDateTitle, "commentary_signals": botCommentarySignals(b.Commentary),
		})
	}
	return out
}

func (s *Server) botPipelineSpecialDateFacts(ctx context.Context, restaurantID int) []map[string]any {
	dates := s.botUpcomingSpecialDates(ctx, restaurantID)
	out := make([]map[string]any, 0, len(dates))
	for _, d := range dates {
		out = append(out, map[string]any{
			"key": "sd_" + strings.ReplaceAll(d.Date, "-", "_"), "date": d.Date,
			"label": d.Title + " (" + botFormatISODateES(d.Date) + ")", "prereserva": d.PrereservaEnabled,
		})
	}
	return out
}

// botHandoffTTL: an open human topic expires after 7 days of silence, so an
// old conversation resumed with a new topic is not blocked forever.
const botHandoffTTL = 7 * 24 * time.Hour

func (s *Server) botPipelineHandoffFact(ctx context.Context, restaurantID int, sender string) map[string]any {
	h, err := s.botConversation.GetHandoff(ctx, restaurantID, sender)
	if err != nil || h == nil || time.Since(time.UnixMilli(h.UpdatedAt)) > botHandoffTTL {
		return nil
	}
	return map[string]any{"topic": h.Topic, "reason": h.Reason, "repeats": h.Repeats}
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

// botApplyPipelineHandoff executes the handoff actions of the decision tree.
// Every human handoff opens (or refreshes) the sticky topic so consecutive
// messages on the same topic keep getting the contact card
// (wa_bot_sticky_handoff_v1). Returns true when the turn is fully handled.
func (s *Server) botApplyPipelineHandoff(ctx context.Context, restaurantID int, msg botWebhookMessage, tenant botTenantConfig, d botPipelineDecision) bool {
	topic := strings.TrimSpace(d.HandoffTopic)
	open := func(reason string) {
		if topic == "" {
			topic = truncate(msg.Text, 160)
		}
		_ = s.botConversation.SetHandoff(ctx, restaurantID, msg.Sender, topic, reason, d.HandoffReason == "repeat")
		log.Printf("[bot] checkpoint wa_bot_sticky_handoff_v1 restaurant_id=%d sender=%s reason=%s repeat=%t", restaurantID, msg.Sender, reason, d.HandoffReason == "repeat")
	}
	switch d.Action {
	case "handoff_same_day":
		return s.botSameDayIntentGuard(ctx, restaurantID, msg, tenant)
	case "handoff_extras":
		log.Printf("[bot] checkpoint booking_extras_change_blocked restaurant_id=%d sender=%s", restaurantID, msg.Sender)
		s.botBlockExtrasChange(ctx, restaurantID, msg, tenant)
		open("extras")
		return true
	case "handoff_allergens":
		s.botSendAllergenNotice(ctx, restaurantID, msg, tenant)
		open("allergens")
		return true
	case "handoff_event":
		s.botManagementHandoff(ctx, restaurantID, msg, tenant, botLocalizedHandoff(botEventHandoffText, botEventHandoffTextEN, d.Language), "event_booking", d.Intent)
		open("event")
		return true
	case "handoff_special_booking":
		s.botManagementHandoff(ctx, restaurantID, msg, tenant, botLocalizedHandoff(botSpecialBookingHandoffText, botSpecialBookingHandoffTextEN, d.Language), "special_date_booking", d.Intent)
		open("special_date_booking")
		return true
	case "handoff_human":
		text := strings.TrimSpace(d.HandoffText)
		if text == "" {
			text = botContactIntroText("")
		}
		s.botManagementHandoff(ctx, restaurantID, msg, tenant, text, "human_"+d.HandoffReason, d.Intent)
		open(d.HandoffReason)
		return true
	}
	return false
}
