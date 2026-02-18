package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

const (
	boMemberAvatarReadLimit = 4 << 20
	boMemberAvatarMaxBytes  = 200 * 1024
)

func (s *Server) handleBOMemberAvatarUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	if !s.bunnyMembersConfigured() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Storage de avatar no configurado en servidor",
		})
		return
	}

	if err := r.ParseMultipartForm(boMemberAvatarReadLimit); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "No se pudo procesar el fichero",
		})
		return
	}

	f, _, err := r.FormFile("avatar")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Selecciona una imagen",
		})
		return
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, boMemberAvatarReadLimit+1))
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo leer la imagen")
		return
	}
	if len(raw) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "La imagen esta vacia",
		})
		return
	}
	if len(raw) > boMemberAvatarReadLimit {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Imagen demasiado grande",
		})
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(http.DetectContentType(raw)))
	if !strings.HasPrefix(contentType, "image/webp") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Formato invalido: sube una imagen WEBP",
		})
		return
	}
	if len(raw) > boMemberAvatarMaxBytes {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "La imagen debe pesar como maximo 200KB",
		})
		return
	}

	userID := member.ID
	if member.BOUserID != nil && *member.BOUserID > 0 {
		userID = *member.BOUserID
	}

	objectPath := path.Join("images", "avatars", strconv.Itoa(a.ActiveRestaurantID), fmt.Sprintf("user_%d.webp", userID))

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := s.bunnyMembersPut(ctx, objectPath, raw, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo subir el avatar")
		return
	}

	avatarURL := s.bunnyMembersPullURL(objectPath)
	result, err := s.db.ExecContext(r.Context(), `
		UPDATE restaurant_members
		SET photo_url = ?
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
	`, avatarURL, memberID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar el avatar")
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Miembro no encontrado",
		})
		return
	}

	updated, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"member":    updated,
		"avatarUrl": avatarURL,
	})
}
