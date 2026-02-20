package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
)

type restaurantBrandingCfg struct {
	BrandName        string
	LogoURL          string
	PrimaryColor     string
	AccentColor      string
	EmailFromName    string
	EmailFromAddress string
}

func (s *Server) loadRestaurantBranding(ctx context.Context, restaurantID int) (restaurantBrandingCfg, error) {
	var (
		brandName        string
		logoURL          sql.NullString
		primaryColor     sql.NullString
		accentColor      sql.NullString
		emailFromName    sql.NullString
		emailFromAddress sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(NULLIF(TRIM(rb.brand_name), ''), r.name) AS brand_name,
			rb.logo_url,
			rb.primary_color,
			rb.accent_color,
			rb.email_from_name,
			rb.email_from_address
		FROM restaurants r
		LEFT JOIN restaurant_branding rb ON rb.restaurant_id = r.id
		WHERE r.id = ?
		LIMIT 1
	`, restaurantID).Scan(&brandName, &logoURL, &primaryColor, &accentColor, &emailFromName, &emailFromAddress)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restaurantBrandingCfg{}, nil
		}
		return restaurantBrandingCfg{}, err
	}

	return restaurantBrandingCfg{
		BrandName:        strings.TrimSpace(brandName),
		LogoURL:          strings.TrimSpace(logoURL.String),
		PrimaryColor:     strings.TrimSpace(primaryColor.String),
		AccentColor:      strings.TrimSpace(accentColor.String),
		EmailFromName:    strings.TrimSpace(emailFromName.String),
		EmailFromAddress: strings.TrimSpace(emailFromAddress.String),
	}, nil
}

func (s *Server) restaurantFallbackEmail(ctx context.Context, restaurantID int) string {
	cfg, err := s.loadRestaurantBranding(ctx, restaurantID)
	if err == nil && strings.TrimSpace(cfg.EmailFromAddress) != "" {
		return strings.TrimSpace(cfg.EmailFromAddress)
	}
	if v := strings.TrimSpace(os.Getenv("DEFAULT_CONTACT_EMAIL")); v != "" {
		return v
	}
	// Keep behavior stable for existing deployments without branding configured.
	return "reservas@alqueriavillacarmen.com"
}

type restaurantIntegrationsCfg struct {
	N8nWebhookURL              string
	EnabledEvents              []string
	UazapiURL                  string
	UazapiToken                string
	RestaurantWhatsappNumbers  []string
}

func (s *Server) loadProvisionedUAZAPICredentials(ctx context.Context, restaurantID int) (uazURL string, uazToken string, err error) {
	var (
		baseURL sql.NullString
		token   sql.NullString
	)
	err = s.db.QueryRowContext(ctx, `
		SELECT s.base_url, i.instance_token
		FROM restaurant_uazapi_instances i
		JOIN uazapi_servers s ON s.id = i.server_id
		WHERE i.restaurant_id = ? AND i.is_active = 1
		LIMIT 1
	`, restaurantID).Scan(&baseURL, &token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || isSQLSchemaError(err) {
			return "", "", nil
		}
		return "", "", err
	}

	uazURL = strings.TrimRight(strings.TrimSpace(baseURL.String), "/")
	uazToken = strings.TrimSpace(token.String)
	if uazURL == "" || uazToken == "" {
		return "", "", nil
	}
	return uazURL, uazToken, nil
}

func (s *Server) loadRestaurantIntegrations(ctx context.Context, restaurantID int) (restaurantIntegrationsCfg, error) {
	var (
		webhook    sql.NullString
		enabledRaw sql.NullString
		uazURL     sql.NullString
		uazToken   sql.NullString
		numbersRaw sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT n8n_webhook_url, enabled_events_json, uazapi_url, uazapi_token, restaurant_whatsapp_numbers_json
		FROM restaurant_integrations
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&webhook, &enabledRaw, &uazURL, &uazToken, &numbersRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restaurantIntegrationsCfg{}, nil
		}
		return restaurantIntegrationsCfg{}, err
	}

	enabled := []string{}
	if enabledRaw.Valid && strings.TrimSpace(enabledRaw.String) != "" {
		_ = json.Unmarshal([]byte(enabledRaw.String), &enabled)
	}

	restaurantNumbers := []string{}
	if numbersRaw.Valid && strings.TrimSpace(numbersRaw.String) != "" {
		_ = json.Unmarshal([]byte(numbersRaw.String), &restaurantNumbers)
	}

	return restaurantIntegrationsCfg{
		N8nWebhookURL:             strings.TrimSpace(webhook.String),
		EnabledEvents:             enabled,
		UazapiURL:                 strings.TrimSpace(uazURL.String),
		UazapiToken:               strings.TrimSpace(uazToken.String),
		RestaurantWhatsappNumbers: restaurantNumbers,
	}, nil
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func normalizeWhatsappRecipients(list []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, raw := range list {
		n := digitsOnly(raw)
		if n == "" {
			continue
		}
		// Common case: Spanish local numbers.
		if len(n) == 9 {
			n = "34" + n
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func (s *Server) uazapiBaseAndToken(ctx context.Context, restaurantID int) (uazURL string, uazToken string) {
	if provisionedURL, provisionedToken, err := s.loadProvisionedUAZAPICredentials(ctx, restaurantID); err == nil {
		if provisionedURL != "" && provisionedToken != "" {
			return provisionedURL, provisionedToken
		}
	}

	cfg, err := s.loadRestaurantIntegrations(ctx, restaurantID)
	if err != nil {
		return "", ""
	}

	uazURL = strings.TrimRight(strings.TrimSpace(cfg.UazapiURL), "/")
	uazToken = strings.TrimSpace(cfg.UazapiToken)
	// Backwards-compat fallback to env.
	if uazURL == "" {
		uazURL = strings.TrimRight(strings.TrimSpace(os.Getenv("UAZAPI_URL")), "/")
	}
	if uazToken == "" {
		uazToken = strings.TrimSpace(os.Getenv("UAZAPI_TOKEN"))
	}
	return uazURL, uazToken
}

func (s *Server) uazapiSendTextURL(ctx context.Context, restaurantID int) (sendURL string, recipients []string) {
	uazURL, uazToken := s.uazapiBaseAndToken(ctx, restaurantID)
	if uazURL == "" {
		return "", nil
	}

	sendURL = uazURL + "/send/text"
	if uazToken != "" {
		sendURL += "?token=" + url.QueryEscape(uazToken)
	}

	cfg, err := s.loadRestaurantIntegrations(ctx, restaurantID)
	if err != nil {
		return sendURL, nil
	}
	recipients = normalizeWhatsappRecipients(cfg.RestaurantWhatsappNumbers)
	return sendURL, recipients
}

func (s *Server) sendRestaurantWhatsAppText(ctx context.Context, restaurantID int, text string) {
	sendURL, recipients := s.uazapiSendTextURL(ctx, restaurantID)
	if sendURL == "" || len(recipients) == 0 || strings.TrimSpace(text) == "" {
		return
	}

	for _, n := range recipients {
		_, _, _ = sendUazAPI(ctx, sendURL, map[string]any{
			"number": n,
			"text":   text,
		})
	}
}
