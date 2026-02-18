package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"preactvillacarmen/internal/httpx"
)

type boIntegrations struct {
	N8nWebhookURL  string   `json:"n8nWebhookUrl"`
	EnabledEvents  []string `json:"enabledEvents"`
	UazapiURL      string   `json:"uazapiUrl"`
	UazapiToken    string   `json:"uazapiToken"`
	RestaurantWhatsappNumbers []string `json:"restaurantWhatsappNumbers"`
}

type boBranding struct {
	BrandName        string `json:"brandName"`
	LogoURL          string `json:"logoUrl"`
	PrimaryColor     string `json:"primaryColor"`
	AccentColor      string `json:"accentColor"`
	EmailFromName    string `json:"emailFromName"`
	EmailFromAddress string `json:"emailFromAddress"`
}

func (s *Server) handleBOIntegrationsGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	var webhook sql.NullString
	var enabledRaw sql.NullString
	var uazURL sql.NullString
	var uazToken sql.NullString
	var numbersRaw sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT n8n_webhook_url, enabled_events_json, uazapi_url, uazapi_token, restaurant_whatsapp_numbers_json
		FROM restaurant_integrations
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&webhook, &enabledRaw, &uazURL, &uazToken, &numbersRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando integraciones")
		return
	}

	enabled := []string{}
	if enabledRaw.Valid && strings.TrimSpace(enabledRaw.String) != "" {
		_ = json.Unmarshal([]byte(enabledRaw.String), &enabled)
	}

	restaurantNumbers := []string{}
	if numbersRaw.Valid && strings.TrimSpace(numbersRaw.String) != "" {
		_ = json.Unmarshal([]byte(numbersRaw.String), &restaurantNumbers)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"integrations": boIntegrations{
			N8nWebhookURL: strings.TrimSpace(webhook.String),
			EnabledEvents: enabled,
			UazapiURL:     strings.TrimSpace(uazURL.String),
			UazapiToken:   strings.TrimSpace(uazToken.String),
			RestaurantWhatsappNumbers: restaurantNumbers,
		},
	})
}

func (s *Server) handleBOIntegrationsSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	var input boIntegrations
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	webhook := strings.TrimSpace(input.N8nWebhookURL)
	if webhook != "" {
		u, err := url.Parse(webhook)
		if err != nil || u.Scheme == "" || u.Host == "" {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "n8nWebhookUrl invalida",
			})
			return
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "n8nWebhookUrl debe ser http/https",
			})
			return
		}
	}

	enabledJSON, _ := json.Marshal(input.EnabledEvents)
	uazURL := strings.TrimSpace(input.UazapiURL)
	uazToken := strings.TrimSpace(input.UazapiToken)
	numbersJSON, _ := json.Marshal(input.RestaurantWhatsappNumbers)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO restaurant_integrations
			(restaurant_id, n8n_webhook_url, enabled_events_json, uazapi_url, uazapi_token, restaurant_whatsapp_numbers_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			n8n_webhook_url = VALUES(n8n_webhook_url),
			enabled_events_json = VALUES(enabled_events_json),
			uazapi_url = VALUES(uazapi_url),
			uazapi_token = VALUES(uazapi_token),
			restaurant_whatsapp_numbers_json = VALUES(restaurant_whatsapp_numbers_json)
	`, restaurantID, webhook, string(enabledJSON), uazURL, uazToken, string(numbersJSON))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando integraciones")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"integrations": boIntegrations{
			N8nWebhookURL: webhook,
			EnabledEvents: input.EnabledEvents,
			UazapiURL:     uazURL,
			UazapiToken:   uazToken,
			RestaurantWhatsappNumbers: input.RestaurantWhatsappNumbers,
		},
	})
}

func (s *Server) handleBOBrandingGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	var (
		brandName        sql.NullString
		logoURL          sql.NullString
		primaryColor     sql.NullString
		accentColor      sql.NullString
		emailFromName    sql.NullString
		emailFromAddress sql.NullString
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT brand_name, logo_url, primary_color, accent_color, email_from_name, email_from_address
		FROM restaurant_branding
		WHERE restaurant_id = ?
		LIMIT 1
	`, restaurantID).Scan(&brandName, &logoURL, &primaryColor, &accentColor, &emailFromName, &emailFromAddress)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando branding")
		return
	}

	out := boBranding{
		BrandName:        strings.TrimSpace(brandName.String),
		LogoURL:          strings.TrimSpace(logoURL.String),
		PrimaryColor:     strings.TrimSpace(primaryColor.String),
		AccentColor:      strings.TrimSpace(accentColor.String),
		EmailFromName:    strings.TrimSpace(emailFromName.String),
		EmailFromAddress: strings.TrimSpace(emailFromAddress.String),
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"branding": out,
	})
}

func (s *Server) handleBOBrandingSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	restaurantID := a.ActiveRestaurantID

	var input boBranding
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	// Best-effort trimming; validation is intentionally light.
	input.BrandName = strings.TrimSpace(input.BrandName)
	input.LogoURL = strings.TrimSpace(input.LogoURL)
	input.PrimaryColor = strings.TrimSpace(input.PrimaryColor)
	input.AccentColor = strings.TrimSpace(input.AccentColor)
	input.EmailFromName = strings.TrimSpace(input.EmailFromName)
	input.EmailFromAddress = strings.TrimSpace(input.EmailFromAddress)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO restaurant_branding
			(restaurant_id, brand_name, logo_url, primary_color, accent_color, email_from_name, email_from_address)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			brand_name = VALUES(brand_name),
			logo_url = VALUES(logo_url),
			primary_color = VALUES(primary_color),
			accent_color = VALUES(accent_color),
			email_from_name = VALUES(email_from_name),
			email_from_address = VALUES(email_from_address)
	`, restaurantID, input.BrandName, input.LogoURL, input.PrimaryColor, input.AccentColor, input.EmailFromName, input.EmailFromAddress)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando branding")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"branding": input,
	})
}
