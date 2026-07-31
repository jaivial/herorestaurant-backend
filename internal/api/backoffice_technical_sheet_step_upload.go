package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// Direct (non-AI) step image upload.
//
// The AI path is queued because a provider call takes seconds; a plain upload
// is synchronous because the bytes are already in hand. Both end in the same
// place: normalise to WebP, put it on the CDN, store only the URL.

const stepImageMaxUpload = 8 << 20

func (s *Server) handleBOTechnicalSheetStepImageUpload(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	stepID, _ := strconv.ParseInt(chi.URLParam(r, "stepId"), 10, 64)

	// Resolving the step first keeps a foreign or missing id from ever reaching
	// storage, and gives a 404 instead of a confusing upload error.
	if _, err := s.stepBelongsToSheet(r.Context(), a.ActiveRestaurantID, sheetID, stepID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "Paso no encontrado")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando el paso")
		return
	}

	if err := r.ParseMultipartForm(stepImageMaxUpload); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Formulario invalido")
		return
	}
	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "No se ha enviado ninguna imagen")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, stepImageMaxUpload+1))
	if err != nil || len(raw) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Imagen vacia o ilegible")
		return
	}
	if len(raw) > stepImageMaxUpload {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "Imagen demasiado grande (max 8 MB)")
		return
	}

	contentType := http.DetectContentType(raw)
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		httpx.WriteError(w, http.StatusBadRequest, "Formato no permitido. Usa JPG, PNG o WebP.")
		return
	}

	// Checked before the conversion work: without storage the result has
	// nowhere to live, and recording a URL that resolves to nothing would be
	// worse than refusing.
	if !s.bunnyConfigured() {
		httpx.WriteError(w, http.StatusServiceUnavailable, "El almacenamiento de imagenes no esta configurado")
		return
	}

	normalized, err := specialmenuimage.NormalizeToWebP(r.Context(), raw, "step.webp", contentType)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "No se pudo procesar la imagen")
		return
	}

	objectPath := fmt.Sprintf("recipes/steps/%d/%d-%d.webp", a.ActiveRestaurantID, stepID, time.Now().UnixNano())
	if err := s.bunnyPut(r.Context(), objectPath, normalized, "image/webp"); err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "No se pudo subir la imagen")
		return
	}
	imageURL := s.bunnyPullURL(objectPath)

	// A manual upload supersedes any queued generation: leaving the step
	// PENDING would show a spinner over an image that is already there.
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE stock_recipe_steps
		   SET image_url=?, image_object_path=?, generation_status='NONE',
		       generation_mode=NULL, generation_error=NULL
		 WHERE restaurant_id=? AND id=? AND recipe_id=?`,
		imageURL, objectPath, a.ActiveRestaurantID, stepID, sheetID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando la imagen")
		return
	}

	s.sheetHub.broadcastImageJob(a.ActiveRestaurantID, map[string]any{
		"stepId": stepID, "status": "READY", "mode": "UPLOAD", "imageUrl": imageURL,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "imageUrl": imageURL})
}
