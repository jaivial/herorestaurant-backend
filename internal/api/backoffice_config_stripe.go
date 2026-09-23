package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/integrations"
	"preactvillacarmen/internal/vault"
)

// =============================================================================
// Per-restaurant Stripe configuration (admin/config?content=stripe).
//
// Secret + webhook secret are vault-encrypted at rest and never returned to
// the browser: the BO only sees masked hints. demo_mode swaps Stripe Checkout
// for a local demo checkout so the whole prereserva flow can be tested without
// charging a card.
// Coordination id: stripe_prereserva_adelanto_v1
// =============================================================================

type stripeSettings struct {
	SecretKey      string
	WebhookSecret  string
	PublishableKey string
	DemoMode       bool
	Currency       string
	Configured     bool // row exists
}

// liveReady: real payments are possible (not demo, secret present).
func (c stripeSettings) liveReady() bool { return !c.DemoMode && c.SecretKey != "" }

// enabled: the restaurant can take stripe adelantos (demo or live).
func (c stripeSettings) enabled() bool { return c.Configured && (c.DemoMode || c.SecretKey != "") }

func (c stripeSettings) client() *integrations.StripeClient {
	return integrations.NewStripeClient(c.SecretKey, c.WebhookSecret)
}

func (s *Server) loadStripeConfig(ctx context.Context, restaurantID int) (stripeSettings, error) {
	var (
		secretEnc, webhookEnc, publishable sql.NullString
		demo                               int
		currency                           string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT secret_key_encrypted, webhook_secret_encrypted, publishable_key, demo_mode, currency
		FROM restaurant_stripe_config WHERE restaurant_id = ?`, restaurantID).
		Scan(&secretEnc, &webhookEnc, &publishable, &demo, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return stripeSettings{Currency: "eur", DemoMode: true}, nil
	}
	if err != nil {
		return stripeSettings{}, err
	}
	out := stripeSettings{PublishableKey: strings.TrimSpace(publishable.String), DemoMode: demo != 0, Currency: currency, Configured: true}
	decrypt := func(v sql.NullString, what string) string {
		if !v.Valid || v.String == "" || s.cfg.VaultToken == "" {
			return ""
		}
		plain, derr := vault.Decrypt(s.cfg.VaultToken, v.String)
		if derr != nil {
			log.Printf("[stripe_prereserva_adelanto_v1] restaurant=%d decrypt %s failed: %v", restaurantID, what, derr)
			return ""
		}
		return strings.TrimSpace(plain)
	}
	out.SecretKey = decrypt(secretEnc, "secret")
	out.WebhookSecret = decrypt(webhookEnc, "webhook")
	return out, nil
}

// maskSecret keeps the key prefix (rk_live_ / sk_test_ …) and the last 4.
func maskSecret(v string) string {
	if v == "" {
		return ""
	}
	prefix := v
	if i := strings.Index(v[3:], "_"); i >= 0 && i+4 < len(v) {
		prefix = v[:i+4]
	} else if len(v) > 8 {
		prefix = v[:8]
	}
	if len(v) <= len(prefix)+4 {
		return prefix + "…"
	}
	return prefix + "…" + v[len(v)-4:]
}

// stripeWebhookURL is served by the restaurant's public website, which
// proxies every /api/* call to this backend (the backoffice host does not).
func (s *Server) stripeWebhookURL(ctx context.Context, restaurantID int) string {
	return strings.TrimRight(resolveRestaurantPublicBaseURL(ctx, s, restaurantID), "/") + "/api/stripe/prereserva-webhook"
}

type stripeConfigDTO struct {
	Configured        bool   `json:"configured"`
	DemoMode          bool   `json:"demo_mode"`
	Currency          string `json:"currency"`
	HasSecretKey      bool   `json:"has_secret_key"`
	SecretKeyHint     string `json:"secret_key_hint"`
	HasWebhookSecret  bool   `json:"has_webhook_secret"`
	WebhookSecretHint string `json:"webhook_secret_hint"`
	PublishableKey    string `json:"publishable_key"`
	LiveMode          bool   `json:"live_mode"`
	WebhookURL        string `json:"webhook_url"`
}

func (s *Server) stripeConfigToDTO(ctx context.Context, restaurantID int, c stripeSettings) stripeConfigDTO {
	return stripeConfigDTO{
		Configured:        c.Configured,
		DemoMode:          c.DemoMode,
		Currency:          c.Currency,
		HasSecretKey:      c.SecretKey != "",
		SecretKeyHint:     maskSecret(c.SecretKey),
		HasWebhookSecret:  c.WebhookSecret != "",
		WebhookSecretHint: maskSecret(c.WebhookSecret),
		PublishableKey:    c.PublishableKey,
		LiveMode:          strings.Contains(c.SecretKey, "_live_"),
		WebhookURL:        s.stripeWebhookURL(ctx, restaurantID),
	}
}

func (s *Server) handleBOStripeConfigGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	cfg, err := s.loadStripeConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuración de Stripe"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": s.stripeConfigToDTO(r.Context(), a.ActiveRestaurantID, cfg)})
}

// Blank secret fields keep the stored value; "clear_*" removes it.
type stripeConfigSetRequest struct {
	SecretKey          string  `json:"secret_key"`
	WebhookSecret      string  `json:"webhook_secret"`
	PublishableKey     *string `json:"publishable_key"`
	DemoMode           *bool   `json:"demo_mode"`
	ClearSecretKey     bool    `json:"clear_secret_key"`
	ClearWebhookSecret bool    `json:"clear_webhook_secret"`
}

func (s *Server) saveStripeConfig(ctx context.Context, restaurantID int, next stripeSettings) error {
	if s.cfg.VaultToken == "" {
		return errors.New("VAULT_TOKEN no configurado")
	}
	enc := func(v string) (any, error) {
		if v == "" {
			return nil, nil
		}
		return vault.Encrypt(s.cfg.VaultToken, v)
	}
	secretEnc, err := enc(next.SecretKey)
	if err != nil {
		return err
	}
	webhookEnc, err := enc(next.WebhookSecret)
	if err != nil {
		return err
	}
	currency := next.Currency
	if currency == "" {
		currency = "eur"
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO restaurant_stripe_config (restaurant_id, secret_key_encrypted, webhook_secret_encrypted, publishable_key, demo_mode, currency)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE secret_key_encrypted = VALUES(secret_key_encrypted),
			webhook_secret_encrypted = VALUES(webhook_secret_encrypted), publishable_key = VALUES(publishable_key),
			demo_mode = VALUES(demo_mode), currency = VALUES(currency)`,
		restaurantID, secretEnc, webhookEnc, nullIfEmpty(strings.TrimSpace(next.PublishableKey)), boolToTinyint(next.DemoMode), currency)
	return err
}

func (s *Server) handleBOStripeConfigSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	var req stripeConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON inválido"})
		return
	}
	cur, err := s.loadStripeConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuración de Stripe"})
		return
	}
	next := cur
	if v := strings.TrimSpace(req.SecretKey); v != "" {
		if !strings.HasPrefix(v, "sk_") && !strings.HasPrefix(v, "rk_") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "La clave secreta debe empezar por sk_ o rk_"})
			return
		}
		next.SecretKey = v
	}
	if req.ClearSecretKey {
		next.SecretKey = ""
	}
	if v := strings.TrimSpace(req.WebhookSecret); v != "" {
		if !strings.HasPrefix(v, "whsec_") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "El secreto del webhook debe empezar por whsec_"})
			return
		}
		next.WebhookSecret = v
	}
	if req.ClearWebhookSecret {
		next.WebhookSecret = ""
	}
	if req.PublishableKey != nil {
		next.PublishableKey = strings.TrimSpace(*req.PublishableKey)
	}
	if req.DemoMode != nil {
		next.DemoMode = *req.DemoMode
	}
	if !next.DemoMode && next.SecretKey == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Para desactivar el modo demo añade la clave secreta de Stripe"})
		return
	}
	if err := s.saveStripeConfig(r.Context(), a.ActiveRestaurantID, next); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error guardando configuración de Stripe"})
		return
	}
	next.Configured = true
	logCheckpoint(r, "stripe_config_saved", "demo_mode", boolStr(next.DemoMode), "has_secret", boolStr(next.SecretKey != ""))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": s.stripeConfigToDTO(r.Context(), a.ActiveRestaurantID, next)})
}

// handleBOStripeConfigTest validates the stored secret against Stripe.
func (s *Server) handleBOStripeConfigTest(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	cfg, err := s.loadStripeConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil || cfg.SecretKey == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No hay clave secreta de Stripe guardada"})
		return
	}
	if err := cfg.client().CheckCredentials(r.Context()); err != nil {
		log.Printf("[stripe_prereserva_adelanto_v1] restaurant=%d credential check failed: %v", a.ActiveRestaurantID, err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Stripe rechazó la clave (revisa permisos de Checkout Sessions)"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "live_mode": strings.Contains(cfg.SecretKey, "_live_")})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
