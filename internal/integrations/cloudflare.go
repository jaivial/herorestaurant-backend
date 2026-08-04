package integrations

// Cloudflare DNS client (minimal). Uses Global API key auth (email + key) —
// the account also owns Registrar, but Registrar calls need an account-scoped
// token; the key is sufficient for Zone + DNS records which is what serving
// needs.
//
// ponytail: no SDK, raw REST. Covers exactly what website serving needs:
// zone lookup/create, DNS record upsert/delete. Registrar/domain-purchase
// (P6) will need a scoped token — flagged there.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CloudflareClient struct {
	Email   string
	APIKey  string
	Token   string // account-scoped API token (Bearer); used when set, overrides global key
	Account string // account id, optional
	Base    string
	HTTP    *http.Client
}

func NewCloudflareClient(email, apiKey, token, account string) *CloudflareClient {
	return &CloudflareClient{
		Email:   email,
		APIKey:  apiKey,
		Token:   token,
		Account: account,
		Base:    "https://api.cloudflare.com/client/v4",
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

type cfResponse struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *CloudflareClient) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var cf cfResponse
	if err := json.Unmarshal(b, &cf); err != nil {
		return fmt.Errorf("cloudflare %s %s: bad response: %s", method, path, strings.TrimSpace(string(b)))
	}
	if !cf.Success {
		if len(cf.Errors) > 0 {
			return fmt.Errorf("cloudflare %s %s: %s (code %d)", method, path, cf.Errors[0].Message, cf.Errors[0].Code)
		}
		return fmt.Errorf("cloudflare %s %s: request failed", method, path)
	}
	if out != nil {
		return json.Unmarshal(cf.Result, out)
	}
	return nil
}

// Zone is the subset of a Cloudflare zone we need.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (c *CloudflareClient) ListZones(ctx context.Context, name string) ([]Zone, error) {
	var out []Zone
	path := "/zones?per_page=50"
	if name != "" {
		path += "&name=" + name
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *CloudflareClient) EnsureZone(ctx context.Context, name string) (Zone, error) {
	zones, err := c.ListZones(ctx, name)
	if err != nil {
		return Zone{}, err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, name) {
			return z, nil
		}
	}
	// Create zone (full setup; records we add explicitly).
	var created Zone
	body := strings.NewReader(fmt.Sprintf(`{"name":%q,"type":"full","jump_start":false}`, name))
	if err := c.do(ctx, http.MethodPost, "/zones", body, &created); err != nil {
		return Zone{}, err
	}
	return created, nil
}

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// EnsureDNSRecord upserts a DNS record (A for subdomains of the apex, CNAME
// for custom domains pointing at the apex). proxied=true gives automatic TLS.
func (c *CloudflareClient) EnsureDNSRecord(ctx context.Context, zoneID, rtype, name, content string, proxied bool) (*DNSRecord, error) {
	// Look for an existing record with the same type+name.
	var existing []DNSRecord
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, rtype, name), nil, &existing)
	if err == nil && len(existing) > 0 {
		rec := existing[0]
		if rec.Content != content || rec.Proxied != proxied {
			body := strings.NewReader(fmt.Sprintf(`{"type":%q,"name":%q,"content":%q,"proxied":%t}`, rtype, name, content, proxied))
			var updated DNSRecord
			if err := c.do(ctx, http.MethodPut, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, rec.ID), body, &updated); err != nil {
				return nil, err
			}
			return &updated, nil
		}
		return &rec, nil
	}
	// Create.
	body := strings.NewReader(fmt.Sprintf(`{"type":%q,"name":%q,"content":%q,"proxied":%t}`, rtype, name, content, proxied))
	var created DNSRecord
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), body, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (c *CloudflareClient) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), nil, nil)
}

// ---------------------------------------------------------------------------
// Registrar (domain purchase).
// ---------------------------------------------------------------------------

// RegistrarDomain is the domain-check result for one domain.
type RegistrarDomain struct {
	Name         string `json:"name"`
	Registrable  bool   `json:"registrable"`
	Tier         string `json:"tier"`
	Reason       string `json:"reason"`
	PriceEUR     string `json:"price_eur,omitempty"`
	Pricing      struct {
		Currency        string `json:"currency"`
		RegistrationCost string `json:"registration_cost"`
		RenewalCost     string `json:"renewal_cost"`
	} `json:"pricing"`
	Available  *bool  `json:"available"` // set only for owned/registrar-managed
	CanRegister bool  `json:"can_register"`
	PremiumType string `json:"premium_type"`
}

// SupportedTLD reports whether the domain's extension is registrable.
func (d *RegistrarDomain) SupportedTLD() bool {
	return d.Registrable || (d.Tier != "" && d.Reason == "")
}

// Do performs an authenticated raw CF API call (exposed for registrar + other
// account-scoped endpoints that don't fit the typed helpers).
func (c *CloudflareClient) Do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return b, fmt.Errorf("cloudflare %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// CheckDomain returns availability for a single domain. Cloudflare's API
// exposes availability via the domain-check endpoint.
func (c *CloudflareClient) CheckDomain(ctx context.Context, name string) (*RegistrarDomain, error) {
	// Real-time check: POST /accounts/{id}/registrar/domain-check {domains:[...]}
	// The `result` field is already `{domains:[...]}` — parse it directly.
	body := strings.NewReader(fmt.Sprintf(`{"domains":[%q]}`, name))
	var resp struct {
		Domains []RegistrarDomain `json:"domains"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/accounts/%s/registrar/domain-check", c.Account), body, &resp); err != nil {
		return &RegistrarDomain{Name: name}, err
	}
	if len(resp.Domains) > 0 {
		return &resp.Domains[0], nil
	}
	return &RegistrarDomain{Name: name}, nil
}

// RegisterDomain purchases a domain via Cloudflare Registrar.
// Prerequisites: registrar-write token, billing profile, default registrant
// contact configured, and the Domain Registration Agreement accepted.
// body is the full JSON payload (domain_name + contacts.registrant).
// See https://developers.cloudflare.com/registrar/registrar-api/.
func (c *CloudflareClient) RegisterDomain(ctx context.Context, body string) ([]byte, error) {
	return c.Do(ctx, http.MethodPost, "/accounts/"+c.Account+"/registrar/registrations", strings.NewReader(body))
}

// RegistrationStatus polls a registration's state.
func (c *CloudflareClient) RegistrationStatus(ctx context.Context, name string) ([]byte, error) {
	return c.Do(ctx, http.MethodGet, "/accounts/"+c.Account+"/registrar/registrations/"+name+"/registration-status", nil)
}

