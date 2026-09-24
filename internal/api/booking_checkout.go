package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/integrations"
)

// =============================================================================
// Stripe checkout of a prereserva adelanto.
//
//   POST /api/bookings/front/checkout      validate the booking exactly like a
//        direct insert, store it as a pending checkout, open Stripe Checkout
//        (or the demo checkout) and return its URL. Nothing is inserted yet.
//   GET  /api/bookings/checkout/demo/{id}  demo checkout page (demo_mode).
//   POST /api/bookings/checkout/{id}/complete  verify the payment and insert
//        the prereserva (idempotent), then receipt + email + WhatsApp.
//   POST /api/stripe/prereserva-webhook    checkout.session.completed backstop.
//
// Coordination id: stripe_prereserva_adelanto_v1
// =============================================================================

// checkoutTTL: Stripe Checkout needs >= 30 min; a demo/pending checkout older
// than this can no longer be paid.
const checkoutTTL = 31 * time.Minute

const (
	checkoutStatusPending   = "pending"
	checkoutStatusPaid      = "paid"
	checkoutStatusCompleted = "completed"
	checkoutStatusFailed    = "failed"
	checkoutStatusCancelled = "cancelled"
)

func logStripeFlow(event string, restaurantID int, publicID string, detail string) {
	log.Printf("[stripe_prereserva_adelanto_v1] %s restaurant=%d checkout=%s %s", event, restaurantID, publicID, detail)
}

func newCheckoutPublicID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "pc_" + hex.EncodeToString(b)
}

// specialDateRequiresStripe: prereserva + adelanto + stripe is the only method.
func specialDateRequiresStripe(settings *specialDateSettings) bool {
	if settings == nil || !settings.PrereservaEnabled || !settings.RequiresAdelanto {
		return false
	}
	return len(settings.AdelantoPaymentMethods) == 1 && settings.AdelantoPaymentMethods[0] == adelantoMethodStripe
}

type checkoutRow struct {
	ID                int64
	RestaurantID      int
	PublicID          string
	Provider          string
	ProviderSessionID string
	Status            string
	AmountCents       int64
	Currency          string
	Form              url.Values
	BookingID         int64
	PaymentIntentID   string
	ReceiptURL        string
	ErrorMessage      string
	PaidAt            sql.NullTime
	DestinationHash   string
	ExpiresAt         sql.NullTime
}

func (c *checkoutRow) expired() bool {
	return c.ExpiresAt.Valid && time.Now().After(c.ExpiresAt.Time)
}

func (s *Server) loadCheckout(ctx context.Context, restaurantID int, publicID string) (*checkoutRow, error) {
	var (
		row                                checkoutRow
		sessionID, intent, receipt, errMsg sql.NullString
		bookingID                          sql.NullInt64
		formJSON                           string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, restaurant_id, public_id, provider, provider_session_id, status, amount_cents, currency,
		       form_json, booking_id, payment_intent_id, receipt_url, error_message, paid_at,
		       COALESCE(destination_hash, ''), expires_at
		FROM booking_checkouts WHERE public_id = ? AND restaurant_id = ?`, publicID, restaurantID).
		Scan(&row.ID, &row.RestaurantID, &row.PublicID, &row.Provider, &sessionID, &row.Status, &row.AmountCents, &row.Currency,
			&formJSON, &bookingID, &intent, &receipt, &errMsg, &row.PaidAt, &row.DestinationHash, &row.ExpiresAt)
	if err != nil {
		return nil, err
	}
	row.ProviderSessionID, row.PaymentIntentID, row.ReceiptURL, row.ErrorMessage = sessionID.String, intent.String, receipt.String, errMsg.String
	row.BookingID = bookingID.Int64
	row.Form = url.Values{}
	_ = json.Unmarshal([]byte(formJSON), &row.Form)
	return &row, nil
}

// adelantoTotalCents sums count × adelanto of every snapshot line.
func adelantoTotalCents(snap *specialBookingSnapshot) int64 {
	if snap == nil {
		return 0
	}
	total := 0.0
	for _, m := range snap.Menus {
		total += m.AdelantoPerUnit * float64(m.Count)
	}
	return int64(math.Round(total * 100))
}

func (s *Server) handleBookingCheckoutCreate(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	if !s.checkRateLimit(httpx.ClientIP(r), restaurantID) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "message": "Demasiadas solicitudes. Inténtalo de nuevo en un momento."})
		return
	}
	if err := parseLegacyForm(r, 5<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Faltan campos requeridos"})
		return
	}
	if strings.TrimSpace(r.FormValue("website_url")) != "" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"success": false, "message": "Spam detected."})
		return
	}

	pb, err := s.prepareFrontBooking(r, restaurantID)
	if err != nil {
		var fe *frontBookingError
		if errors.As(err, &fe) {
			httpx.WriteJSON(w, fe.Status, fe.Body)
			return
		}
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if !pb.requiresStripe {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Esta reserva no requiere pago online", "error_code": "STRIPE_NOT_REQUIRED"})
		return
	}
	amount := adelantoTotalCents(pb.specialSnapshot)
	if amount <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "El adelanto de esta prereserva es 0 €; revisa la configuración de la fecha especial"})
		return
	}
	// Coordination id: stripe_connect_multitenant_v1 - the platform account
	// charges; funds go to the restaurant's connected account.
	connect, ready := s.connectReady(r.Context(), restaurantID)
	if !ready {
		httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": "El pago online no está disponible en este momento", "error_code": "STRIPE_NOT_CONFIGURED"})
		return
	}
	demo := connect.Demo
	const currency = "eur"
	fee := int64(math.Round(float64(amount) * s.cfg.StripePlatformFeePercent / 100))

	publicID := newCheckoutPublicID()
	formJSON, _ := json.Marshal(r.Form)
	provider := "stripe"
	if demo {
		provider = "demo"
	}
	expires := time.Now().Add(checkoutTTL)
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO booking_checkouts (restaurant_id, public_id, provider, status, amount_cents, currency, form_json, destination_hash, application_fee_cents, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, restaurantID, publicID, provider, checkoutStatusPending, amount, currency, string(formJSON),
		s.connectAccountHash(connect.AccountID), fee, expires); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "No se pudo iniciar el pago"})
		return
	}

	baseURL := s.checkoutReturnBaseURL(r, restaurantID)
	successURL := baseURL + "/reservas/pago-completado?checkout=" + publicID
	cancelURL := baseURL + "/reservas?date=" + url.QueryEscape(pb.params.ReservationDate) + "&pago=cancelado"

	var redirectURL string
	if demo {
		redirectURL = "/api/bookings/checkout/demo/" + publicID
	} else {
		cli, err := s.platformStripe()
		if err != nil {
			httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": "El pago online no está disponible en este momento", "error_code": "STRIPE_NOT_CONFIGURED"})
			return
		}
		title := "Adelanto prereserva"
		if pb.specialSnapshot != nil && pb.specialSnapshot.Title != "" {
			title += " · " + pb.specialSnapshot.Title
		}
		title += " · " + pb.params.ReservationDate
		sess, err := cli.CreateDestinationCheckout(r.Context(), integrations.DestinationCheckout{
			AmountCents: amount, FeeCents: fee, Currency: currency, Description: title,
			Destination: connect.AccountID, SuccessURL: successURL, CancelURL: cancelURL,
			CustomerEmail: pb.params.ContactEmail, ExpiresAtUnix: expires.Unix(), IdempotencyKey: publicID,
			Metadata: map[string]string{"checkout_public_id": publicID, "restaurant_id": fmt.Sprint(restaurantID), "coordination_id": "stripe_connect_multitenant_v1"},
		})
		if err != nil || sess.URL == "" {
			logStripeFlow("session_create_failed", restaurantID, publicID, fmt.Sprint(err))
			_, _ = s.db.ExecContext(r.Context(), `UPDATE booking_checkouts SET status = ?, error_message = ? WHERE public_id = ?`, checkoutStatusFailed, "stripe session create failed", publicID)
			httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": "No se pudo abrir el pago con tarjeta. Inténtalo de nuevo."})
			return
		}
		_, _ = s.db.ExecContext(r.Context(), `UPDATE booking_checkouts SET provider_session_id = ? WHERE public_id = ?`, sess.ID, publicID)
		redirectURL = sess.URL
	}
	logCheckpoint(r, "prereserva_checkout_created", "checkout", publicID, "provider", provider, "amount_cents", fmt.Sprint(amount))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"checkout_id":  publicID,
		"checkout_url": redirectURL,
		"amount":       float64(amount) / 100,
		"currency":     currency,
		"demo":         demo,
	})
}

// handleBookingCheckoutDemoPage renders the demo "Stripe" page. Paying marks
// the checkout paid and goes to the same success page Stripe would.
func (s *Server) handleBookingCheckoutDemoPage(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.checkScopedRateLimit("checkout_view", httpx.ClientIP(r), restaurantID, 30) {
		http.Error(w, "Demasiadas solicitudes", http.StatusTooManyRequests)
		return
	}
	row, err := s.loadCheckout(r.Context(), restaurantID, chi.URLParam(r, "id"))
	if err != nil || row.Provider != "demo" {
		http.NotFound(w, r)
		return
	}
	// S1: a demo checkout only pays while the restaurant is still in demo mode
	// and before it expires; otherwise it is dead.
	if connect, _ := s.connectReady(r.Context(), restaurantID); connect == nil || !connect.Demo || row.expired() {
		http.Error(w, "Este pago de demostración ya no es válido", http.StatusGone)
		return
	}
	baseURL := s.checkoutReturnBaseURL(r, restaurantID)
	success := baseURL + "/reservas/pago-completado?checkout=" + row.PublicID
	cancel := baseURL + "/reservas?date=" + url.QueryEscape(row.Form.Get("reservation_date")) + "&pago=cancelado"
	if r.Method == http.MethodPost {
		if r.FormValue("action") == "pay" {
			_, _ = s.db.ExecContext(r.Context(), `
				UPDATE booking_checkouts SET status = ?, paid_at = NOW(), payment_intent_id = ?
				WHERE public_id = ? AND status = ?`, checkoutStatusPaid, "demo_"+row.PublicID[3:15], row.PublicID, checkoutStatusPending)
			logCheckpoint(r, "prereserva_checkout_demo_paid", "checkout", row.PublicID)
			http.Redirect(w, r, success, http.StatusSeeOther)
			return
		}
		_, _ = s.db.ExecContext(r.Context(), `UPDATE booking_checkouts SET status = ? WHERE public_id = ? AND status = ?`, checkoutStatusCancelled, row.PublicID, checkoutStatusPending)
		http.Redirect(w, r, cancel, http.StatusSeeOther)
		return
	}
	amount := formatMoney(float64(row.AmountCents)/100, row.Currency)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Pago demo</title><style>body{font-family:system-ui,sans-serif;background:#f6f9fc;margin:0;display:grid;place-items:center;min-height:100vh}
.card{background:#fff;border-radius:14px;box-shadow:0 10px 30px rgba(0,0,0,.08);padding:28px;max-width:380px;width:calc(100%% - 40px)}
.badge{display:inline-block;background:#fff4d6;color:#8a5a00;border-radius:999px;padding:4px 10px;font-size:12px;font-weight:600}
h1{font-size:20px;margin:14px 0 4px}.amount{font-size:32px;font-weight:700;margin:10px 0 18px}
button{width:100%%;border:0;border-radius:10px;padding:13px;font-size:16px;cursor:pointer;margin-top:8px}
.pay{background:#635bff;color:#fff}.cancel{background:#eef0f4;color:#333}</style></head>
<body><div class="card" data-testid="demo-checkout-card"><span class="badge" data-testid="demo-checkout-badge">MODO DEMO · no se cobra nada</span>
<h1 data-testid="demo-checkout-title">Adelanto de la prereserva</h1><p data-testid="demo-checkout-date">%s · %s personas</p>
<div class="amount" data-testid="demo-checkout-amount">%s</div>
<form method="post"><button class="pay" name="action" value="pay" data-testid="demo-checkout-pay">Pagar %s</button>
<button class="cancel" name="action" value="cancel" data-testid="demo-checkout-cancel">Cancelar</button></form></div></body></html>`,
		html.EscapeString(row.Form.Get("reservation_date")), html.EscapeString(row.Form.Get("party_size")), html.EscapeString(amount), html.EscapeString(amount))
}

// completeCheckout confirms the payment and inserts the prereserva exactly
// once. Safe to call from the success page and the webhook concurrently.
func (s *Server) completeCheckout(r *http.Request, restaurantID int, publicID string) (*checkoutRow, map[string]any, error) {
	ctx := r.Context()
	row, err := s.loadCheckout(ctx, restaurantID, publicID)
	if err != nil {
		return nil, nil, errors.New("Pago no encontrado")
	}
	if row.Status == checkoutStatusCompleted {
		return row, nil, nil
	}
	if row.Status == checkoutStatusCancelled || row.Status == checkoutStatusFailed {
		return row, nil, fmt.Errorf("El pago no se completó")
	}

	// 1) Payment verified (demo pays in its own page; Stripe is asked).
	if row.Status == checkoutStatusPending {
		if row.Provider != "stripe" || row.ProviderSessionID == "" {
			return row, nil, errors.New("El pago todavía no se ha completado")
		}
		cli, err := s.platformStripe()
		if err != nil {
			return row, nil, errors.New("Stripe no configurado")
		}
		sess, err := cli.RetrieveConnectCheckout(ctx, row.ProviderSessionID)
		if err != nil {
			logStripeFlow("session_retrieve_failed", restaurantID, publicID, err.Error())
			return row, nil, errors.New("No se pudo verificar el pago con Stripe")
		}
		// S5: paid, same amount, same checkout, and the money went to THIS
		// restaurant's connected account.
		if sess.PaymentStatus != "paid" || sess.AmountTotal != row.AmountCents ||
			sess.Metadata["checkout_public_id"] != publicID || sess.Metadata["restaurant_id"] != fmt.Sprint(restaurantID) ||
			row.DestinationHash == "" || s.connectAccountHash(sess.PaymentIntent.TransferData.Destination) != row.DestinationHash {
			logStripeFlow("session_mismatch", restaurantID, publicID, sess.PaymentStatus)
			return row, nil, errors.New("El pago todavía no se ha completado")
		}
		sessPaymentIntent := sess.PaymentIntent.ID
		_, _ = s.db.ExecContext(ctx, `UPDATE booking_checkouts SET status = ?, paid_at = NOW(), payment_intent_id = ? WHERE id = ? AND status = ?`,
			checkoutStatusPaid, sessPaymentIntent, row.ID, checkoutStatusPending)
	}

	// 2) Claim the insert: only one caller moves paid -> completed.
	res, err := s.db.ExecContext(ctx, `UPDATE booking_checkouts SET status = ?, completed_at = NOW() WHERE id = ? AND status = ?`,
		checkoutStatusCompleted, row.ID, checkoutStatusPaid)
	if err != nil {
		return row, nil, errors.New("No se pudo registrar la prereserva")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fresh, _ := s.loadCheckout(ctx, restaurantID, publicID)
		return fresh, nil, nil // another caller completed it
	}

	// 3) Replay the stored form: same validation, then insert + notify.
	replay := r.Clone(ctx)
	replay.Form = row.Form
	replay.PostForm = row.Form
	replay.MultipartForm = nil
	pb, err := s.prepareFrontBooking(replay, restaurantID)
	if err != nil {
		// Paid but no longer bookable (e.g. sold out meanwhile): keep the
		// payment on record for a manual refund.
		logStripeFlow("paid_but_invalid", restaurantID, publicID, err.Error())
		_, _ = s.db.ExecContext(ctx, `UPDATE booking_checkouts SET status = ?, error_message = ? WHERE id = ?`, checkoutStatusFailed, truncate(err.Error(), 500), row.ID)
		if fresh, ferr := s.loadCheckout(ctx, restaurantID, publicID); ferr == nil {
			row = fresh
		}
		return row, nil, errors.New("El pago se recibió pero la prereserva no se pudo completar. El restaurante contactará contigo para el reembolso.")
	}
	markSnapshotPaidByStripe(pb, float64(row.AmountCents)/100)

	paidAt := time.Now()
	if row.PaidAt.Valid {
		paidAt = row.PaidAt.Time
	}
	fresh, _ := s.loadCheckout(ctx, restaurantID, publicID)
	pdfBytes, perr := s.buildCheckoutReceipt(ctx, restaurantID, fresh, pb, paidAt, 0)
	var receipt *bookingReceipt
	if perr == nil {
		receipt = &bookingReceipt{Filename: "comprobante-prereserva-" + publicID + ".pdf", PDF: pdfBytes}
	}

	bookingID, status, resp := s.commitFrontBooking(replay, pb, receipt)
	if bookingID > 0 && status != http.StatusOK {
		// Stored and paid; only a notification failed. Keep it completed.
		logStripeFlow("notification_failed", restaurantID, publicID, anyToString(resp["message"]))
		_, _ = s.db.ExecContext(ctx, `UPDATE booking_checkouts SET error_message = ? WHERE id = ?`, truncate(anyToString(resp["message"]), 500), row.ID)
	}
	if bookingID <= 0 {
		_, _ = s.db.ExecContext(ctx, `UPDATE booking_checkouts SET status = ?, error_message = ? WHERE id = ?`, checkoutStatusFailed, truncate(anyToString(resp["message"]), 500), row.ID)
		if fresh, ferr := s.loadCheckout(ctx, restaurantID, publicID); ferr == nil {
			row = fresh
		}
		return row, resp, fmt.Errorf("%s", anyToString(resp["message"]))
	}
	// Final receipt carries the booking number; upload that one.
	if pdfBytes, perr = s.buildCheckoutReceipt(ctx, restaurantID, fresh, pb, paidAt, bookingID); perr == nil {
		receipt = s.storeReceipt(ctx, restaurantID, publicID, pdfBytes)
	}
	receiptURL := ""
	if receipt != nil {
		receiptURL = receipt.URL
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE booking_checkouts SET booking_id = ?, receipt_url = ? WHERE id = ?`, bookingID, nullIfEmpty(receiptURL), row.ID)
	logStripeFlow("completed", restaurantID, publicID, fmt.Sprintf("booking=%d status=%d", bookingID, status))
	out, _ := s.loadCheckout(ctx, restaurantID, publicID)
	return out, resp, nil
}

// markSnapshotPaidByStripe records the adelanto as fully paid by stripe.
func markSnapshotPaidByStripe(pb *preparedFrontBooking, amount float64) {
	if pb.specialSnapshot == nil {
		return
	}
	pb.specialSnapshot.AdelantosPaid = []specialBookingAdelantoPaid{{Method: adelantoMethodStripe, Amount: amount}}
	if b, err := json.Marshal(pb.specialSnapshot); err == nil {
		pb.params.SpecialJSON = string(b)
	}
}

func (s *Server) buildCheckoutReceipt(ctx context.Context, restaurantID int, row *checkoutRow, pb *preparedFrontBooking, paidAt time.Time, bookingID int64) ([]byte, error) {
	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	in := receiptInput{
		Reference: row.PublicID, PaymentRef: row.PaymentIntentID, PaidAt: paidAt, Demo: row.Provider == "demo",
		BrandName: firstNonEmpty(branding.BrandName, "Restaurante"), Address: branding.Address, Phone: branding.Phone, Email: branding.Email,
		Customer: pb.params.CustomerName, CustomerMail: pb.params.ContactEmail, CustomerTel: maskPhone(pb.phoneE164),
		Date: pb.params.ReservationDate, Time: strings.TrimSuffix(anyToString(pb.params.ReservationTime), ":00"), PartySize: pb.params.PartySize,
		BookingID: bookingID, Total: float64(row.AmountCents) / 100, Currency: row.Currency,
	}
	if pb.specialSnapshot != nil {
		in.DateTitle = pb.specialSnapshot.Title
		for _, m := range pb.specialSnapshot.Menus {
			if m.AdelantoPerUnit > 0 && m.Count > 0 {
				in.Lines = append(in.Lines, receiptLine{Label: m.Label, Count: m.Count, Amount: m.AdelantoPerUnit * float64(m.Count)})
			}
		}
	}
	return buildReceiptPDF(in)
}

// checkoutPublicView is what the success page shows.
func (s *Server) checkoutPublicView(ctx context.Context, restaurantID int, row *checkoutRow) map[string]any {
	view := map[string]any{
		"checkout_id": row.PublicID,
		"status":      row.Status,
		"amount":      float64(row.AmountCents) / 100,
		"currency":    row.Currency,
		"demo":        row.Provider == "demo",
		"payment_ref": row.PaymentIntentID,
		"receipt_url": row.ReceiptURL,
		"booking_id":  row.BookingID,
		"error":       row.ErrorMessage,
		"reservation": map[string]any{
			"date":          row.Form.Get("reservation_date"),
			"time":          row.Form.Get("reservation_time"),
			"party_size":    row.Form.Get("party_size"),
			"customer_name": row.Form.Get("customer_name"),
			"contact_email": row.Form.Get("contact_email"),
		},
	}
	if row.PaidAt.Valid {
		view["paid_at"] = row.PaidAt.Time.Format(time.RFC3339)
	}
	if row.BookingID > 0 {
		var specialRaw sql.NullString
		if s.db.QueryRowContext(ctx, `SELECT special_json FROM bookings WHERE id = ? AND restaurant_id = ?`, row.BookingID, restaurantID).Scan(&specialRaw) == nil {
			view["special"] = s.buildSpecialBookingResponse(ctx, restaurantID, true, true, specialRaw.String)
		}
	}
	return view
}

func (s *Server) handleBookingCheckoutComplete(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}
	if !s.checkScopedRateLimit("checkout_view", httpx.ClientIP(r), restaurantID, 30) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "message": "Demasiadas solicitudes. Inténtalo de nuevo en un momento."})
		return
	}
	row, resp, err := s.completeCheckout(r, restaurantID, chi.URLParam(r, "id"))
	if row == nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Pago no encontrado"})
		return
	}
	body := map[string]any{"success": err == nil && row.Status == checkoutStatusCompleted, "checkout": s.checkoutPublicView(r.Context(), restaurantID, row)}
	if err != nil {
		body["message"] = err.Error()
	}
	if resp != nil {
		for _, k := range []string{"email_sent", "whatsapp_sent", "whatsapp_warning"} {
			if v, ok := resp[k]; ok {
				body[k] = v
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// handleStripeConnectWebhook is the ONE platform webhook for every tenant
// (Connect endpoint). Signature is checked with STRIPE_CONNECT_WEBHOOK_SECRET
// and a 5-minute replay window; events are processed once (stripe_events).
//   checkout.session.completed -> complete the prereserva of that checkout
//   account.updated            -> refresh the tenant's onboarding status
// Coordination id: stripe_connect_multitenant_v1
func (s *Server) handleStripeConnectWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.StripeConnectWebhookSecret == "" || s.cfg.StripePlatformSecretKey == "" {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "message": "webhook no configurado"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false})
		return
	}
	cli := integrations.NewStripeClient(s.cfg.StripePlatformSecretKey, s.cfg.StripeConnectWebhookSecret)
	ev, err := cli.VerifyWebhookSignatureWithTolerance(body, r.Header.Get("Stripe-Signature"), 5*time.Minute)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "firma inválida"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT IGNORE INTO stripe_events (event_id, type, payload, processed_at) VALUES (?, ?, ?, NOW())`, ev.ID, ev.Type, "{}")
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "duplicate": true})
		return
	}
	switch ev.Type {
	case "checkout.session.completed":
		var obj struct {
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.Unmarshal(ev.Data.Object, &obj)
		var restaurantID int
		_, _ = fmt.Sscan(obj.Metadata["restaurant_id"], &restaurantID)
		if publicID := obj.Metadata["checkout_public_id"]; restaurantID > 0 && publicID != "" {
			ctx := withRestaurantID(r.Context(), restaurantID)
			// completeCheckout re-reads the session from Stripe and checks the
			// destination account, so forged metadata cannot complete anything.
			if _, _, err := s.completeCheckout(r.WithContext(ctx), restaurantID, publicID); err != nil {
				logStripeFlow("webhook_complete_error", restaurantID, publicID, err.Error())
			}
		}
	case "account.updated":
		var obj struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(ev.Data.Object, &obj)
		var restaurantID int
		if obj.ID != "" && s.db.QueryRowContext(r.Context(), `SELECT restaurant_id FROM restaurant_stripe_connect WHERE account_hash = ?`, s.connectAccountHash(obj.ID)).Scan(&restaurantID) == nil {
			if row, err := s.loadConnectAccount(r.Context(), restaurantID); err == nil && row != nil && row.AccountID == obj.ID {
				if _, err := s.refreshConnectAccount(r.Context(), row); err != nil {
					log.Printf("[stripe_connect_multitenant_v1] restaurant=%d account.updated refresh: %v", restaurantID, err)
				} else {
					log.Printf("[stripe_connect_multitenant_v1] restaurant=%d account status=%s", restaurantID, row.Status)
				}
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// maskPhone keeps the last 3 digits: the receipt is a public CDN file.
func maskPhone(e164 string) string {
	digits := strings.TrimPrefix(e164, "+")
	if len(digits) <= 3 {
		return "***"
	}
	return "+" + strings.Repeat("*", len(digits)-3) + digits[len(digits)-3:]
}

// checkoutReturnBaseURL is where Stripe/demo send the payer back. The site that
// started the checkout wins when its Origin is explicitly allow-listed
// (CORS_ALLOW_ORIGINS, never "*"), so dev/preview sites and tenants whose
// public website is not live yet keep the payer on the same site. Otherwise the
// restaurant's public website is used.
// Coordination id: stripe_connect_multitenant_v1.return_url
func (s *Server) checkoutReturnBaseURL(r *http.Request, restaurantID int) string {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin != "" && s.resolveAllowedOrigin(origin) == origin {
		log.Printf("stripe_connect_multitenant_v1.return_url restaurant=%d source=origin base=%s", restaurantID, origin)
		return origin
	}
	return strings.TrimRight(resolveRestaurantPublicBaseURL(r.Context(), s, restaurantID), "/")
}
