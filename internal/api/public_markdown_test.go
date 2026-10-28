package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publicMarkdownStatic(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, path := range []string{"/arroces-y-paellas", "/en/arroces-y-paellas"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\ntitle: Arroces\ncanonical: https://www.alqueriavillacarmen.com" + path + "\nlastmod: 2026-10-01\nrobots: noindex, follow\n---\n# Arroces\n" + activeRiceMarkdownStart + "\ncontenido antiguo\n" + activeRiceMarkdownEnd + "\n"
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path), "index.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "eventos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "eventos", "index.md"), []byte("# Eventos"), 0o644); err != nil {
		t.Fatal(err)
	}
	sitemap := `<?xml version="1.0"?><urlset><url><loc>https://www.alqueriavillacarmen.com/arroces-y-paellas</loc><lastmod>2026-10-01</lastmod></url><url><loc>https://www.alqueriavillacarmen.com/menu/menu-fin-de-semana</loc><lastmod>2026-10-01</lastmod></url></urlset>`
	if err := os.WriteFile(filepath.Join(dir, "sitemap.xml"), []byte(sitemap), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPublicMarkdownLiveRiceHeadersCacheAndEnglish(t *testing.T) {
	menus := editorialRiceFixture()
	for i := range menus {
		menus[i].ModifiedAt = "2026-10-28T12:30:00Z"
	}
	var loadErr error
	h := newPublicMarkdownHandler(publicMarkdownStatic(t), http.NotFoundHandler(), func(context.Context) ([]publicMenuItem, error) { return menus, loadErr }).(*publicMarkdownHandler)
	now := time.Unix(1000, 0)
	h.now = func() time.Time { return now }
	request := func(path string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, recorder.Code)
		}
		return recorder, recorder.Body.String()
	}

	response, spanish := request("/arroces-y-paellas/index.md")
	for _, expected := range []string{"## Nuestros arroces", "- Arroz del senyoret", "- Arroz negro", "Con caldo de pescado", "Suplemento +3€", "+5€", "[Menú del día →](https://www.alqueriavillacarmen.com/menu/menu-del-dia)", "[Menú fin de semana →](https://www.alqueriavillacarmen.com/menu/menu-fin-de-semana)", "[Menú arrocero →](https://www.alqueriavillacarmen.com/menu/menu-arrocero)", "lastmod: 2026-10-28", activeRiceMarkdownStart, activeRiceMarkdownEnd} {
		if !strings.Contains(spanish, expected) {
			t.Errorf("Spanish Markdown missing %q\n%s", expected, spanish)
		}
	}
	if strings.Count(spanish, "Arroz del senyoret") != 1 || strings.Contains(spanish, "Arroz a banda") || strings.Contains(spanish, "Meloso de pulpo") || strings.Contains(spanish, "contenido antiguo") {
		t.Error("live Markdown did not deduplicate and restrict dishes to the active weekend menu")
	}
	if response.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || response.Header().Get("X-Robots-Tag") != "noindex, follow" || response.Header().Get("X-Villacarmen-Markdown-Cache") != "MISS" {
		t.Fatalf("unexpected Markdown headers: %#v", response.Header())
	}
	_, cached := request("/arroces-y-paellas/index.md")
	if !strings.Contains(cached, "Arroz negro") {
		t.Error("cached Markdown lost dishes")
	}
	_, english := request("/en/arroces-y-paellas/index.md")
	for _, expected := range []string{"## Our rice dishes", "With fish stock", "https://www.alqueriavillacarmen.com/en/menu/menu-arrocero"} {
		if !strings.Contains(english, expected) {
			t.Errorf("English Markdown missing %q", expected)
		}
	}
	staticResponse, staticBody := request("/eventos/index.md")
	if staticBody != "# Eventos" || staticResponse.Header().Get("X-Robots-Tag") != "noindex, follow" || staticResponse.Header().Get("X-Villacarmen-Markdown-Cache") != "" {
		t.Error("static Markdown was not served with the expected headers")
	}
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/eventos/index.md", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("Markdown HEAD returned %d with %d bytes", head.Code, head.Body.Len())
	}

	menus[1].Active = false
	now = now.Add(61 * time.Second)
	_, inactive := request("/arroces-y-paellas/index.md")
	if strings.Contains(inactive, "Arroz del senyoret") || !strings.Contains(inactive, "menu-del-dia") || !strings.Contains(inactive, "menu-arrocero") {
		t.Error("deactivated weekend menu remained or active backlinks disappeared")
	}
	loadErr = errors.New("database unavailable")
	now = now.Add(61 * time.Second)
	staleResponse, stale := request("/arroces-y-paellas/index.md")
	if staleResponse.Header().Get("X-Villacarmen-Markdown-Cache") != "STALE" || !strings.Contains(stale, "menu-del-dia") || staleResponse.Header().Get("Warning") == "" {
		t.Error("last good Markdown was not served stale with a warning")
	}
}

func TestPublicMarkdownPreservesNewerEditorialDateAndFallsBack(t *testing.T) {
	menus := editorialRiceFixture()
	for i := range menus {
		menus[i].ModifiedAt = "2026-09-01T12:30:00Z"
	}
	h := newPublicMarkdownHandler(publicMarkdownStatic(t), http.NotFoundHandler(), func(context.Context) ([]publicMenuItem, error) { return menus, nil }).(*publicMarkdownHandler)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/arroces-y-paellas/index.md", nil))
	if !strings.Contains(recorder.Body.String(), "lastmod: 2026-10-01") {
		t.Error("an older menu date regressed the editorial lastmod")
	}
	h.now = func() time.Time { return time.Unix(1000, 0).Add(2 * time.Minute) }
	h.load = func(context.Context) ([]publicMenuItem, error) { return nil, errors.New("database unavailable") }
	h.cache = make(map[string]publicMarkdownCacheEntry)
	fallback := httptest.NewRecorder()
	h.ServeHTTP(fallback, httptest.NewRequest(http.MethodGet, "/arroces-y-paellas/index.md", nil))
	if fallback.Code != http.StatusOK || fallback.Header().Get("X-Villacarmen-Markdown-Cache") != "FALLBACK" || fallback.Header().Get("Warning") == "" {
		t.Fatalf("build-time Markdown fallback returned %d and headers %#v", fallback.Code, fallback.Header())
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/reservas/modificar/index.md", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("private booking workflow returned %d", missing.Code)
	}
	for _, path := range []string{"/backoffice/index.md", "/menu/menu-inexistente/index.md"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s returned %d with Content-Type %q", path, recorder.Code, recorder.Header().Get("Content-Type"))
		}
	}
}

func TestPublicMarkdownSitemapRefreshesOnlyMenuLastmod(t *testing.T) {
	menus := editorialRiceFixture()
	menus[1].ModifiedAt = "2026-10-28T12:30:00Z"
	h := newPublicMarkdownHandler(publicMarkdownStatic(t), http.NotFoundHandler(), func(context.Context) ([]publicMenuItem, error) { return menus, nil })
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, "/menu/menu-fin-de-semana</loc><lastmod>2026-10-28</lastmod>") || !strings.Contains(body, "/arroces-y-paellas</loc><lastmod>2026-10-28</lastmod>") {
		t.Fatalf("unexpected sitemap lastmod values: %s", body)
	}
	menus[1].ModifiedAt = "2026-09-01T12:30:00Z"
	older := httptest.NewRecorder()
	h.ServeHTTP(older, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if !strings.Contains(older.Body.String(), "/menu/menu-fin-de-semana</loc><lastmod>2026-10-01</lastmod>") {
		t.Error("an older menu timestamp regressed sitemap lastmod")
	}
}
