package api

// Domain purchase billing: Stripe Checkout + webhook.
//
// Flow:
//   POST /admin/site-builder/billing/checkout  { domain, amount_eur } → { url }
//   (user pays on Stripe)
//   POST /stripe/webhook                       checkout.session.completed
//        → mark restaurant_domains paid, create recurring invoice, provision
//          the domain's zone + DNS via Cloudflare.
//
// Stripe restricted key + webhook secret come from env (STRIPE_SECRET_KEY /
// STRIPE_WEBHOOK_SECRET). The webhook is unauthenticated by session — it uses
// Stripe's signature. The checkout endpoint requires a backoffice session +
// ajustes section.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/integrations"
)

// checkoutRequest is the payload from the editor: which domain + price.
type checkoutRequest struct {
	Domain    string `json:"domain"`
	AmountEur string `json:"amount_eur"`
}

func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpxWriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if s.cfg.StripeSecretKey == "" {
		httpxWriteError(w, http.StatusServiceUnavailable, "Stripe no configurado (STRIPE_SECRET_KEY)")
		return
	}

	var req checkoutRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Domain) == "" {
		httpxWriteError(w, http.StatusBadRequest, "Missing domain")
		return
	}
	amountCents, err := strconv.ParseInt(req.AmountEur, 10, 64)
	if err != nil || amountCents <= 0 {
		httpxWriteError(w, http.StatusBadRequest, "Invalid amount_eur (cents)")
		return
	}

	sub := s.instatic.subdomainFor(a.ActiveRestaurantID)

	stripe := integrations.NewStripeClient(s.cfg.StripeSecretKey, s.cfg.StripeWebhookSecret)
	successURL := "https://" + sub + "/?purchase=success"
	cancelURL := "https://" + sub + "/?purchase=cancelled"

	sess, err := stripe.CreateCheckoutSession(r.Context(), amountCents, "eur",
		"Registro de dominio "+req.Domain, req.Domain, successURL, cancelURL,
		map[string]string{"domain": req.Domain, "restaurant_id": strconv.Itoa(a.ActiveRestaurantID)})
	if err != nil {
		httpxWriteError(w, http.StatusBadGateway, "Stripe checkout failed: "+err.Error())
		return
	}

	_, _ = s.db.Exec(`UPDATE restaurant_domains SET stripe_checkout_session_id=?, stripe_payment_status='pending', registration_cost=? WHERE restaurant_id=? AND domain=?`,
		sess.ID, req.AmountEur, a.ActiveRestaurantID, req.Domain)

	writeJSON(w, map[string]any{"success": true, "url": sess.URL, "session_id": sess.ID})
}

// handleStripeWebhook receives Stripe events. Auth is the Stripe signature.
func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StripeWebhookSecret == "" {
		httpxWriteError(w, http.StatusServiceUnavailable, "Stripe webhook no configurado")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpxWriteError(w, http.StatusBadRequest, "read body")
		return
	}
	stripe := integrations.NewStripeClient(s.cfg.StripeSecretKey, s.cfg.StripeWebhookSecret)
	ev, err := stripe.VerifyWebhookSignature(body, r.Header.Get("Stripe-Signature"))
	if err != nil {
		httpxWriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM stripe_events WHERE event_id=?`, ev.ID).Scan(&exists); err == nil {
		writeJSON(w, map[string]any{"success": true, "duplicate": true})
		return
	}
	_, _ = s.db.Exec(`INSERT INTO stripe_events (event_id, type, payload, processed_at) VALUES (?, ?, ?, NOW())`,
		ev.ID, ev.Type, string(body))

	switch ev.Type {
	case "checkout.session.completed":
		s.handleStripeCheckoutCompleted(r.Context(), ev)
	}

	writeJSON(w, map[string]any{"success": true})
}

// handleStripeCheckoutCompleted marks a domain paid and provisions it.
func (s *Server) handleStripeCheckoutCompleted(ctx context.Context, ev *integrations.Event) {
	var obj struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(ev.Data.Object, &obj); err != nil {
		return
	}
	domain := obj.Metadata["domain"]
	restaurantID, _ := strconv.Atoi(obj.Metadata["restaurant_id"])
	if domain == "" || restaurantID == 0 {
		return
	}

	// Mark paid + upsert the domain row (the checkout UPDATE may have matched
	// nothing if the row didn't exist yet).
	_, _ = s.db.Exec(`
		INSERT INTO restaurant_domains (restaurant_id, domain, is_primary, registration_status, stripe_payment_status)
		VALUES (?, ?, 0, 'paid', 'paid')
		ON DUPLICATE KEY UPDATE registration_status='paid', stripe_payment_status='paid'
	`, restaurantID, domain)

	// Provision zone + DNS (proxied → CF TLS) for the custom domain.
	s.provisionDomainSite(ctx, restaurantID, domain)

	// Recurring invoice seam for the annual domain fee.
	var price string
	if p := obj.Metadata["price_eur"]; p != "" {
		price = p
	}
	_, _ = s.db.Exec(`
		INSERT INTO recurring_invoices (restaurant_id, customer_name, customer_email, amount, currency, frequency, start_date, feature_key, concept)
		VALUES (?, (SELECT name FROM restaurants WHERE id=?), '', COALESCE(NULLIF(?, ''), '0.00'), 'EUR', 'yearly', CURDATE(), 'website_domain', ?)
	`, restaurantID, restaurantID, price, domain)
}
