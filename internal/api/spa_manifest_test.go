package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAHandlerServesWebManifestMIME(t *testing.T) {
	dir := t.TempDir()
	manifest := []byte(`{"name":"Villa Carmen"}`)
	if err := os.WriteFile(filepath.Join(dir, "site.webmanifest"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	SPAHandler(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/site.webmanifest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/manifest+json"; got != want {
		t.Fatalf("expected manifest MIME %q, got %q", want, got)
	}
	if rec.Body.String() != string(manifest) {
		t.Fatal("manifest content changed")
	}
}
