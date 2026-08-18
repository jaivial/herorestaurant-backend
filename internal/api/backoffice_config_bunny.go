package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// BunnyCDN storage configuration (root-only). DB-backed replacement for the
// env-only BUNNY_* variables, scoped per restaurant. The three fields mirror
// what Bunny shows in the storage zone panel. The access key is never returned
// to clients: GET returns only hasStorageKey + a masked preview.

type boBunnyStorageConfig struct {
	StorageZone    string `json:"storageZone,omitempty"`
	HasStorageKey  bool   `json:"hasStorageKey"`
	StorageKeyMask string `json:"storageKeyMask,omitempty"`
	PullBaseURL    string `json:"pullBaseUrl,omitempty"`
	IsActive       bool   `json:"isActive"`
	// UsingEnvFallback reports that nothing stored is in effect yet, so the
	// process-wide BUNNY_* values are still serving this restaurant.
	UsingEnvFallback bool `json:"usingEnvFallback"`
}

type boBunnyStorageConfigSetRequest struct {
	StorageZone *string `json:"storageZone,omitempty"`
	// nil or empty string = keep the stored key. Any non-empty value replaces it.
	StorageKey  *string `json:"storageKey,omitempty"`
	PullBaseURL *string `json:"pullBaseUrl,omitempty"`
	IsActive    *bool   `json:"isActive,omitempty"`
}

// validateBunnyPullBaseURL keeps a typo from silently producing image URLs that
// resolve nowhere. Empty is allowed: it means "fall back to env".
func validateBunnyPullBaseURL(raw string) error {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return errors.New("no se pudo interpretar la URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("debe empezar por http:// o https://")
	}
	if u.Host == "" {
		return errors.New("falta el dominio")
	}
	return nil
}

// validateBunnyStorageZone rejects the full endpoint URL Bunny displays
// (https://storage.bunnycdn.com/<zone>), since only the zone name belongs here
// and pasting the URL would break every upload.
func validateBunnyStorageZone(raw string) error {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	if strings.Contains(v, "/") || strings.Contains(v, ":") {
		return errors.New("indica solo el nombre de la zona, no la URL completa")
	}
	return nil
}

func (s *Server) handleBOBunnyStorageConfigGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	row, found, err := s.loadBunnyStorageRow(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuracion"})
		return
	}
	cfg := boBunnyStorageConfig{
		StorageZone:      row.creds.StorageZone,
		HasStorageKey:    row.creds.StorageKey != "",
		StorageKeyMask:   maskAPIKey(row.creds.StorageKey),
		PullBaseURL:      row.creds.PullBaseURL,
		IsActive:         row.IsActive,
		UsingEnvFallback: !found || !row.IsActive,
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "config": cfg})
}

func (s *Server) handleBOBunnyStorageConfigSet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}

	var req boBunnyStorageConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}

	ctx := r.Context()
	current, _, err := s.loadBunnyStorageRow(ctx, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error cargando configuracion"})
		return
	}

	// Absent field = keep current. For zone/URL an explicit "" clears the value
	// (falling back to env); for the key "" means keep, since the client never
	// receives the stored key to send back.
	next := current.creds
	if req.StorageZone != nil {
		next.StorageZone = strings.TrimSpace(*req.StorageZone)
	}
	if req.PullBaseURL != nil {
		next.PullBaseURL = strings.TrimSpace(*req.PullBaseURL)
	}
	if req.StorageKey != nil && strings.TrimSpace(*req.StorageKey) != "" {
		next.StorageKey = strings.TrimSpace(*req.StorageKey)
	}

	isActive := current.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	if err := validateBunnyStorageZone(next.StorageZone); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Zona de almacenamiento no valida: " + err.Error()})
		return
	}
	if err := validateBunnyPullBaseURL(next.PullBaseURL); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Pull URL no valida: " + err.Error()})
		return
	}

	// Activating an incomplete zone would break every upload and image URL for
	// this restaurant, so require all three values before enabling.
	if isActive && (next.StorageZone == "" || next.StorageKey == "" || next.PullBaseURL == "") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Para activar necesitas zona de almacenamiento, password y pull URL",
		})
		return
	}

	activeInt := 0
	if isActive {
		activeInt = 1
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO bunny_storage_config
			(restaurant_id, storage_zone, storage_access_key, pull_base_url, is_active, updated_by_user_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			storage_zone = VALUES(storage_zone),
			storage_access_key = VALUES(storage_access_key),
			pull_base_url = VALUES(pull_base_url),
			is_active = VALUES(is_active),
			updated_by_user_id = VALUES(updated_by_user_id)
	`,
		a.ActiveRestaurantID,
		nullIfEmpty(next.StorageZone), nullIfEmpty(next.StorageKey), nullIfEmpty(next.PullBaseURL),
		activeInt, a.User.ID,
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error guardando configuracion"})
		return
	}

	// Drop the cached credentials so the next upload uses what was just saved.
	s.bunnyCredsCache.invalidate(a.ActiveRestaurantID)

	s.handleBOBunnyStorageConfigGet(w, r)
}
