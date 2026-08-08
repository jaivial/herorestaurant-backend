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
