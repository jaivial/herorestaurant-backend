package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Public, no-auth self-service unsubscribe for campaign email / WhatsApp.
// The Preact page /baja-publicidad?b=<bookingID>&c=<channel> is the only client.
// Coordination id prefix: camp-unsub (shared with that page).

const campaignUnsubscribeCoordID = "camp-unsub"

// campaignUnsubscribeMaxBodyBytes keeps the unauthenticated POST bounded.
const campaignUnsubscribeMaxBodyBytes = 8 * 1024

// campaignUnsubscribeGenericName is used whenever the booking id cannot be
// tied to the tenant, so the endpoint never becomes a booking-id oracle.
const campaignUnsubscribeGenericName = "este restaurante"

// campaignUnsubscribeReasons: the 5 allowed reasons. MUST match the Preact
// page options exactly; the keys are what gets stored in campaign_suppressions.
var campaignUnsubscribeReasons = map[string]bool{
	"demasiados_emails": true,
	"no_relevante":      true,
	"nunca_me_suscribi": true,
	"duplicado":         true,
	"otro":              true,
}

type campaignUnsubscribeTarget struct {
	Email string
	Phone string
}

// handlePublicUnsubscribeContext GET /unsubscribe/context?b=<bookingID>
// Renders what the page needs without leaking personal data, and never
// confirms nor denies that a booking id exists (no enumeration oracle).
func (s *Server) handlePublicUnsubscribeContext(w http.ResponseWriter, r *http.Request) {
	restaurantID, _ := restaurantIDFromContext(r.Context())
	bookingID := campaignUnsubscribeBookingID(r.URL.Query().Get("b"))

	// Generic name until the target is proven to belong to this tenant, so an
	// unknown id is indistinguishable from a foreign one. A recipient without a
	// booking (test send, hand-pasted audience) still resolves through its own
	// token, so the page shows the real state instead of a generic one.
	name := campaignUnsubscribeGenericName
	already, masked := false, ""

	target, ok := s.campaignUnsubscribeBookingTarget(r.Context(), restaurantID, bookingID)
	if !ok {
		target, ok = campaignUnsubscribeTokenTarget(r.URL.Query().Get("t"))
	}
	if ok {
		name = s.campaignUnsubscribeRestaurantName(r.Context(), restaurantID)
		already = s.campaignUnsubscribeAlready(r.Context(), restaurantID, target)
		masked = campaignUnsubscribeMask(campaignUnsubscribePick(r.URL.Query().Get("c"), target))
	}

	slog.Default().Info("campaign.unsubscribe.context", "coord_id", campaignUnsubscribeCoordID,
		"restaurant_id", restaurantID, "booking_id", bookingID, "already", already)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"restaurant_name": name,
		"already":         already,
		"masked_target":   masked,
	})
}

// handlePublicUnsubscribe POST /unsubscribe  {booking_id, channel, reason}
// Always answers success:true for a syntactically valid request; an unknown
// booking id simply inserts nothing.
func (s *Server) handlePublicUnsubscribe(w http.ResponseWriter, r *http.Request) {
	restaurantID, _ := restaurantIDFromContext(r.Context())

	var in struct {
		BookingID int64  `json:"booking_id"`
		Channel   string `json:"channel"`
		Reason    string `json:"reason"`
		// Target is the base64url token of the contact when the message had no
		// booking row; the booking id still wins when it resolves.
		Target string `json:"target"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, campaignUnsubscribeMaxBodyBytes)).Decode(&in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Peticion invalida")
		return
	}
	in.Channel = strings.ToLower(strings.TrimSpace(in.Channel))
	if in.Channel != "email" && in.Channel != "whatsapp" && in.Channel != "all" {
		httpx.WriteError(w, http.StatusBadRequest, "Canal invalido")
		return
	}

	slog.Default().Info("campaign.unsubscribe.received", "coord_id", campaignUnsubscribeCoordID,
		"restaurant_id", restaurantID, "booking_id", in.BookingID, "channel", in.Channel)

	target, ok := s.campaignUnsubscribeBookingTarget(r.Context(), restaurantID, in.BookingID)
	if !ok {
		target, ok = campaignUnsubscribeTokenTarget(in.Target)
	}
	stored := 0
	if ok {
		stored = s.campaignUnsubscribeStore(r, restaurantID, in.BookingID, in.Channel, target,
			campaignUnsubscribeReason(in.Reason))
	} else {
		slog.Default().Info("campaign.unsubscribe.unknown_booking", "coord_id", campaignUnsubscribeCoordID,
			"restaurant_id", restaurantID, "channel", in.Channel)
	}

	slog.Default().Info("campaign.unsubscribe.done", "coord_id", campaignUnsubscribeCoordID,
		"restaurant_id", restaurantID, "booking_id", in.BookingID, "channel", in.Channel, "stored", stored)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// campaignUnsubscribeBookingTarget returns the contact of a booking that
// belongs to this restaurant; ok=false when the booking is unknown or foreign.
func (s *Server) campaignUnsubscribeBookingTarget(ctx context.Context, restaurantID int, bookingID int64) (campaignUnsubscribeTarget, bool) {
	if bookingID <= 0 || restaurantID <= 0 {
		return campaignUnsubscribeTarget{}, false
	}
	var t campaignUnsubscribeTarget
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(contact_email), ''), ''), COALESCE(NULLIF(TRIM(contact_phone), ''), '')
		FROM bookings WHERE restaurant_id = ? AND id = ? LIMIT 1
	`, restaurantID, bookingID).Scan(&t.Email, &t.Phone)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Default().Warn("campaign.unsubscribe.lookup_failed", "coord_id", campaignUnsubscribeCoordID,
				"restaurant_id", restaurantID, "booking_id", bookingID, "err", err.Error())
		}
		return campaignUnsubscribeTarget{}, false
	}
	t.Phone = normalizeWhatsAppNumber(t.Phone)
	return t, true
}

// campaignUnsubscribeStore inserts one suppression row per implied channel.
// INSERT IGNORE + uq_suppression make it idempotent.
func (s *Server) campaignUnsubscribeStore(r *http.Request, restaurantID int, bookingID int64, channel string, t campaignUnsubscribeTarget, reason string) int {
	ctx := r.Context()
	type pair struct{ channel, target string }
	pairs := []pair{}
	if channel == "email" || channel == "all" {
		pairs = append(pairs, pair{"email", strings.ToLower(strings.TrimSpace(t.Email))})
	}
	if channel == "whatsapp" || channel == "all" {
		pairs = append(pairs, pair{"whatsapp", t.Phone})
	}

	stored := 0
	for _, p := range pairs {
		if p.target == "" || len(p.target) > 190 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
			VALUES (?, ?, ?, ?, ?, 'unsubscribe')
		`, restaurantID, p.channel, p.target, bookingID, reason); err != nil {
			slog.Default().Warn("campaign.unsubscribe.insert_failed", "coord_id", campaignUnsubscribeCoordID,
				"restaurant_id", restaurantID, "channel", p.channel, "err", err.Error())
			continue
		}
		stored++
		slog.Default().Info("campaign.unsubscribe.suppressed", "coord_id", campaignUnsubscribeCoordID,
			"restaurant_id", restaurantID, "channel", p.channel, "booking_id", bookingID)
	}
	return stored
}

// campaignUnsubscribeAlready reports whether the contact is already suppressed.
func (s *Server) campaignUnsubscribeAlready(ctx context.Context, restaurantID int, t campaignUnsubscribeTarget) bool {
	for _, p := range []struct{ channel, target string }{
		{"email", strings.ToLower(strings.TrimSpace(t.Email))},
		{"whatsapp", t.Phone},
	} {
		if p.target == "" {
			continue
		}
		var one int
		if err := s.db.QueryRowContext(ctx, `
			SELECT 1 FROM campaign_suppressions WHERE restaurant_id = ? AND channel = ? AND target = ? LIMIT 1
		`, restaurantID, p.channel, p.target).Scan(&one); err == nil {
			return true
		}
	}
	return false
}

// campaignUnsubscribeRestaurantName resolves the tenant display name, with a
// generic fallback so a foreign booking id is never distinguishable.
func (s *Server) campaignUnsubscribeRestaurantName(ctx context.Context, restaurantID int) string {
	const generic = campaignUnsubscribeGenericName
	if restaurantID <= 0 {
		return generic
	}
	branding, err := s.loadRestaurantBranding(ctx, restaurantID)
	if err != nil || strings.TrimSpace(branding.BrandName) == "" {
		return generic
	}
	return strings.TrimSpace(branding.BrandName)
}

// campaignUnsubscribePick chooses the target matching the requested channel.
func campaignUnsubscribePick(channel string, t campaignUnsubscribeTarget) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "whatsapp":
		return t.Phone
	default:
		return t.Email
	}
}

// campaignUnsubscribeMask shows just enough to reassure, never the full value.
func campaignUnsubscribeMask(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if at := strings.Index(target, "@"); at > 0 {
		local, domain := target[:at], target[at+1:]
		for _, head := range local {
			return string(head) + "***@" + domain
		}
	}
	if len(target) <= 2 {
		return "**"
	}
	return strings.Repeat("*", len(target)-2) + target[len(target)-2:]
}

// campaignUnsubscribeReason keeps only the allowed values, bounded to the column.
func campaignUnsubscribeReason(raw string) string {
	reason := strings.ToLower(strings.TrimSpace(raw))
	if !campaignUnsubscribeReasons[reason] {
		if reason != "" {
			slog.Default().Info("campaign.unsubscribe.reason_ignored", "coord_id", campaignUnsubscribeCoordID)
		}
		return ""
	}
	if len(reason) > 255 {
		reason = reason[:255]
	}
	return reason
}

// campaignUnsubscribeTokenTarget decodes the ?t= opt-out token (base64url of the
// recipient target) emitted only in the recipient's own email/WhatsApp link, so
// a recipient without a booking row can still stop the campaigns. Invalid or
// oversized values are rejected.
func campaignUnsubscribeTokenTarget(raw string) (campaignUnsubscribeTarget, bool) {
	decoded := campaignDecodeTargetToken(raw)
	if decoded == "" || len(decoded) > 190 {
		return campaignUnsubscribeTarget{}, false
	}
	if strings.Contains(decoded, "@") {
		email := strings.ToLower(strings.TrimSpace(decoded))
		local, domain, found := strings.Cut(email, "@")
		if !found || local == "" || !strings.Contains(domain, ".") {
			return campaignUnsubscribeTarget{}, false
		}
		return campaignUnsubscribeTarget{Email: email}, true
	}
	phone := normalizeWhatsAppNumber(decoded)
	if phone == "" {
		return campaignUnsubscribeTarget{}, false
	}
	return campaignUnsubscribeTarget{Phone: phone}, true
}

// campaignUnsubscribeBookingID parses the ?b= parameter defensively.
func campaignUnsubscribeBookingID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}
