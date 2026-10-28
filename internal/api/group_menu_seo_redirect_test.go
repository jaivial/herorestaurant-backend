package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSEOGroupMenuTargetUsesOnlyActiveGroupMenus(t *testing.T) {
	manifest := &seoRoutesManifest{GroupMenuPaths: map[string]string{"1": "/menu/menu-arrocero"}}
	for _, test := range []struct {
		path, rawQuery, want string
		found                bool
	}{
		{path: "/menusdegrupos", rawQuery: "menu=1", want: "/menu/menu-arrocero", found: true},
		{path: "/en/menusdegrupos/", rawQuery: "menu=1&utm_source=seo", want: "/en/menu/menu-arrocero?utm_source=seo", found: true},
		{path: "/menusdegrupos", rawQuery: "menu=999", found: false},
		{path: "/menusdegrupos", rawQuery: "utm_source=seo", found: false},
		{path: "/menu/1", rawQuery: "menu=1", found: false},
	} {
		got, found := seoGroupMenuTarget(manifest, test.path, url.Values{})
		if test.rawQuery != "" {
			query, err := url.ParseQuery(test.rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			got, found = seoGroupMenuTarget(manifest, test.path, query)
		}
		if got != test.want || found != test.found {
			t.Errorf("seoGroupMenuTarget(%q, %q) = %q, %v; want %q, %v", test.path, test.rawQuery, got, found, test.want, test.found)
		}
	}
}

func TestCanonicalGroupMenuQueryRedirectsOnce(t *testing.T) {
	manifest := &seoRoutesManifest{GroupMenuPaths: map[string]string{"1": "/menu/menu-arrocero"}}
	handler := canonicalPublicHost(manifest)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, host := range []string{"www.alqueriavillacarmen.com", "alqueriavillacarmen.com"} {
		req := httptest.NewRequest(http.MethodGet, "http://backend/menusdegrupos?menu=1&utm_source=seo", nil)
		req.Host = host
		req.Header.Set("X-Forwarded-Host", host)
		req.Header.Set("X-Forwarded-Proto", "https")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusMovedPermanently || res.Header().Get("Location") != "https://www.alqueriavillacarmen.com/menu/menu-arrocero?utm_source=seo" {
			t.Fatalf("host %s: status=%d location=%q", host, res.Code, res.Header().Get("Location"))
		}
	}
}
