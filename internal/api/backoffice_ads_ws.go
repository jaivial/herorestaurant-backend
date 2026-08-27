package api

import (
	"context"
	"strings"
	"time"
)

// Concrete error codes for the ad AI image pipeline. They travel over the
// restaurant-scoped WebSocket (fichaje hub) so the backoffice can render an
// actionable toast even when an intermediary (Cloudflare/nginx) replaces the
// HTTP error body of the original request.
const (
	boAdAIErrorInsufficientCredits = "insufficient_credits"
	boAdAIErrorGeneric             = "generic"
)

// classifyBOAdAIError maps a provider failure to a stable error code plus the
// raw detail for logging/debugging. Match on lowercase substrings so provider
// wording changes (capitalisation, extra punctuation) keep classifying.
func classifyBOAdAIError(err error) (string, string) {
	if err == nil {
		return boAdAIErrorGeneric, ""
	}
	detail := strings.TrimSpace(err.Error())
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "insufficient credits") || strings.Contains(lower, "top up your account") {
		return boAdAIErrorInsufficientCredits, detail
	}
	return boAdAIErrorGeneric, detail
}

// broadcastBOAdImageFailed emits an ad_image_failed event on the restaurant
// WebSocket hub. Best-effort: hub nil / no clients are no-ops, the HTTP error
// response remains the source of truth.
func (s *Server) broadcastBOAdImageFailed(ctx context.Context, restaurantID int, adID int64, code, message string) {
	if s == nil || s.fichajeHub == nil || restaurantID <= 0 || adID <= 0 {
		return
	}
	_ = ctx
	s.fichajeHub.broadcast(restaurantID, map[string]any{
		"type":    "ad_image_failed",
		"adId":    adID,
		"code":    code,
		"message": message,
		"at":      time.Now().UTC().Format(time.RFC3339),
	})
}
