package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/lib/specialmenuimage"
)

// The worker drains queued step-image jobs. It runs outside the request cycle
// because a provider call can take tens of seconds: doing it inline would hold
// an HTTP connection open and leave the user staring at a spinner with no way
// to leave the page.

const (
	// stepImageJobTimeout bounds one provider call so a hung request cannot
	// occupy the worker forever.
	stepImageJobTimeout = 4 * time.Minute
	// stepImageStuckAfter reclaims jobs whose worker died mid-flight; without
	// this they would sit in RUNNING and the step would show progress forever.
	stepImageStuckAfter = 15 * time.Minute
)

// StartStepImageWorker begins polling for queued jobs. It is intentionally a
// simple poller: a queue broker would be premature until measurement shows the
// scheduler is not enough.
func (s *Server) StartStepImageWorker(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reclaimStuckStepImageJobs(ctx)
				for s.processNextStepImageJob(ctx) {
					// Keep draining while there is work; the ticker only wakes
					// the loop up.
				}
			}
		}
	}()
}

// reclaimStuckStepImageJobs fails jobs abandoned by a crashed worker so the UI
// stops showing them as in progress.
func (s *Server) reclaimStuckStepImageJobs(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, restaurant_id, step_id FROM stock_recipe_step_image_jobs
		 WHERE status='RUNNING' AND started_at < DATE_SUB(NOW(), INTERVAL ? MINUTE)`,
		int(stepImageStuckAfter.Minutes()))
	if err != nil {
		return
	}
	type stuck struct {
		jobID, stepID int64
		restaurantID  int
	}
	var jobs []stuck
	for rows.Next() {
		var job stuck
		if err := rows.Scan(&job.jobID, &job.restaurantID, &job.stepID); err == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	for _, job := range jobs {
		s.failStepImageJob(ctx, job.restaurantID, job.jobID, job.stepID,
			"la generacion se interrumpio; vuelve a intentarlo")
	}
}

// processNextStepImageJob claims and runs one job. It returns true when it did
// work, so the caller can keep draining.
func (s *Server) processNextStepImageJob(ctx context.Context) bool {
	var jobID, stepID int64
	var restaurantID int
	var mode, prompt string

	// Claiming with a conditional UPDATE means two workers can never take the
	// same job, without needing a separate lock.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	err = tx.QueryRowContext(ctx, `
		SELECT id, restaurant_id, step_id, mode, COALESCE(prompt,'')
		  FROM stock_recipe_step_image_jobs
		 WHERE status='PENDING' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&jobID, &restaurantID, &stepID, &mode, &prompt)
	if err != nil {
		tx.Rollback()
		return false
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE stock_recipe_step_image_jobs SET status='RUNNING', started_at=NOW() WHERE id=?`,
		jobID); err != nil {
		tx.Rollback()
		return false
	}
	if err := tx.Commit(); err != nil {
		return false
	}

	jobCtx, cancel := context.WithTimeout(ctx, stepImageJobTimeout)
	defer cancel()

	if err := s.runStepImageJob(jobCtx, restaurantID, jobID, stepID, mode, prompt); err != nil {
		// The reason is stored on the step so the user can decide what to do;
		// the log keeps the detail for operators.
		log.Printf("step image job %d failed: %v", jobID, err)
		s.failStepImageJob(ctx, restaurantID, jobID, stepID, userFacingImageError(err))
	}
	return true
}

// userFacingImageError keeps provider internals out of the UI while still
// telling the user something actionable.
func userFacingImageError(err error) string {
	message := err.Error()
	if len(message) > 200 {
		message = message[:200]
	}
	return "No se pudo generar la imagen: " + message
}

func (s *Server) runStepImageJob(ctx context.Context, restaurantID int, jobID, stepID int64, mode, prompt string) error {
	provider := s.resolveAIImageProvider(ctx, restaurantID)
	if strings.TrimSpace(provider.APIKey) == "" {
		return errors.New("la IA de imagenes no esta configurada")
	}
	if !s.bunnyConfigured(ctx, restaurantID) {
		return errors.New("el almacenamiento de imagenes no esta configurado")
	}

	// AI_ENHANCE edits the existing picture; AI_GENERATE draws from the prompt
	// alone. They use different models, so the slug is chosen per mode.
	modelSlug := provider.I2IModelSlug
	var sourceImage []byte
	if mode == "AI_GENERATE" {
		modelSlug = provider.T2IModelSlug
	} else {
		currentURL, err := s.stepImageURL(ctx, restaurantID, stepID)
		if err != nil {
			return err
		}
		if sourceImage, err = downloadImage(ctx, currentURL); err != nil {
			return err
		}
	}

	editURL := aiImageEditURLForModel(provider.BaseURL, modelSlug)
	if editURL == "" {
		return errors.New("el modelo de imagen no esta configurado")
	}

	generated, err := s.callComidaImageEdit(ctx, editURL, provider.APIKey, prompt, sourceImage, "image/webp")
	if err != nil {
		return err
	}

	// Same pipeline as every other image: normalise before it reaches the CDN,
	// so one provider's odd output cannot become a 4 MB PNG on the menu.
	normalized, err := specialmenuimage.NormalizeToWebP(ctx, generated, "step.webp", "image/webp")
	if err != nil {
		return err
	}

	objectPath := fmt.Sprintf("recipes/steps/%d/%d-%d.webp", restaurantID, stepID, time.Now().UnixNano())
	if err := s.bunnyPut(ctx, restaurantID, objectPath, normalized, "image/webp"); err != nil {
		return err
	}

	// completeStepImageJob refuses to publish if the job was cancelled while
	// the provider was working.
	applied, err := s.completeStepImageJob(ctx, restaurantID, jobID, stepID, objectPath, s.bunnyPullURL(ctx, restaurantID, objectPath))
	if err != nil {
		return err
	}
	if !applied {
		log.Printf("step image job %d finished but was no longer active; result discarded", jobID)
	}
	return nil
}

func (s *Server) stepImageURL(ctx context.Context, restaurantID int, stepID int64) (string, error) {
	var imageURL string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(image_url,'') FROM stock_recipe_steps WHERE restaurant_id=? AND id=?`,
		restaurantID, stepID).Scan(&imageURL); err != nil {
		return "", err
	}
	if strings.TrimSpace(imageURL) == "" {
		return "", errors.New("el paso ya no tiene imagen que mejorar")
	}
	return imageURL, nil
}

func downloadImage(ctx context.Context, imageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("no se pudo descargar la imagen actual (%d)", res.StatusCode)
	}
	// The cap matches the upload limit: an unbounded read here would let a
	// bad URL exhaust memory.
	return io.ReadAll(io.LimitReader(res.Body, 25<<20))
}
