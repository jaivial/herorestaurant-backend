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
	"database/sql"
	"errors"
	"fmt"
	"time"

	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/integrations"
)

// checkoutRequest is the payload from the editor: which domain + price.
type checkoutRequest struct {
	Domain    string `json:"domain"`
	AmountEur string `json:"amount_eur"`
	// SiteID selects the site the paid domain maps to (required with several sites).
	SiteID string `json:"site_id"`
}

// reserveCheckoutAttempt returns the attempt key for the own domain row:
// a fresh key when there is no open attempt (no session or payment failed),
// the existing key of an attempt that is being created or ended uncertain
// (same Idempotency-Key -> no second Stripe session), and an error when a
// real session is already open.
func (s *Server) reserveCheckoutAttempt(ctx context.Context, restaurantID int, domain string, row ownedDomainRow, amountCents int64) (string, error) {
	expected := eurCentsDecimal(amountCents)
	if row.CheckoutSessionID.Valid {
		v := row.CheckoutSessionID.String
		for _, prefix := range []string{"creating:", "uncertain:"} {
			if strings.HasPrefix(v, prefix) {
				// Same key = same Stripe request: the amount must not change.
				if !row.RegistrationCost.Valid || row.RegistrationCost.String != expected {
					return "", errors.New("el importe no coincide con el intento de pago en curso")
				}
				return checkoutAttemptReusable(strings.TrimPrefix(v, prefix))
			}
		}
		if row.StripePaymentStatus != "failed" {
			// A real session id (stable): reconcile by that id only.
			if err := s.reconcileDomainCheckoutSession(ctx, restaurantID, domain, v); err != nil {
				return "", err
			}
		}
	}
	// "<uuid>@<unix>": the reservation time travels with the key so a retry
	// can tell whether Stripe still holds the Idempotency-Key.
	key := uuid.NewString() + "@" + strconv.FormatInt(time.Now().Unix(), 10)
	// registration_cost = the server-side expected amount of THIS attempt
	// (numeric(10,2) euros; the source stored the cents figure there).
	res, err := s.db.ExecContext(ctx, nativeSQL(`UPDATE restaurant_domains SET stripe_checkout_session_id=?, stripe_payment_status='pending', registration_cost=? WHERE restaurant_id=? AND domain=? AND stripe_payment_status <> 'paid' AND (stripe_checkout_session_id IS NULL OR stripe_payment_status = 'failed')`, `UPDATE restaurant_domains SET stripe_checkout_session_id=?, stripe_payment_status='pending', registration_cost=? WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND stripe_payment_status <> 'paid' AND (stripe_checkout_session_id IS NULL OR stripe_payment_status = 'failed')`), "creating:"+key, expected, restaurantID, domain)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return key, nil
	}
	// Lost the race to a concurrent same-owner request: reuse its key.
	again, err := s.claimOwnedDomain(ctx, restaurantID, domain)
	if err != nil {
		return "", err
	}
	if again.CheckoutSessionID.Valid {
		v := again.CheckoutSessionID.String
		for _, prefix := range []string{"creating:", "uncertain:"} {
			if strings.HasPrefix(v, prefix) {
				if !again.RegistrationCost.Valid || again.RegistrationCost.String != expected {
					return "", errors.New("el importe no coincide con el intento de pago en curso")
				}
				return checkoutAttemptReusable(strings.TrimPrefix(v, prefix))
			}
		}
	}
	return "", errors.New("ya hay un pago en curso para este dominio")
}

// checkoutAttemptWindow is below Stripe's documented minimum Idempotency-Key
// retention (keys are pruned after at least 24 hours), so a reused key
// still deduplicates. Coordination id: checkout_idempotency_v2.
const checkoutAttemptWindow = 23 * time.Hour

// checkoutAttemptReusable returns the stored attempt key when its key is still
// inside the window. An older (or timestamp-less) creating/uncertain attempt
// fails closed: Stripe may already have forgotten the key, so a retry could
// open a SECOND session; there is no stable id to look up, and listing
// sessions to guess is not done. Manual reconciliation is required.
func checkoutAttemptReusable(key string) (string, error) {
	at := strings.LastIndexByte(key, '@')
	if at < 0 {
		return "", errCheckoutNeedsReconcile
	}
	ts, err := strconv.ParseInt(key[at+1:], 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)) > checkoutAttemptWindow || time.Until(time.Unix(ts, 0)) > time.Minute {
		return "", errCheckoutNeedsReconcile
	}
	return key, nil
}

var errCheckoutNeedsReconcile = errors.New("intento de pago con resultado incierto y caducado: requiere conciliación manual con Stripe antes de reintentar")

// reconcileDomainCheckoutSession looks up the row's OWN stable session id.
// expired -> conditional move to failed (only while the row still holds that
// id), so a new attempt may start; anything else, or a lookup error, keeps
// the attempt blocked (fail closed). Never refunds or charges.
func (s *Server) reconcileDomainCheckoutSession(ctx context.Context, restaurantID int, domain, sessionID string) error {
	stripe := integrations.NewStripeClient(s.cfg.StripeSecretKey, s.cfg.StripeWebhookSecret)
	sess, err := stripe.RetrieveCheckoutSession(ctx, sessionID)
	if err != nil || sess.ID != sessionID {
		return errors.New("ya hay un pago en curso para este dominio")
	}
	switch sess.Status {
	case "expired":
		if _, err := s.db.ExecContext(ctx, nativeSQL(`UPDATE restaurant_domains SET stripe_payment_status='failed' WHERE restaurant_id=? AND domain=? AND stripe_checkout_session_id=? AND stripe_payment_status='pending'`, `UPDATE restaurant_domains SET stripe_payment_status='failed' WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND stripe_checkout_session_id=? AND stripe_payment_status='pending'`), restaurantID, domain, sessionID); err != nil {
			return err
		}
		return nil
	case "complete":
		return errors.New("el pago ya se completó y está pendiente de confirmación")
	default:
		return errors.New("ya hay un pago en curso para este dominio")
	}
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
	// domain_price_authority_v1: the amount comes from the client and no
	// server-side EUR price exists for this flow (repair-ignore3 evidence:
	// no frontend caller in repo/backoffice, Cloudflare PriceEUR never
	// populated, the premium price is another flow). Fail closed - checkout
	// only - until the owner fixes the price source. No session is created.
	if !s.domainCheckoutPriceAuthority() {
		httpxWriteError(w, http.StatusServiceUnavailable, "precio de dominio sin fuente autorizada: checkout desactivado hasta decision del propietario")
		return
	}
	// padding_domain_identity_v1: one DNS identity for claim, Stripe metadata
	// and every UPDATE (existing normalizePremiumDomain; invalid -> 400).
	if d := normalizePremiumDomain(req.Domain); d == "" {
		httpxWriteError(w, http.StatusBadRequest, "Invalid domain")
		return
	} else {
		req.Domain = d
	}
	amountCents, err := strconv.ParseInt(req.AmountEur, 10, 64)
	if err != nil || amountCents <= 0 || amountCents > 9999999999 {
		httpxWriteError(w, http.StatusBadRequest, "Invalid amount_eur (cents)")
		return
	}

	// domain_reservation_v1 / checkout_idempotency_v1: claim the domain row and
	// reserve ONE checkout attempt BEFORE talking to Stripe. The attempt key is
	// stored in stripe_checkout_session_id as "creating:<key>" and sent as the
	// Stripe Idempotency-Key, so a retry after a timeout/ambiguous error reuses
	// the same key and gets Stripe's original session instead of a second one.
	// An ambiguous error never clears the reservation ("uncertain:<key>").
	claimed, claimErr := s.claimOwnedDomain(r.Context(), a.ActiveRestaurantID, req.Domain)
	if errors.Is(claimErr, errDomainUnavailable) {
		httpxWriteError(w, http.StatusConflict, "dominio no disponible: "+req.Domain)
		return
	}
	if claimErr != nil {
		httpxWriteError(w, http.StatusInternalServerError, "no se pudo validar el dominio")
		return
	}
	if claimed.StripePaymentStatus == "paid" {
		httpxWriteError(w, http.StatusConflict, "el dominio ya está pagado")
		return
	}
	// Resolve the target site BEFORE payment (site_mapping_selection_v1): a
	// restaurant with several sites must say which one; it travels to the
	// webhook in the signed session metadata.
	siteID, siteErr := s.resolveMappingSite(r.Context(), a.ActiveRestaurantID, req.SiteID)
	if siteErr != nil {
		httpxWriteError(w, http.StatusBadRequest, siteErr.Error())
		return
	}
	key, keyErr := s.reserveCheckoutAttempt(r.Context(), a.ActiveRestaurantID, req.Domain, claimed, amountCents)
	if keyErr != nil {
		log.Printf("[checkout_idempotency_v2] domain=%s restaurant=%d attempt refused: %v", req.Domain, a.ActiveRestaurantID, keyErr)
		httpxWriteError(w, http.StatusConflict, keyErr.Error())
		return
	}

	sub := s.instatic.subdomainFor(a.ActiveRestaurantID)

	stripe := integrations.NewStripeClient(s.cfg.StripeSecretKey, s.cfg.StripeWebhookSecret)
	successURL := "https://" + sub + "/?purchase=success"
	cancelURL := "https://" + sub + "/?purchase=cancelled"

	metadata := map[string]string{"domain": req.Domain, "restaurant_id": strconv.Itoa(a.ActiveRestaurantID)}
	if siteID != "" {
		metadata["site_id"] = siteID
	}
	// Stable, signed (event signature) attempt identity checked by the webhook.
	metadata["attempt"] = key
	metadata["amount_cents"] = strconv.FormatInt(amountCents, 10)
	sess, err := stripe.CreateCheckoutSessionKeyed(r.Context(), "domain-checkout:"+key, amountCents, "eur",
		"Registro de dominio "+req.Domain, successURL, cancelURL, metadata)
	if err != nil {
		// Ambiguous (timeout/5xx may still have created the session): keep the
		// reservation as uncertain; the next request retries with the SAME key.
		if _, cerr := s.db.ExecContext(r.Context(), nativeSQL(`UPDATE restaurant_domains SET stripe_checkout_session_id=? WHERE restaurant_id=? AND domain=? AND stripe_checkout_session_id IN (?, ?)`, `UPDATE restaurant_domains SET stripe_checkout_session_id=? WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND stripe_checkout_session_id IN (?, ?)`), "uncertain:"+key, a.ActiveRestaurantID, req.Domain, "creating:"+key, "uncertain:"+key); cerr != nil {
			log.Printf("[checkout_idempotency_v1] mark uncertain domain=%s restaurant=%d: %v", req.Domain, a.ActiveRestaurantID, cerr)
		}
		httpxWriteError(w, http.StatusBadGateway, "Stripe checkout failed: "+err.Error())
		return
	}

	// Store the real session id on our own reserved attempt. A failed write
	// keeps the reservation; a retry reuses the key and gets the same session.
	if _, uerr := s.db.ExecContext(r.Context(), nativeSQL(`UPDATE restaurant_domains SET stripe_checkout_session_id=?, stripe_payment_status='pending', registration_cost=? WHERE restaurant_id=? AND domain=? AND stripe_checkout_session_id IN (?, ?)`, `UPDATE restaurant_domains SET stripe_checkout_session_id=?, stripe_payment_status='pending', registration_cost=? WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND stripe_checkout_session_id IN (?, ?)`),
		sess.ID, eurCentsDecimal(amountCents), a.ActiveRestaurantID, req.Domain, "creating:"+key, "uncertain:"+key); uerr != nil {
		log.Printf("[checkout_idempotency_v1] record checkout session domain=%s restaurant=%d: %v", req.Domain, a.ActiveRestaurantID, uerr)
	}

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
	// Timestamp tolerance (replay of an old signed event is refused).
	ev, err := stripe.VerifyWebhookSignatureWithTolerance(body, r.Header.Get("Stripe-Signature"), 5*time.Minute)
	if err != nil {
		httpxWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A test-mode event must never act on a live key (and vice versa).
	var mode struct {
		Livemode bool `json:"livemode"`
	}
	if json.Unmarshal(body, &mode) != nil || mode.Livemode != stripe.Live() {
		log.Printf("[stripe_event_processing_v2] event %s livemode=%v does not match key mode: refused", ev.ID, mode.Livemode)
		httpxWriteError(w, http.StatusBadRequest, "livemode mismatch")
		return
	}

	claim, state, claimErr := s.claimStripeEvent(r.Context(), ev.ID, ev.Type, string(body))
	if claimErr != nil {
		log.Printf("[stripe_event_processing_v2] claim %s: %v", ev.ID, claimErr)
		httpxWriteError(w, http.StatusServiceUnavailable, "event claim failed")
		return
	}
	switch state {
	case stripeEventDone:
		writeJSON(w, map[string]any{"success": true, "duplicate": true})
		return
	case stripeEventBusy:
		httpxWriteError(w, http.StatusServiceUnavailable, "event in progress")
		return
	}
	defer claim.close()

	outcome, procErr := stripeOutcomeDone, error(nil)
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		outcome, procErr = s.handleStripeCheckoutCompleted(r.Context(), claim, stripe, ev)
	case "checkout.session.expired", "checkout.session.async_payment_failed":
		outcome, procErr = s.handleStripeCheckoutClosed(r.Context(), claim, ev)
	}
	if procErr != nil {
		log.Printf("[stripe_event_processing_v2] event %s (%s) not processed, Stripe will retry: %v", ev.ID, ev.Type, procErr)
		httpxWriteError(w, http.StatusServiceUnavailable, "event processing failed")
		return
	}
	if outcome == stripeOutcomeReview {
		// Durable review state: processed_at stays NULL; Stripe is told 200.
		log.Printf("[stripe_reconcile_v1] event %s (%s) left unprocessed for manual review", ev.ID, ev.Type)
		writeJSON(w, map[string]any{"success": true, "review": true})
		return
	}
	if err := claim.markProcessed(r.Context()); err != nil {
		log.Printf("[stripe_event_processing_v2] mark %s processed: %v", ev.ID, err)
		httpxWriteError(w, http.StatusServiceUnavailable, "event mark failed")
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

// handleStripeCheckoutCompleted marks a domain paid, invoices once and
// provisions it.
//
// Trust: the event is signature-verified, then the session is RE-READ from
// Stripe by id (provider object, not the event payload). Accepted only when
// paid, currency eur, metadata domain/restaurant/attempt match the OWN row's
// recorded attempt (or its stored session id), and amount_total equals the
// server-side expected amount persisted on that row at reservation
// (registration_cost) - metadata alone is never the price authority.
// Anything else: REVIEW (left unprocessed), nothing written.
//
// DB effects run in ONE tx on the pinned claim connection (fencing) under
// the domain row lock. Provisioning runs after commit; only "mapped" or
// "skipped_no_site" (source: no site, no mapping) completes; DNS/provider
// errors are retryable (Ensure* idempotent), ownership/site conflicts REVIEW.
func (s *Server) handleStripeCheckoutCompleted(ctx context.Context, claim *stripeEventClaim, stripe *integrations.StripeClient, ev *integrations.Event) (stripeOutcome, error) {
	var evObj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ev.Data.Object, &evObj); err != nil || evObj.ID == "" {
		return stripeOutcomeReview, nil
	}
	sess, err := stripe.RetrieveCheckoutSession(ctx, evObj.ID)
	if err != nil {
		return stripeOutcomeDone, err // transient provider read: retry
	}
	if sess.ID != evObj.ID {
		return stripeOutcomeReview, nil
	}
	if sess.PaymentStatus != "paid" {
		// Delayed methods: async_payment_succeeded will follow. Nothing to do.
		log.Printf("[stripe_event_processing_v2] checkout %s payment_status=%q: not marked paid", sess.ID, sess.PaymentStatus)
		return stripeOutcomeDone, nil
	}
	domain := sess.Metadata["domain"]
	restaurantID, _ := strconv.Atoi(sess.Metadata["restaurant_id"])
	attempt := sess.Metadata["attempt"]
	if domain == "" || restaurantID == 0 || !strings.EqualFold(sess.Currency, "eur") {
		return stripeOutcomeReview, nil
	}
	if mc := sess.Metadata["amount_cents"]; mc != "" {
		if want, err := strconv.ParseInt(mc, 10, 64); err != nil || want != sess.AmountTotal {
			return stripeOutcomeReview, nil
		}
	}

	tx, err := claim.conn.BeginTx(ctx, nil)
	if err != nil {
		return stripeOutcomeDone, err
	}
	defer tx.Rollback()
	var owner int
	var rowSession, rowCost sql.NullString
	var payStatus string
	err = tx.QueryRowContext(ctx, nativeSQL(
		`SELECT restaurant_id, stripe_checkout_session_id, stripe_payment_status, registration_cost FROM restaurant_domains WHERE domain = ? FOR UPDATE`,
		`SELECT restaurant_id, stripe_checkout_session_id, CAST(stripe_payment_status AS text), CAST(registration_cost AS text) FROM restaurant_domains WHERE app.norm_ascii(domain) = app.norm_ascii(?) FOR UPDATE`),
		domain).Scan(&owner, &rowSession, &payStatus, &rowCost)
	if errors.Is(err, sql.ErrNoRows) {
		// A paid session always comes from a reserved row; none = review.
		return stripeOutcomeReview, nil
	}
	if err != nil {
		return stripeOutcomeDone, err
	}
	if owner != restaurantID {
		log.Printf("[domain_owner_isolation_v1] stripe checkout %s domain=%s restaurant=%d foreign owner: review", sess.ID, domain, restaurantID)
		return stripeOutcomeReview, nil
	}
	current := rowSession.String
	if payStatus == "paid" {
		if current != sess.ID {
			log.Printf("[stripe_reconcile_v1] checkout %s domain=%s: already paid by another session, refund review", sess.ID, domain)
			return stripeOutcomeReview, nil
		}
		// Same session already applied (retry after partial run): continue to
		// the idempotent invoice + provisioning steps.
	} else {
		isCurrent := current == sess.ID || (attempt != "" && (current == "creating:"+attempt || current == "uncertain:"+attempt))
		if !isCurrent {
			log.Printf("[stripe_reconcile_v1] checkout %s domain=%s is not the recorded attempt (or pre-v2 metadata): review", sess.ID, domain)
			return stripeOutcomeReview, nil
		}
		if !rowCost.Valid || rowCost.String != eurCentsDecimal(sess.AmountTotal) {
			log.Printf("[stripe_reconcile_v1] checkout %s domain=%s amount differs from the server-recorded expected amount: review", sess.ID, domain)
			return stripeOutcomeReview, nil
		}
		if _, err := tx.ExecContext(ctx, nativeSQL(`UPDATE restaurant_domains SET stripe_checkout_session_id = ?, stripe_payment_status = 'paid', registration_status = CASE WHEN registration_status IN ('pending','failed') THEN 'paid' ELSE registration_status END WHERE restaurant_id = ? AND domain = ?`, `UPDATE restaurant_domains SET stripe_checkout_session_id = ?, stripe_payment_status = 'paid', registration_status = CASE WHEN registration_status IN ('pending','failed') THEN 'paid' ELSE registration_status END WHERE restaurant_id = ? AND app.norm_ascii(domain) = app.norm_ascii(?)`), sess.ID, restaurantID, domain); err != nil {
			return stripeOutcomeDone, err
		}
	}
	// Recurring invoice for the annual fee, once per domain (NOT EXISTS under
	// the row lock). Amount = the verified paid amount.
	if _, err := tx.ExecContext(ctx, nativeSQL(`
		INSERT INTO recurring_invoices (restaurant_id, customer_name, customer_email, amount, currency, frequency, start_date, feature_key, concept)
		SELECT ?, (SELECT name FROM restaurants WHERE id=?), '', ?, 'EUR', 'yearly', CURDATE(), 'website_domain', ?
		FROM DUAL WHERE NOT EXISTS (SELECT 1 FROM recurring_invoices WHERE restaurant_id = ? AND feature_key = 'website_domain' AND concept = ?)
	`, `
		INSERT INTO recurring_invoices (restaurant_id, customer_name, customer_email, amount, currency, frequency, start_date, feature_key, concept)
		SELECT CAST(? AS integer), (SELECT name FROM restaurants WHERE id=?), '', CAST(CAST(? AS text) AS numeric), 'EUR', 'yearly', CAST(statement_timestamp() AT TIME ZONE 'UTC' AS date), 'website_domain', CAST(? AS text)
		WHERE NOT EXISTS (SELECT 1 FROM recurring_invoices WHERE restaurant_id = ? AND feature_key = 'website_domain' AND concept = ?)
	`), restaurantID, restaurantID, eurCentsDecimal(sess.AmountTotal), domain, restaurantID, domain); err != nil {
		return stripeOutcomeDone, err
	}
	if err := tx.Commit(); err != nil {
		return stripeOutcomeDone, err
	}
	switch mapping := s.provisionDomainSite(ctx, restaurantID, domain, sess.Metadata["site_id"]); mapping {
	case "mapped", "skipped_no_site":
		return stripeOutcomeDone, nil
	case "skipped_dns", "skipped_error":
		// Row stays 'paid' (not active): durable pending; Stripe retries.
		return stripeOutcomeDone, errors.New("provisioning not completed: " + mapping)
	default:
		log.Printf("[stripe_reconcile_v1] checkout %s domain=%s provisioning=%s: review", sess.ID, domain, mapping)
		return stripeOutcomeReview, nil
	}
}

// eurCentsDecimal renders integer cents as the numeric(10,2) text form.
func eurCentsDecimal(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return sign + strconv.FormatInt(c/100, 10) + "." + fmt.Sprintf("%02d", c%100)
}

// handleStripeCheckoutClosed handles checkout.session.expired and
// async_payment_failed: the OWN row moves pending -> failed only while it
// still holds THIS session (its id, or this attempt's creating/uncertain
// marker), so an old session can never clear a newer attempt. Fenced on
// the claim connection. 0 rows matched = nothing of ours to close (done).
func (s *Server) handleStripeCheckoutClosed(ctx context.Context, claim *stripeEventClaim, ev *integrations.Event) (stripeOutcome, error) {
	var obj stripeDomainCheckoutObject
	if err := json.Unmarshal(ev.Data.Object, &obj); err != nil || obj.ID == "" {
		return stripeOutcomeReview, nil
	}
	domain := obj.Metadata["domain"]
	restaurantID, _ := strconv.Atoi(obj.Metadata["restaurant_id"])
	if domain == "" || restaurantID == 0 {
		return stripeOutcomeDone, nil
	}
	markers := []any{obj.ID, obj.ID, obj.ID}
	if a := obj.Metadata["attempt"]; a != "" {
		markers = []any{obj.ID, "creating:" + a, "uncertain:" + a}
	}
	args := append([]any{obj.ID, restaurantID, domain}, markers...)
	_, err := claim.conn.ExecContext(ctx, nativeSQL(`UPDATE restaurant_domains SET stripe_checkout_session_id = ?, stripe_payment_status = 'failed' WHERE restaurant_id = ? AND domain = ? AND stripe_payment_status = 'pending' AND stripe_checkout_session_id IN (?, ?, ?)`, `UPDATE restaurant_domains SET stripe_checkout_session_id = ?, stripe_payment_status = 'failed' WHERE restaurant_id = ? AND app.norm_ascii(domain) = app.norm_ascii(?) AND stripe_payment_status = 'pending' AND stripe_checkout_session_id IN (?, ?, ?)`), args...)
	return stripeOutcomeDone, err
}

// stripeDomainCheckoutObject is the signed checkout.session object.
type stripeDomainCheckoutObject struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata"`
}

// domainCheckoutPriceAuthority reports whether a server-side domain price
// source exists. None is grounded yet, so it is false: checkout refuses.
func (s *Server) domainCheckoutPriceAuthority() bool { return false }
