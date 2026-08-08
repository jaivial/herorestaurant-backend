package integrations

// Platform-level Stripe client: extends the minimal domain-purchase client
// with the calls a superadmin dashboard needs — list recent charges and
// create refunds. Reuses the same raw-REST approach.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type PlatformStripeClient struct {
	SecretKey string
	HTTP      *http.Client
}

func newPlatformStripe(secretKey string) *PlatformStripeClient {
	return &PlatformStripeClient{
		SecretKey: secretKey,
		HTTP:      &http.Client{Timeout: 25 * time.Second},
	}
}

// NewPlatformStripeClient is the exported constructor used by api package.
func NewPlatformStripeClient(secretKey string) *PlatformStripeClient {
	return newPlatformStripe(secretKey)
}

func (c *PlatformStripeClient) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.stripe.com/v1"+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.SecretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.HTTP.Do(req)
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

// ListCharges returns recent charges as normalized maps.
func (c *PlatformStripeClient) ListCharges(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	form := url.Values{}
	form.Set("limit", strconv.Itoa(limit))

	var raw struct {
		Data []struct {
			ID             string            `json:"id"`
			Amount         int64             `json:"amount"`
			AmountRefunded int64             `json:"amount_refunded"`
			Currency       string            `json:"currency"`
			Status         string            `json:"status"`
			Paid           bool              `json:"paid"`
			Refunded       bool              `json:"refunded"`
			ReceiptEmail   string            `json:"receipt_email"`
			Description    string            `json:"description"`
			Created        int64             `json:"created"`
			PaymentIntent  string            `json:"payment_intent"`
			Metadata       map[string]string `json:"metadata"`
			BillingDetails map[string]any    `json:"billing_details"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/charges", form, &raw); err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(raw.Data))
	for _, ch := range raw.Data {
		out = append(out, map[string]any{
			"id":             ch.ID,
			"amount":         ch.Amount,
			"amountRefunded": ch.AmountRefunded,
			"currency":       ch.Currency,
			"status":         ch.Status,
			"paid":           ch.Paid,
			"refunded":       ch.Refunded,
			"receiptEmail":   ch.ReceiptEmail,
			"description":    ch.Description,
			"created":        ch.Created,
			"paymentIntent":  ch.PaymentIntent,
			"metadata":       ch.Metadata,
			"billingDetails": ch.BillingDetails,
		})
	}
	return out, nil
}

// CreateRefund issues a refund for a charge or payment intent.
// If amount is 0, it refunds the full amount.
func (c *PlatformStripeClient) CreateRefund(ctx context.Context, chargeOrPIID string, amount int64, reason string) (map[string]any, error) {
	form := url.Values{}
	form.Set("reason", reason)
	if strings.HasPrefix(chargeOrPIID, "pi_") {
		form.Set("payment_intent", chargeOrPIID)
	} else {
		form.Set("charge", chargeOrPIID)
	}
	if amount > 0 {
		form.Set("amount", strconv.FormatInt(amount, 10))
	}

	var out map[string]any
	if err := c.do(ctx, http.MethodPost, "/refunds", form, &out); err != nil {
		return nil, err
	}
	return out, nil
}
