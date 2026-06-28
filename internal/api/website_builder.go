package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"preactvillacarmen/internal/httpx"
)

type WebsiteConfig struct {
	ID           int     `json:"id"`
	RestaurantID int     `json:"restaurant_id"`
	TemplateID   *string `json:"template_id"`
	CustomHTML   *string `json:"custom_html"`
	Domain       *string `json:"domain"`
	IsPublished  bool    `json:"is_published"`
}

func (s *Server) handleGetWebsiteConfig(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var config WebsiteConfig
	var templateID, customHTML, domain sql.NullString
	var isPublished bool

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, restaurant_id, template_id, custom_html, domain, is_published
		FROM restaurant_websites
		WHERE restaurant_id = ?
		LIMIT 1
	`, a.ActiveRestaurantID).Scan(&config.ID, &config.RestaurantID, &templateID, &customHTML, &domain, &isPublished)

	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": nil})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error al obtener configuración")
		return
	}

	if templateID.Valid {
		config.TemplateID = &templateID.String
	}
	if customHTML.Valid {
		config.CustomHTML = &customHTML.String
	}
	if domain.Valid {
		config.Domain = &domain.String
	}
	config.IsPublished = isPublished

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": config})
}

func (s *Server) handleUpdateWebsiteConfig(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		TemplateID  *string `json:"template_id"`
		CustomHTML  *string `json:"custom_html"`
		IsPublished *bool   `json:"is_published"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	var id int
	err := s.db.QueryRowContext(r.Context(), "SELECT id FROM restaurant_websites WHERE restaurant_id = ?", a.ActiveRestaurantID).Scan(&id)

	if err == sql.ErrNoRows {
		published := false
		if req.IsPublished != nil {
			published = *req.IsPublished
		}
		_, err := s.db.ExecContext(r.Context(), `
			INSERT INTO restaurant_websites (restaurant_id, template_id, custom_html, is_published)
			VALUES (?, ?, ?, ?)
		`, a.ActiveRestaurantID, req.TemplateID, req.CustomHTML, published)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error al guardar configuración")
			return
		}
	} else if err == nil {
		query := "UPDATE restaurant_websites SET "
		args := []any{}

		if req.TemplateID != nil {
			query += "template_id = ?, "
			args = append(args, *req.TemplateID)
		}
		if req.CustomHTML != nil {
			query += "custom_html = ?, "
			args = append(args, *req.CustomHTML)
		}
		if req.IsPublished != nil {
			query += "is_published = ?, "
			args = append(args, *req.IsPublished)
		}

		query = query[:len(query)-2]
		query += " WHERE restaurant_id = ?"
		args = append(args, a.ActiveRestaurantID)

		_, err := s.db.ExecContext(r.Context(), query, args...)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error al actualizar configuración")
			return
		}
	} else {
		httpx.WriteError(w, http.StatusInternalServerError, "Error de base de datos")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleAIWebsiteGenerate(w http.ResponseWriter, r *http.Request) {
	_, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	mockHTML := "<html><body><header><h1>Mi Restaurante</h1></header><main><p>Sitio web generado por IA. Prompt: " + req.Prompt + "</p></main></body></html>"

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"custom_html": mockHTML,
	})
}
