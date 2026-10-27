package api

import (
	"context"
	"log"
	"time"

	"preactvillacarmen/internal/aiusage"
)

// Feature labels stored in ai_usage_logs.feature. They are stable identifiers
// used by the Estadísticas aggregation and Forky's ai_usage_report tool.
const (
	aiFeatureForky       = "forky_assistant"
	aiFeatureWhatsAppBot = "whatsapp_bot"
	aiFeatureTranslation = "translation"
	aiFeatureStock       = "stock_ai"
	aiFeatureStockOCR    = "stock_ocr"
	// Campaign HTML builder assistant (campaign_html_builder_ai_v1).
	aiFeatureCampaignBuilder = "campaign_builder"
)

// recordAIUsage persists one LLM request and fans the event out on the global
// socket so the Estadísticas tab updates live. Best effort: observability must
// never break the user-facing call. Coordination id: ai_usage_logs_v1.
func (s *Server) recordAIUsage(ctx context.Context, restaurantID int, feature, provider, model string, tokens aiusage.Tokens, fallback bool) {
	if s == nil || s.db == nil || restaurantID <= 0 {
		return
	}
	now := time.Now()
	cost, priced := aiusage.Cost(provider, model, tokens, now)
	canonicalProvider, _ := aiusage.Normalize(provider, model)
	writeCtx := context.WithoutCancel(ctx)
	res, err := s.db.ExecContext(writeCtx, `INSERT INTO ai_usage_logs
        (restaurant_id,feature,provider,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd,priced,is_fallback,created_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		restaurantID, feature, canonicalProvider, model,
		tokens.Input, tokens.Output, tokens.CacheRead, tokens.CacheWrite,
		cost, boolInt(priced), boolInt(fallback), now)
	if err != nil {
		if !isSQLSchemaError(err) {
			log.Printf("[ai_usage_logs_v1] insert failed restaurant_id=%d feature=%s model=%s err=%v", restaurantID, feature, model, err)
		}
		return
	}
	id, _ := res.LastInsertId()
	s.broadcastBOGlobal(restaurantID, "ai_usage", map[string]any{
		"type":          "ai_usage_recorded",
		"id":            id,
		"restaurant_id": restaurantID,
		"feature":       feature,
		"provider":      canonicalProvider,
		"model":         model,
		"cost_usd":      cost,
		"priced":        priced,
		"created_at":    now.UTC().Format(time.RFC3339Nano),
	})
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func helperAnthropicUsage(input, output, cacheRead, cacheWrite int64) aiusage.Tokens {
	return aiusage.Tokens{Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite}
}

// logWhatsAppMessage appends one message to the MySQL ledger and publishes it
// to open Estadísticas tabs. role follows the transcript convention: "user"
// is inbound, "assistant" is outbound. Coordination id:
// whatsapp_message_logs_v1.
func (s *Server) logWhatsAppMessage(ctx context.Context, restaurantID int, userPhone, role, content, toolName, source string) {
	if s == nil || s.db == nil || restaurantID <= 0 || content == "" {
		return
	}
	direction := "outbound"
	status := "sent"
	if role == "user" {
		direction = "inbound"
		status = "received"
	}
	messageType := "text"
	switch toolName {
	case "send_location", "location":
		messageType = "location"
	case "send_contact", "contact":
		messageType = "contact"
	case "send_document", "send_image", "send_media", "image", "document", "audio", "video":
		messageType = "media"
	}
	now := time.Now()
	writeCtx := context.WithoutCancel(ctx)
	res, err := s.db.ExecContext(writeCtx, `INSERT INTO whatsapp_message_logs
        (restaurant_id,direction,message_type,user_phone,content,source,status,tool_name,created_at)
        VALUES (?,?,?,?,?,?,?,?,?)`,
		restaurantID, direction, messageType, userPhone, content, source, status, toolName, now)
	if err != nil {
		if !isSQLSchemaError(err) {
			log.Printf("[whatsapp_message_logs_v1] insert failed restaurant_id=%d direction=%s source=%s err=%v", restaurantID, direction, source, err)
		}
		return
	}
	id, _ := res.LastInsertId()
	s.broadcastBOGlobal(restaurantID, "whatsapp_log", map[string]any{
		"type":          "whatsapp_message_recorded",
		"id":            id,
		"restaurant_id": restaurantID,
		"direction":     direction,
		"status":        status,
		"created_at":    now.UTC().Format(time.RFC3339Nano),
	})
}
