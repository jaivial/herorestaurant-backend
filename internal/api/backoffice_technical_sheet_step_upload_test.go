package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// pngBytes builds a real, decodable image. A hand-written byte slice would be
// rejected by the WebP normaliser for the wrong reason and hide the behaviour
// under test.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stepUploadReq builds a real multipart request; the boundary has to travel
// with the body, so both are produced together.
func stepUploadReq(t *testing.T, sheetID, stepID string, field string, payload []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, "step.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	req := sheetReq("POST", "/x", body.String(), map[string]string{"id": sheetID, "stepId": stepID})
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// A step image must belong to the tenant's own sheet. Accepting an arbitrary
// step id would let one restaurant overwrite another's recipe photo.
func TestStepImageUploadRejectsAForeignStep(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	addStep(t, s, sheetID, `{"title":"Sofreir"}`)

	req := stepUploadReq(t, strconv.FormatInt(sheetID, 10), "999999", "image", pngBytes(t))
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepImageUpload(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status %d want 404, body %s", rec.Code, rec.Body.String())
	}
}

// Without a configured CDN the handler must fail loudly rather than record a
// URL that resolves to nothing.
func TestStepImageUploadStoresTheImageOrFailsLoudly(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	stepID := addStep(t, s, sheetID, `{"title":"Sofreir"}`)

	req := stepUploadReq(t, strconv.FormatInt(sheetID, 10), strconv.FormatInt(stepID, 10), "image", pngBytes(t))
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepImageUpload(rec, req)

	if !s.bunnyConfiguredContext(req.Context()) {
		if rec.Code != 503 {
			t.Fatalf("status %d want 503, body %s", rec.Code, rec.Body.String())
		}
		return
	}
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ImageURL string `json:"imageUrl"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ImageURL == "" {
		t.Fatal("expected an image URL in the response")
	}
	// The URL must be persisted, or a reload would lose the upload.
	var stored string
	if err := s.db.QueryRow(
		`SELECT COALESCE(image_url,'') FROM stock_recipe_steps WHERE restaurant_id=1 AND id=?`,
		stepID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != out.ImageURL {
		t.Fatalf("stored %q want %q", stored, out.ImageURL)
	}
}

// A request with no file at all is a client error, not a 500.
func TestStepImageUploadRequiresAFile(t *testing.T) {
	s := sheetsTestServer(t)
	sheetID := createSheet(t, s, "Receta")
	stepID := addStep(t, s, sheetID, `{"title":"Sofreir"}`)

	req := stepUploadReq(t, strconv.FormatInt(sheetID, 10), strconv.FormatInt(stepID, 10), "wrongfield", pngBytes(t))
	rec := httptest.NewRecorder()
	s.handleBOTechnicalSheetStepImageUpload(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status %d want 400, body %s", rec.Code, rec.Body.String())
	}
}
