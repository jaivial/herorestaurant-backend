package api

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func createStep(t *testing.T, s *Server, sheetID int64) int64 {
	t.Helper()
	return addStep(t, s, sheetID, `{"title":"Paso","description":"Texto"}`)
}

func requestImageJob(t *testing.T, s *Server, sheetID, stepID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepImageJobCreate(rec, sheetReq("POST", "/x", body, map[string]string{
		"id": strconv.FormatInt(sheetID, 10), "stepId": strconv.FormatInt(stepID, 10),
	}))
	return rec
}

// A queued job must be visible immediately: the user has to see "generating"
// rather than a silent screen while the provider works.
func TestImageJobIsQueuedAndVisibleOnTheStep(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Con imagen")
	stepID := createStep(t, s, sheetID)

	rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"AI_GENERATE","prompt":"paella en sarten"}`)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		JobID int64 `json:"jobId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.JobID == 0 {
		t.Fatal("no job id returned")
	}

	var status, mode string
	s.db.QueryRow(`SELECT status,mode FROM stock_recipe_step_image_jobs WHERE restaurant_id=1 AND id=?`, out.JobID).
		Scan(&status, &mode)
	if status != "PENDING" || mode != "AI_GENERATE" {
		t.Fatalf("job status=%q mode=%q want PENDING/AI_GENERATE", status, mode)
	}

	var stepStatus string
	s.db.QueryRow(`SELECT generation_status FROM stock_recipe_steps WHERE restaurant_id=1 AND id=?`, stepID).Scan(&stepStatus)
	if stepStatus != "PENDING" {
		t.Fatalf("step generation_status=%q want PENDING so the UI can show progress", stepStatus)
	}
}

// Double-clicking "generate" must not bill the provider twice.
func TestRepeatedRequestWithSameIdempotencyKeyReusesTheJob(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Idempotente")
	stepID := createStep(t, s, sheetID)
	body := `{"mode":"AI_GENERATE","prompt":"paella","idempotencyKey":"abc-123"}`

	first := requestImageJob(t, s, sheetID, stepID, body)
	second := requestImageJob(t, s, sheetID, stepID, body)
	if first.Code != 200 || second.Code != 200 {
		t.Fatalf("statuses %d/%d", first.Code, second.Code)
	}
	var a, b struct {
		JobID int64 `json:"jobId"`
	}
	json.Unmarshal(first.Body.Bytes(), &a)
	json.Unmarshal(second.Body.Bytes(), &b)
	if a.JobID != b.JobID {
		t.Fatalf("job ids %d and %d differ; the provider would be charged twice", a.JobID, b.JobID)
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_step_image_jobs WHERE restaurant_id=1 AND step_id=?`, stepID).Scan(&count)
	if count != 1 {
		t.Fatalf("%d jobs created, want 1", count)
	}
}

// AI_GENERATE has no source image, so the prompt is the only description of
// what to draw. Without it the provider would be called with nothing.
func TestGenerateRequiresAPrompt(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Sin prompt")
	stepID := createStep(t, s, sheetID)

	if rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"AI_GENERATE","prompt":"  "}`); rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

func TestImageJobRejectsAnUnknownMode(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Modo raro")
	stepID := createStep(t, s, sheetID)

	if rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"TELEPATHY","prompt":"x"}`); rec.Code != 400 {
		t.Fatalf("status %d want 400", rec.Code)
	}
}

// Enhancing requires something to enhance.
func TestEnhanceRequiresAnExistingImage(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Sin imagen")
	stepID := createStep(t, s, sheetID)

	if rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"AI_ENHANCE","prompt":"mejorar"}`); rec.Code != 400 {
		t.Fatalf("status %d want 400 when the step has no image to enhance", rec.Code)
	}
}

func TestImageJobIsTenantScoped(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Ajena")
	stepID := createStep(t, s, sheetID)

	req := sheetReq("POST", "/x", `{"mode":"AI_GENERATE","prompt":"x"}`, map[string]string{
		"id": strconv.FormatInt(sheetID, 10), "stepId": strconv.FormatInt(stepID, 10)})
	req = req.WithContext(withBOAuth(req.Context(), boAuth{ActiveRestaurantID: 999, Role: "admin", User: boUser{ID: 7}}))

	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepImageJobCreate(rec, req)
	if rec.Code == 200 {
		t.Fatal("a foreign tenant must not queue jobs on this step")
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM stock_recipe_step_image_jobs WHERE step_id=?`, stepID).Scan(&count)
	if count != 0 {
		t.Fatalf("%d jobs leaked", count)
	}
}

// A failed job must explain itself and must not leave the step stuck showing a
// spinner forever.
func TestFailingAJobRecordsTheReasonOnTheStep(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Fallo")
	stepID := createStep(t, s, sheetID)
	rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"AI_GENERATE","prompt":"algo"}`)
	var out struct {
		JobID int64 `json:"jobId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)

	if err := s.failStepImageJob(t.Context(), 1, out.JobID, stepID, "el proveedor no respondio"); err != nil {
		t.Fatal(err)
	}

	var jobStatus, stepStatus, stepError string
	s.db.QueryRow(`SELECT status FROM stock_recipe_step_image_jobs WHERE id=?`, out.JobID).Scan(&jobStatus)
	s.db.QueryRow(`SELECT generation_status,COALESCE(generation_error,'') FROM stock_recipe_steps WHERE id=?`, stepID).
		Scan(&stepStatus, &stepError)
	if jobStatus != "FAILED" || stepStatus != "FAILED" {
		t.Fatalf("job=%q step=%q want FAILED/FAILED", jobStatus, stepStatus)
	}
	if stepError == "" {
		t.Fatal("a failure with no reason leaves the user unable to act")
	}
}

// A cancelled job must never later overwrite the step's image.
func TestCancelledJobDoesNotPublishItsResult(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Cancelada")
	stepID := createStep(t, s, sheetID)
	rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"AI_GENERATE","prompt":"algo"}`)
	var out struct {
		JobID int64 `json:"jobId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)

	if _, err := s.db.Exec(`UPDATE stock_recipe_step_image_jobs SET status='CANCELLED' WHERE id=?`, out.JobID); err != nil {
		t.Fatal(err)
	}
	applied, err := s.completeStepImageJob(t.Context(), 1, out.JobID, stepID, "steps/late.webp", "https://cdn/steps/late.webp")
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("a cancelled job must not publish its image")
	}
	var imageURL string
	s.db.QueryRow(`SELECT COALESCE(image_url,'') FROM stock_recipe_steps WHERE id=?`, stepID).Scan(&imageURL)
	if imageURL != "" {
		t.Fatalf("step image was overwritten by a cancelled job: %q", imageURL)
	}
}

func TestSucceedingAJobPublishesTheImage(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Exito")
	stepID := createStep(t, s, sheetID)
	rec := requestImageJob(t, s, sheetID, stepID, `{"mode":"AI_GENERATE","prompt":"algo"}`)
	var out struct {
		JobID int64 `json:"jobId"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)

	applied, err := s.completeStepImageJob(t.Context(), 1, out.JobID, stepID, "steps/ok.webp", "https://cdn/steps/ok.webp")
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("a pending job must publish its result")
	}
	var status, imageURL string
	s.db.QueryRow(`SELECT generation_status,COALESCE(image_url,'') FROM stock_recipe_steps WHERE id=?`, stepID).
		Scan(&status, &imageURL)
	if status != "READY" || imageURL != "https://cdn/steps/ok.webp" {
		t.Fatalf("status=%q url=%q", status, imageURL)
	}
}
