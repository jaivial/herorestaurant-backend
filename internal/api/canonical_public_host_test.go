package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanonicalPublicHostRedirectsOnceToFinalURL(t *testing.T) {
	manifest := &seoRoutesManifest{
		Redirects: map[string]string{
			"/menudeldia.php":    "/menu/41/menu-del-dia-41",
			"/menuNavidad.php":   "/menu/50/menu-navidad-50",
			"/menusdegrupos.php": "/menusdegrupos",
		},
		MenuPaths: map[string]string{"42": "/menu/menu-fin-de-semana"},
		GonePaths: []string{"/confpostres.php"},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/confpostres.php" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := canonicalPublicHost(manifest)(next)

	for _, test := range []struct {
		name, method, host, proto, path, wantLocation string
		wantStatus                                    int
	}{
		{name: "bare HTTPS legacy path", host: "alqueriavillacarmen.com", proto: "https", path: "/menudeldia.php?origen=seo", wantLocation: "https://www.alqueriavillacarmen.com/menu/41/menu-del-dia-41?origen=seo", wantStatus: http.StatusMovedPermanently},
		{name: "case and port", host: "ALQUERIAVILLACARMEN.COM:443", proto: "https", path: "/en/menuNavidad.php", wantLocation: "https://www.alqueriavillacarmen.com/en/menu/50/menu-navidad-50", wantStatus: http.StatusMovedPermanently},
		{name: "HTTP www legacy path", host: "www.alqueriavillacarmen.com", proto: "http", path: "/menusdegrupos.php", wantLocation: "https://www.alqueriavillacarmen.com/menusdegrupos", wantStatus: http.StatusMovedPermanently},
		{name: "bare host menu slug", host: "alqueriavillacarmen.com", proto: "https", path: "/menu/42/slug-antiguo", wantLocation: "https://www.alqueriavillacarmen.com/menu/menu-fin-de-semana", wantStatus: http.StatusMovedPermanently},
		{name: "bare HTTPS retired page reaches SPA", host: "alqueriavillacarmen.com", proto: "https", path: "/confpostres.php", wantStatus: http.StatusGone},
		{name: "HTTP www retired page reaches SPA", host: "www.alqueriavillacarmen.com", proto: "http", path: "/confpostres.php", wantStatus: http.StatusGone},
		{name: "unknown path and query preserved", host: "alqueriavillacarmen.com", proto: "https", path: "/bodas?a=1", wantLocation: "https://www.alqueriavillacarmen.com/bodas?a=1", wantStatus: http.StatusMovedPermanently},
		{name: "canonical HTTPS host passes", host: "www.alqueriavillacarmen.com", proto: "https", path: "/menudeldia.php", wantStatus: http.StatusNoContent},
		{name: "other host passes", host: "backoffice.alqueriavillacarmen.com", proto: "http", path: "/", wantStatus: http.StatusNoContent},
		{name: "POST passes without redirect", method: http.MethodPost, host: "alqueriavillacarmen.com", proto: "https", path: "/insert_booking.php", wantStatus: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "http://backend"+test.path, nil)
			req.Host = test.host
			req.Header.Set("X-Forwarded-Host", test.host)
			req.Header.Set("X-Forwarded-Proto", test.proto)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", res.Code, test.wantStatus)
			}
			if got := res.Header().Get("Location"); got != test.wantLocation {
				t.Fatalf("Location = %q, want %q", got, test.wantLocation)
			}
		})
	}
}
