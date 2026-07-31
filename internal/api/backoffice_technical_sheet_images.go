package api

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"preactvillacarmen/internal/httpx"
)

// Step images follow the established pipeline: the client compresses to WebP,
// the server normalises, uploads to the CDN and stores only the resulting URL.
//
// AI work is queued rather than done inline. A provider call can take tens of
// seconds; holding an HTTP request open for that would tie up a connection and
// give the user a spinner with no way to navigate away. The job row is the
// user-visible record of progress.

// completeStepImageJob publishes a finished image. It returns false when the
// job is no longer PENDING/RUNNING - a cancelled or superseded job must never
// overwrite whatever the user has since chosen.
func (s *Server) completeStepImageJob(ctx context.Context, restaurantID int, jobID, stepID int64,
	objectPath, imageURL string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// Claiming the job and publishing the image happen together, so a late
	// result cannot slip past a cancellation.
	res, err := tx.ExecContext(ctx, `
		UPDATE stock_recipe_step_image_jobs
		   SET status='SUCCEEDED', result_object_path=?, finished_at=NOW()
		 WHERE restaurant_id=? AND id=? AND status IN ('PENDING','RUNNING')`,
		objectPath, restaurantID, jobID)
	if err != nil {
		return false, err
	}
	if claimed, _ := res.RowsAffected(); claimed == 0 {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE stock_recipe_steps
		   SET image_url=?, image_object_path=?, generation_status='READY', generation_error=NULL
		 WHERE restaurant_id=? AND id=?`,
		imageURL, objectPath, restaurantID, stepID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	// Pushed only after the commit, so a listener can never observe a state the
	// database has not accepted.
	s.sheetHub.broadcastImageJob(restaurantID, map[string]any{
		"jobId": jobID, "stepId": stepID, "status": "SUCCEEDED", "imageUrl": imageURL,
	})
	return true, nil
}

// failStepImageJob records why the work did not produce an image. Without a
// reason on the step the user sees a permanent spinner and cannot act.
func (s *Server) failStepImageJob(ctx context.Context, restaurantID int, jobID, stepID int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE stock_recipe_step_image_jobs
		   SET status='FAILED', error_message=?, finished_at=NOW()
		 WHERE restaurant_id=? AND id=? AND status IN ('PENDING','RUNNING')`,
		truncateForColumn(reason, 500), restaurantID, jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stock_recipe_steps SET generation_status='FAILED', generation_error=?
		 WHERE restaurant_id=? AND id=?`,
		truncateForColumn(reason, 500), restaurantID, stepID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.sheetHub.broadcastImageJob(restaurantID, map[string]any{
		"jobId": jobID, "stepId": stepID, "status": "FAILED", "errorMessage": reason,
	})
	return nil
}

// generatedIdempotencyKey names a request that did not bring its own key. The
// column is NOT NULL and unique per tenant, so an empty string would make the
// second ever job collide with the first.
func generatedIdempotencyKey() string {
	var buf [16]byte
	if _, err := crand.Read(buf[:]); err != nil {
		// A time-based fallback is still unique enough to satisfy the key; the
		// alternative is failing a request for a non-security reason.
		return "gen-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "gen-" + hex.EncodeToString(buf[:])
}

func truncateForColumn(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

// stepBelongsToSheet resolves a step within the tenant and returns its current
// image, which AI_ENHANCE needs as its input.
func (s *Server) stepBelongsToSheet(ctx context.Context, restaurantID int, sheetID, stepID int64) (imageURL string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(image_url,'') FROM stock_recipe_steps
		  WHERE restaurant_id=? AND id=? AND recipe_id=?`,
		restaurantID, stepID, sheetID).Scan(&imageURL)
	return imageURL, err
}

func (s *Server) handleBOTechnicalSheetStepImageJobCreate(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sheetID := sheetIDParam(r)
	stepID, _ := strconv.ParseInt(chi.URLParam(r, "stepId"), 10, 64)

	var in struct {
		Mode           string `json:"mode"`
		Prompt         string `json:"prompt"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos invalidos")
		return
	}
	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	if mode != "AI_ENHANCE" && mode != "AI_GENERATE" {
		httpx.WriteError(w, http.StatusBadRequest, "Modo de imagen invalido")
		return
	}
	prompt := strings.TrimSpace(in.Prompt)

	currentImage, err := s.stepBelongsToSheet(r.Context(), a.ActiveRestaurantID, sheetID, stepID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "Paso no encontrado")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando el paso")
		return
	}

	if mode == "AI_GENERATE" && prompt == "" {
		// There is no source image, so the prompt is the only description of
		// what to draw.
		httpx.WriteError(w, http.StatusBadRequest, "Describe la imagen que quieres generar")
		return
	}
	if mode == "AI_ENHANCE" && currentImage == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Este paso no tiene imagen que mejorar")
		return
	}

	// The idempotency key is unique per tenant and NOT NULL, so a request that
	// does not supply one gets a generated key. A double click that DOES supply
	// one reuses the queued job instead of paying the provider twice.
	idempotencyKey := strings.TrimSpace(in.IdempotencyKey)
	if idempotencyKey != "" {
		var existingID int64
		err := s.db.QueryRowContext(r.Context(),
			`SELECT id FROM stock_recipe_step_image_jobs
			  WHERE restaurant_id=? AND idempotency_key=?`,
			a.ActiveRestaurantID, idempotencyKey).Scan(&existingID)
		if err == nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": true, "jobId": existingID, "reused": true,
			})
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusInternalServerError, "Error comprobando el trabajo")
			return
		}
	} else {
		idempotencyKey = generatedIdempotencyKey()
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando el trabajo")
		return
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(r.Context(), `
		INSERT INTO stock_recipe_step_image_jobs
			(restaurant_id,step_id,mode,status,prompt,idempotency_key,actor_user_id)
		VALUES (?,?,?,'PENDING',?,?,?)`,
		a.ActiveRestaurantID, stepID, mode, prompt, idempotencyKey, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "No se pudo crear el trabajo de imagen")
		return
	}
	jobID, _ := res.LastInsertId()

	// The step carries the status too, so the editor can show progress without
	// having to know about the job table.
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE stock_recipe_steps
		   SET generation_status='PENDING', generation_mode=?, generation_error=NULL
		 WHERE restaurant_id=? AND id=?`,
		mode, a.ActiveRestaurantID, stepID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando el paso")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando el trabajo")
		return
	}
	s.sheetHub.broadcastImageJob(a.ActiveRestaurantID, map[string]any{
		"jobId": jobID, "stepId": stepID, "status": "PENDING", "mode": mode,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "jobId": jobID})
}

// handleBOTechnicalSheetStepImageJobGet lets the editor recover job state after
// a reload or a dropped WebSocket. REST stays the source of truth.
func (s *Server) handleBOTechnicalSheetStepImageJobGet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	stepID, _ := strconv.ParseInt(chi.URLParam(r, "stepId"), 10, 64)

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, mode, status, COALESCE(error_message,''), COALESCE(result_object_path,''), created_at
		  FROM stock_recipe_step_image_jobs
		 WHERE restaurant_id=? AND step_id=? ORDER BY id DESC LIMIT 10`,
		a.ActiveRestaurantID, stepID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error cargando trabajos")
		return
	}
	defer rows.Close()
	jobs := []map[string]any{}
	for rows.Next() {
		var id int64
		var mode, status, errMessage, objectPath, createdAt string
		if err := rows.Scan(&id, &mode, &status, &errMessage, &objectPath, &createdAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo trabajos")
			return
		}
		jobs = append(jobs, map[string]any{
			"id": id, "mode": mode, "status": status,
			"errorMessage": errMessage, "resultObjectPath": objectPath, "createdAt": createdAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "jobs": jobs})
}
