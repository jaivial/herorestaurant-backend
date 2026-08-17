package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"preactvillacarmen/internal/httpx"
)

type bunnyCDNConfig struct {
	PublicPullBaseURL       string
	PublicStorageZone       string
	PublicStorageAccessKey  string
	MemberPullBaseURL       string
	MemberStorageZone       string
	MemberStorageAccessKey  string
	PrivateStorageZone      string
	PrivateStorageAccessKey string
}

type bunnyCDNConfigInput struct {
	PublicPullBaseURL       *string `json:"publicPullBaseUrl,omitempty"`
	PublicStorageZone       *string `json:"publicStorageZone,omitempty"`
	PublicStorageAccessKey  *string `json:"publicStorageAccessKey,omitempty"`
	MemberPullBaseURL       *string `json:"memberPullBaseUrl,omitempty"`
	MemberStorageZone       *string `json:"memberStorageZone,omitempty"`
	MemberStorageAccessKey  *string `json:"memberStorageAccessKey,omitempty"`
	PrivateStorageZone      *string `json:"privateStorageZone,omitempty"`
	PrivateStorageAccessKey *string `json:"privateStorageAccessKey,omitempty"`
}

type bunnyCDNConfigResponse struct {
	PublicPullBaseURL           string `json:"publicPullBaseUrl"`
	PublicStorageZone           string `json:"publicStorageZone"`
	HasPublicStorageAccessKey   bool   `json:"hasPublicStorageAccessKey"`
	PublicStorageAccessKeyMask  string `json:"publicStorageAccessKeyMask,omitempty"`
	MemberPullBaseURL           string `json:"memberPullBaseUrl"`
	MemberStorageZone           string `json:"memberStorageZone"`
	HasMemberStorageAccessKey   bool   `json:"hasMemberStorageAccessKey"`
	MemberStorageAccessKeyMask  string `json:"memberStorageAccessKeyMask,omitempty"`
	PrivateStorageZone          string `json:"privateStorageZone"`
	HasPrivateStorageAccessKey  bool   `json:"hasPrivateStorageAccessKey"`
	PrivateStorageAccessKeyMask string `json:"privateStorageAccessKeyMask,omitempty"`
	PublicConfigured            bool   `json:"publicConfigured"`
	MembersConfigured           bool   `json:"membersConfigured"`
	PrivateConfigured           bool   `json:"privateConfigured"`
}

func normalizeBunnyPullBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	return value
}

func (s *Server) loadBunnyCDNConfig(ctx context.Context, restaurantID int) (bunnyCDNConfig, error) {
	var cfg bunnyCDNConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT public_pull_base_url, public_storage_zone, public_storage_access_key,
		       member_pull_base_url, member_storage_zone, member_storage_access_key,
		       private_storage_zone, private_storage_access_key
		FROM restaurant_bunnycdn_config WHERE restaurant_id = ? LIMIT 1`, restaurantID).Scan(
		&cfg.PublicPullBaseURL, &cfg.PublicStorageZone, &cfg.PublicStorageAccessKey,
		&cfg.MemberPullBaseURL, &cfg.MemberStorageZone, &cfg.MemberStorageAccessKey,
		&cfg.PrivateStorageZone, &cfg.PrivateStorageAccessKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return bunnyCDNConfig{}, nil
	}
	if err != nil {
		return bunnyCDNConfig{}, err
	}
	cfg.PublicPullBaseURL = normalizeBunnyPullBaseURL(cfg.PublicPullBaseURL)
	cfg.MemberPullBaseURL = normalizeBunnyPullBaseURL(cfg.MemberPullBaseURL)
	cfg.PublicStorageZone = strings.TrimSpace(cfg.PublicStorageZone)
	cfg.PublicStorageAccessKey = strings.TrimSpace(cfg.PublicStorageAccessKey)
	cfg.MemberStorageZone = strings.TrimSpace(cfg.MemberStorageZone)
	cfg.MemberStorageAccessKey = strings.TrimSpace(cfg.MemberStorageAccessKey)
	cfg.PrivateStorageZone = strings.TrimSpace(cfg.PrivateStorageZone)
	cfg.PrivateStorageAccessKey = strings.TrimSpace(cfg.PrivateStorageAccessKey)
	return cfg, nil
}

func bunnySecretMask(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "••••••••"
	}
	return value[:4] + "••••" + value[len(value)-4:]
}

func bunnyPublicConfigured(cfg bunnyCDNConfig) bool {
	return cfg.PublicPullBaseURL != "" && cfg.PublicStorageZone != "" && cfg.PublicStorageAccessKey != ""
}

func bunnyMembersConfiguredForConfig(cfg bunnyCDNConfig) bool {
	return cfg.MemberPullBaseURL != "" && cfg.MemberStorageZone != "" && cfg.MemberStorageAccessKey != ""
}

func bunnyPrivateConfiguredForConfig(cfg bunnyCDNConfig) bool {
	return cfg.PrivateStorageZone != "" && cfg.PrivateStorageAccessKey != ""
}

func bunnyCDNConfigView(cfg bunnyCDNConfig) bunnyCDNConfigResponse {
	return bunnyCDNConfigResponse{
		PublicPullBaseURL: cfg.PublicPullBaseURL, PublicStorageZone: cfg.PublicStorageZone,
		HasPublicStorageAccessKey: cfg.PublicStorageAccessKey != "", PublicStorageAccessKeyMask: bunnySecretMask(cfg.PublicStorageAccessKey),
		MemberPullBaseURL: cfg.MemberPullBaseURL, MemberStorageZone: cfg.MemberStorageZone,
		HasMemberStorageAccessKey: cfg.MemberStorageAccessKey != "", MemberStorageAccessKeyMask: bunnySecretMask(cfg.MemberStorageAccessKey),
		PrivateStorageZone:         cfg.PrivateStorageZone,
		HasPrivateStorageAccessKey: cfg.PrivateStorageAccessKey != "", PrivateStorageAccessKeyMask: bunnySecretMask(cfg.PrivateStorageAccessKey),
		PublicConfigured: bunnyPublicConfigured(cfg), MembersConfigured: bunnyMembersConfiguredForConfig(cfg), PrivateConfigured: bunnyPrivateConfiguredForConfig(cfg),
	}
}

func (s *Server) handleBOBunnyCDNConfigGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	cfg, err := s.loadBunnyCDNConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuracion de BunnyCDN")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": bunnyCDNConfigView(cfg)})
}

func (s *Server) handleBOBunnyCDNConfigSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var in bunnyCDNConfigInput
	if err := readJSONBody(r, &in); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON"})
		return
	}
	cfg, err := s.loadBunnyCDNConfig(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuracion de BunnyCDN")
		return
	}
	if in.PublicPullBaseURL != nil {
		cfg.PublicPullBaseURL = normalizeBunnyPullBaseURL(*in.PublicPullBaseURL)
		if strings.TrimSpace(*in.PublicPullBaseURL) != "" && cfg.PublicPullBaseURL == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "URL publica de BunnyCDN invalida"})
			return
		}
	}
	if in.PublicStorageZone != nil {
		cfg.PublicStorageZone = strings.TrimSpace(*in.PublicStorageZone)
	}
	if in.PublicStorageAccessKey != nil && strings.TrimSpace(*in.PublicStorageAccessKey) != "" {
		cfg.PublicStorageAccessKey = strings.TrimSpace(*in.PublicStorageAccessKey)
	}
	if in.MemberPullBaseURL != nil {
		cfg.MemberPullBaseURL = normalizeBunnyPullBaseURL(*in.MemberPullBaseURL)
		if strings.TrimSpace(*in.MemberPullBaseURL) != "" && cfg.MemberPullBaseURL == "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "URL publica de avatares invalida"})
			return
		}
	}
	if in.MemberStorageZone != nil {
		cfg.MemberStorageZone = strings.TrimSpace(*in.MemberStorageZone)
	}
	if in.MemberStorageAccessKey != nil && strings.TrimSpace(*in.MemberStorageAccessKey) != "" {
		cfg.MemberStorageAccessKey = strings.TrimSpace(*in.MemberStorageAccessKey)
	}
	if in.PrivateStorageZone != nil {
		cfg.PrivateStorageZone = strings.TrimSpace(*in.PrivateStorageZone)
	}
	if in.PrivateStorageAccessKey != nil && strings.TrimSpace(*in.PrivateStorageAccessKey) != "" {
		cfg.PrivateStorageAccessKey = strings.TrimSpace(*in.PrivateStorageAccessKey)
	}
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO restaurant_bunnycdn_config
		(restaurant_id, public_pull_base_url, public_storage_zone, public_storage_access_key, member_pull_base_url, member_storage_zone, member_storage_access_key, private_storage_zone, private_storage_access_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE public_pull_base_url=VALUES(public_pull_base_url), public_storage_zone=VALUES(public_storage_zone), public_storage_access_key=VALUES(public_storage_access_key), member_pull_base_url=VALUES(member_pull_base_url), member_storage_zone=VALUES(member_storage_zone), member_storage_access_key=VALUES(member_storage_access_key), private_storage_zone=VALUES(private_storage_zone), private_storage_access_key=VALUES(private_storage_access_key)`,
		a.ActiveRestaurantID, cfg.PublicPullBaseURL, cfg.PublicStorageZone, cfg.PublicStorageAccessKey, cfg.MemberPullBaseURL, cfg.MemberStorageZone, cfg.MemberStorageAccessKey, cfg.PrivateStorageZone, cfg.PrivateStorageAccessKey)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando configuracion de BunnyCDN")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": bunnyCDNConfigView(cfg)})
}
