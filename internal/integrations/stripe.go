package integrations

// Stripe client (minimal REST). Uses a restricted key via basic auth.
// Covers exactly what domain-purchase needs: create a Checkout Session and
// verify webhook signatures. Payment flows that need more (subscriptions,
// invoices) extend here.
//
// ponytail: raw REST, no SDK. ~150 lines vs a full dependency. If Stripe
// surface grows, switch to stripe-go.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type StripeClient struct {
	SecretKey     string
	WebhookSecret string
	HTTP          *http.Client
}

func NewStripeClient(secretKey, webhookSecret string) *StripeClient {
	return &StripeClient{
		SecretKey:     secretKey,
		WebhookSecret: webhookSecret,
		HTTP:          &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *StripeClient) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.stripe.com/v1"+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.SecretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("stripe %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// CheckoutSession is a subset of Stripe's Checkout Session.
type CheckoutSession struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

// CreateCheckoutSession creates a one-time payment Checkout Session.
func (s *StripeClient) CreateCheckoutSession(ctx context.Context, amountCents int64, currency, description, domainName, successURL, cancelURL string, metadata map[string]string) (*CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", currency)
	form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", amountCents))
	form.Set("line_items[0][price_data][product_data][name]", description)
	// Attach metadata so the webhook can map payment → domain.
	for k, v := range metadata {
		form.Set("metadata["+k+"]", v)
	}
	var out CheckoutSession
	err := s.do(ctx, http.MethodPost, "/checkout/sessions", form, &out)
	return &out, err
}

// Event is a Stripe webhook event (subset).
type Event struct {
	ID   string    `json:"id"`
	Type string    `json:"type"`
	Data EventData `json:"data"`
}

type EventData struct {
	Object json.RawMessage `json:"object"`
}

// VerifyWebhookSignature validates the Stripe signature header for a raw body.
// Returns the parsed event.
func (s *StripeClient) VerifyWebhookSignature(payload []byte, sigHeader string) (*Event, error) {
	// Format: t=<timestamp>,v1=<hmac>
	parts := map[string]string{}
	for _, kv := range strings.Split(sigHeader, ",") {
		kv = strings.TrimSpace(kv)
		if i := strings.Index(kv, "="); i > 0 {
			parts[kv[:i]] = kv[i+1:]
		}
	}
	sig := parts["v1"]
	if sig == "" {
		return nil, fmt.Errorf("stripe webhook: missing v1 signature")
	}
	// Stripe signs `t.<payload>` with the webhook secret.
	msg := []byte(parts["t"] + "." + string(payload))
	expected := hmacSHA256(s.WebhookSecret, msg)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return nil, fmt.Errorf("stripe webhook: invalid signature")
	}
	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("stripe webhook: bad JSON: %w", err)
	}
	return &ev, nil
}

func hmacSHA256(key string, msg []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(msg)
	return hex.EncodeToString(mac.Sum(nil))
}

// CheckoutSessionDetail is the subset read back when confirming a payment.
type CheckoutSessionDetail struct {
	ID            string            `json:"id"`
	Status        string            `json:"status"`
	PaymentStatus string            `json:"payment_status"`
	AmountTotal   int64             `json:"amount_total"`
	Currency      string            `json:"currency"`
	PaymentIntent string            `json:"payment_intent"`
	Metadata      map[string]string `json:"metadata"`
}

// RetrieveCheckoutSession reads a Checkout Session to confirm it was paid.
func (s *StripeClient) RetrieveCheckoutSession(ctx context.Context, id string) (*CheckoutSessionDetail, error) {
	var out CheckoutSessionDetail
	err := s.do(ctx, http.MethodGet, "/checkout/sessions/"+url.PathEscape(id), nil, &out)
	return &out, err
}

// CheckCredentials performs a read-only call to validate the key. Restricted
// keys only need Checkout Sessions read permission.
func (s *StripeClient) CheckCredentials(ctx context.Context) error {
	return s.do(ctx, http.MethodGet, "/checkout/sessions?limit=1", nil, nil)
}

// ---- Connect (stripe_connect_multitenant_v1) ---------------------------------

// ConnectAccount is the subset of a connected account the platform needs.
type ConnectAccount struct {
	ID               string `json:"id"`
	ChargesEnabled   bool   `json:"charges_enabled"`
	PayoutsEnabled   bool   `json:"payouts_enabled"`
	DetailsSubmitted bool   `json:"details_submitted"`
	Requirements     struct {
		CurrentlyDue        []string `json:"currently_due"`
		PastDue             []string `json:"past_due"`
		PendingVerification []string `json:"pending_verification"`
		DisabledReason      string   `json:"disabled_reason"`
	} `json:"requirements"`
	ExternalAccounts struct {
		Data []struct {
			Last4 string `json:"last4"`
		} `json:"data"`
	} `json:"external_accounts"`
}

// managedRiskAPIVersion: Express dashboard + Stripe as losses collector is a
// public preview of Accounts v2 (docs: connected-accounts-with-managed-risk).
const managedRiskAPIVersion = "2026-08-26.preview"

// CreateManagedRiskAccount creates the tenant's connected account with Accounts
// v2: Express dashboard, Stripe collects its fees from the account and assumes
// losses (Managed Risk). Stripe requires this model for new live platforms;
// charges must then be direct charges on the account. v1 endpoints (account
// links, login links, retrieve, delete, balance) keep working with the id.
// Coordination id: stripe_connect_managed_risk_v1
func (s *StripeClient) CreateManagedRiskAccount(ctx context.Context, country, email, businessName, website string, metadata map[string]string) (*ConnectAccount, error) {
	body := map[string]any{
		"dashboard": "express",
		"identity":  map[string]any{"country": strings.ToLower(country)},
		"defaults": map[string]any{
			"currency":         "eur",
			"responsibilities": map[string]any{"fees_collector": "stripe", "losses_collector": "stripe"},
		},
		"configuration": map[string]any{"merchant": map[string]any{
			"mcc":          "5812", // eating places & restaurants
			"capabilities": map[string]any{"card_payments": map[string]any{"requested": true}},
		}},
		"metadata": metadata,
	}
	if email != "" {
		body["contact_email"] = email
	}
	if businessName != "" {
		body["display_name"] = businessName
	}
	if website != "" {
		body["defaults"].(map[string]any)["profile"] = map[string]any{"business_url": website}
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.stripe.com/v2/core/accounts", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.SecretKey, "")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Version", managedRiskAPIVersion)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("stripe POST /v2/core/accounts: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &created); err != nil || created.ID == "" {
		return nil, fmt.Errorf("stripe POST /v2/core/accounts: no id")
	}
	return s.RetrieveAccount(ctx, created.ID)
}

// RetrieveAccount reads a connected account's onboarding state.
func (s *StripeClient) RetrieveAccount(ctx context.Context, id string) (*ConnectAccount, error) {
	var out ConnectAccount
	return &out, s.do(ctx, http.MethodGet, "/accounts/"+url.PathEscape(id), nil, &out)
}

// CreateAccountLink returns a single-use hosted onboarding URL.
func (s *StripeClient) CreateAccountLink(ctx context.Context, accountID, refreshURL, returnURL string) (string, error) {
	form := url.Values{}
	form.Set("account", accountID)
	form.Set("type", "account_onboarding")
	form.Set("refresh_url", refreshURL)
	form.Set("return_url", returnURL)
	form.Set("collection_options[fields]", "eventually_due")
	var out struct {
		URL string `json:"url"`
	}
	err := s.do(ctx, http.MethodPost, "/account_links", form, &out)
	return out.URL, err
}

// DeleteAccount removes a connected account. Stripe allows it for test
// accounts at any time and for live Express accounts once balances are zero.
func (s *StripeClient) DeleteAccount(ctx context.Context, accountID string) error {
	return s.do(ctx, http.MethodDelete, "/accounts/"+url.PathEscape(accountID), nil, nil)
}

// ConnectedBalance is the connected account's money not yet paid out.
type ConnectedBalance struct {
	AvailableCents int64 // ready to be paid out to the bank
	PendingCents   int64 // recent charges Stripe has not released yet
}

func (b ConnectedBalance) Total() int64 { return b.AvailableCents + b.PendingCents }

// ConnectedBalanceOf reads the balance of a connected account.
func (s *StripeClient) ConnectedBalanceOf(ctx context.Context, accountID string) (ConnectedBalance, error) {
	var out ConnectedBalance
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.stripe.com/v1/balance", nil)
	if err != nil {
		return out, err
	}
	req.SetBasicAuth(s.SecretKey, "")
	req.Header.Set("Stripe-Account", accountID)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("stripe GET /balance: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var raw struct {
		Available []struct {
			Amount int64 `json:"amount"`
		} `json:"available"`
		Pending []struct {
			Amount int64 `json:"amount"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return out, err
	}
	for _, x := range raw.Available {
		out.AvailableCents += x.Amount
	}
	for _, x := range raw.Pending {
		out.PendingCents += x.Amount
	}
	return out, nil
}

// Live reports whether the key operates on live money.
func (s *StripeClient) Live() bool { return strings.Contains(s.SecretKey, "_live_") }

// CreateLoginLink returns a one-time link to the tenant's Express dashboard.
func (s *StripeClient) CreateLoginLink(ctx context.Context, accountID string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	err := s.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/login_links", url.Values{}, &out)
	return out.URL, err
}

// ConnectCheckout is a Checkout Session created ON the connected account
// (direct charge: the restaurant is merchant of record and pays Stripe's fee;
// the platform keeps FeeCents as application fee). Required by Managed Risk.
// Coordination id: stripe_connect_managed_risk_v1
type ConnectCheckout struct {
	Account        string
	AmountCents    int64
	FeeCents       int64
	Currency       string
	Description    string
	SuccessURL     string
	CancelURL      string
	CustomerEmail  string
	ExpiresAtUnix  int64
	IdempotencyKey string
	Metadata       map[string]string
}

func (s *StripeClient) CreateConnectCheckout(ctx context.Context, in ConnectCheckout) (*CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", in.SuccessURL)
	form.Set("cancel_url", in.CancelURL)
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", in.Currency)
	form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", in.AmountCents))
	form.Set("line_items[0][price_data][product_data][name]", in.Description)
	if in.FeeCents > 0 {
		form.Set("payment_intent_data[application_fee_amount]", fmt.Sprintf("%d", in.FeeCents))
	}
	if in.CustomerEmail != "" {
		form.Set("customer_email", in.CustomerEmail)
	}
	if in.ExpiresAtUnix > 0 {
		form.Set("expires_at", fmt.Sprintf("%d", in.ExpiresAtUnix))
	}
	for k, v := range in.Metadata {
		form.Set("metadata["+k+"]", v)
		form.Set("payment_intent_data[metadata]["+k+"]", v)
	}
	var out CheckoutSession
	err := s.doAs(ctx, http.MethodPost, "/checkout/sessions", form, &out, in.IdempotencyKey, in.Account)
	return &out, err
}

// ConnectCheckoutDetail is what completion verifies (expanded payment intent).
type ConnectCheckoutDetail struct {
	ID            string            `json:"id"`
	PaymentStatus string            `json:"payment_status"`
	AmountTotal   int64             `json:"amount_total"`
	Currency      string            `json:"currency"`
	Metadata      map[string]string `json:"metadata"`
	PaymentIntent struct {
		ID           string `json:"id"`
		OnBehalfOf   string `json:"on_behalf_of"`
		TransferData struct {
			Destination string `json:"destination"`
		} `json:"transfer_data"`
		ApplicationFeeAmount int64 `json:"application_fee_amount"`
	} `json:"payment_intent"`
}

// RetrieveConnectCheckout reads a session that lives on the connected account.
// Finding it under that account is itself proof the money went to it.
func (s *StripeClient) RetrieveConnectCheckout(ctx context.Context, account, id string) (*ConnectCheckoutDetail, error) {
	var out ConnectCheckoutDetail
	err := s.doAs(ctx, http.MethodGet, "/checkout/sessions/"+url.PathEscape(id)+"?expand[]=payment_intent", nil, &out, "", account)
	return &out, err
}

// CreateWebhookEndpoint registers a Connect webhook on the platform account.
// The returned secret must go straight to env, never to logs or the DB.
func (s *StripeClient) CreateWebhookEndpoint(ctx context.Context, endpointURL string, events []string, connect bool, description string) (id, secret string, err error) {
	form := url.Values{}
	form.Set("url", endpointURL)
	for i, e := range events {
		form.Set(fmt.Sprintf("enabled_events[%d]", i), e)
	}
	if connect {
		form.Set("connect", "true")
	}
	form.Set("description", description)
	var out struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	err = s.do(ctx, http.MethodPost, "/webhook_endpoints", form, &out)
	return out.ID, out.Secret, err
}

// VerifyWebhookSignatureWithTolerance adds replay protection: the signed
// timestamp must be within tolerance of now.
func (s *StripeClient) VerifyWebhookSignatureWithTolerance(payload []byte, sigHeader string, tolerance time.Duration) (*Event, error) {
	var ts int64
	for _, kv := range strings.Split(sigHeader, ",") {
		kv = strings.TrimSpace(kv)
		if strings.HasPrefix(kv, "t=") {
			_, _ = fmt.Sscan(kv[2:], &ts)
		}
	}
	if ts <= 0 {
		return nil, fmt.Errorf("stripe webhook: missing timestamp")
	}
	if d := time.Since(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return nil, fmt.Errorf("stripe webhook: timestamp outside tolerance")
	}
	// A header may carry several v1 signatures (secret rotation).
	msg := []byte(fmt.Sprintf("%d.%s", ts, payload))
	expected := hmacSHA256(s.WebhookSecret, msg)
	for _, kv := range strings.Split(sigHeader, ",") {
		kv = strings.TrimSpace(kv)
		if strings.HasPrefix(kv, "v1=") && hmac.Equal([]byte(kv[3:]), []byte(expected)) {
			var ev Event
			if err := json.Unmarshal(payload, &ev); err != nil {
				return nil, fmt.Errorf("stripe webhook: bad JSON: %w", err)
			}
			return &ev, nil
		}
	}
	return nil, fmt.Errorf("stripe webhook: invalid signature")
}

func (s *StripeClient) doIdempotent(ctx context.Context, method, path string, form url.Values, out any, key string) error {
	return s.doAs(ctx, method, path, form, out, key, "")
}

// doAs is do() with an optional Idempotency-Key and an optional connected
// account (Stripe-Account header: the request runs on that account).
func (s *StripeClient) doAs(ctx context.Context, method, path string, form url.Values, out any, key, account string) error {
	if key == "" && account == "" {
		return s.do(ctx, method, path, form, out)
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.stripe.com/v1"+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.SecretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if account != "" {
		req.Header.Set("Stripe-Account", account)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("stripe %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, out)
}
