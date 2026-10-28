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

func editorialRiceFixture() []publicMenuItem {
	supplement := 3.0
	return []publicMenuItem{
		{ID: 41, Slug: "menu-del-dia-41", MenuTitle: "Menú del día", MenuTitleEnglish: "Menu of the day", Price: "22.00", Active: true, MenuPublicActive: true, publicMenuSEO: publicMenuSEO{SEOSlug: "menu-del-dia"}, Sections: []publicMenuSection{{ID: 1, Title: "Arroces", DisplayTitle: "Arroces", Kind: "arroces", Dishes: []publicMenuDish{{ID: 11, Title: "Arroz a banda", TitleEnglish: "Arroz a banda", Description: "Con caldo de pescado", DescriptionEnglish: "With fish stock", DescriptionEnabled: true}}}}},
		{ID: 42, Slug: "menu-fin-de-semana-42", MenuTitle: "Menú fin de semana", Price: "30.00", Active: true, MenuPublicActive: true, Sections: []publicMenuSection{{ID: 2, Title: "Arroces", DisplayTitle: "Arroces", Kind: "arroces", Dishes: []publicMenuDish{{ID: 12, Title: "Arroz del senyoret", SupplementEnabled: true, SupplementPrice: &supplement}}}}},
		{ID: 1, Slug: "menu-arrocero-1", MenuTitle: "Menú arrocero", Price: "35.00", Active: true, MenuPublicActive: true, Sections: []publicMenuSection{{ID: 3, Title: "Arroz a elegir", DisplayTitle: "Arroz a elegir", Kind: "principales", Dishes: []publicMenuDish{{ID: 13, Title: "Meloso de pulpo"}}}}},
	}
}

func editorialRiceStatic(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, route := range []string{"arroces-y-paellas", "en/arroces-y-paellas", "guia-arroces-valencianos", "en/guia-arroces-valencianos"} {
		path := filepath.Join(dir, filepath.FromSlash(route))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `<html><body><div id="app"><div class="editorial-menu-marker"><!--vc-active-rice-menus:start--></div><div class="editorial-menu-marker"><!--vc-active-rice-menus:end--></div></div><script>window.__VILLACARMEN_DATA__={"menus":{"stale":{"id":99}},"redirects":{}}</script></body></html>`
		if err := os.WriteFile(filepath.Join(path, "index.html"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEditorialRiceSSRActiveInactiveEnglishAndCache(t *testing.T) {
	menus := editorialRiceFixture()
	var loadErr error
	h := newEditorialRiceSSR(editorialRiceStatic(t), http.NotFoundHandler(), func(context.Context) ([]publicMenuItem, error) { return menus, loadErr }).(*editorialRiceSSR)
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

	response, spanish := request("/arroces-y-paellas")
	for _, expected := range []string{"Arroz a banda", "Con caldo de pescado", "Arroz del senyoret", "Meloso de pulpo", "Suplemento +3 €", `"menus":{"1"`} {
		if !strings.Contains(spanish, expected) {
			t.Errorf("Spanish HTML missing %q", expected)
		}
	}
	if response.Header().Get("X-Villacarmen-Rice-Cache") != "MISS" || response.Header().Get("Cache-Control") != "public, max-age=60, stale-while-revalidate=300" {
		t.Fatalf("unexpected cache headers: %#v", response.Header())
	}
	if strings.Contains(spanish, `"stale"`) {
		t.Error("stale bootstrap menu was not replaced")
	}

	_, cached := request("/arroces-y-paellas")
	if !strings.Contains(cached, "Arroz a banda") {
		t.Error("cache did not preserve SSR content")
	}
	_, english := request("/en/guia-arroces-valencianos")
	for _, expected := range []string{"Rice dishes from our menus", "With fish stock", "Supplement +3 €"} {
		if !strings.Contains(english, expected) {
			t.Errorf("English HTML missing %q", expected)
		}
	}

	menus[1].Active = false
	now = now.Add(61 * time.Second)
	_, deactivated := request("/arroces-y-paellas")
	if strings.Contains(deactivated, "Arroz del senyoret") || strings.Contains(deactivated, `"42"`) {
		t.Error("deactivated menu remained in HTML or bootstrap")
	}
	if !strings.Contains(deactivated, "Arroz a banda") || !strings.Contains(deactivated, "Meloso de pulpo") {
		t.Error("active menus disappeared with inactive sibling")
	}

	loadErr = errors.New("test database unavailable")
	now = now.Add(61 * time.Second)
	staleResponse, stale := request("/arroces-y-paellas")
	if staleResponse.Header().Get("X-Villacarmen-Rice-Cache") != "STALE" || !strings.Contains(stale, "Arroz a banda") {
		t.Error("last good document was not served stale")
	}
}

func TestEditorialRiceSSRHeadAndUnrelatedRoute(t *testing.T) {
	h := newEditorialRiceSSR(editorialRiceStatic(t), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }), func(context.Context) ([]publicMenuItem, error) { return editorialRiceFixture(), nil })
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/arroces-y-paellas", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD returned %d with %d body bytes", head.Code, head.Body.Len())
	}
	other := httptest.NewRecorder()
	h.ServeHTTP(other, httptest.NewRequest(http.MethodGet, "/eventos", nil))
	if other.Code != http.StatusTeapot {
		t.Fatalf("unrelated route returned %d", other.Code)
	}
}
