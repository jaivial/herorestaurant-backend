package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"preactvillacarmen/internal/config"
)

func TestSPANavigationSplitSeparatesPublicPagesFromAPI(t *testing.T) {
	staticDir := t.TempDir()
	for _, path := range []string{"vinos", "postres"} {
		if err := os.MkdirAll(filepath.Join(staticDir, path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(staticDir, path, "index.html"), []byte("<html>public page</html>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{cfg: config.Config{StaticDir: staticDir}}
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"source":"api"}`))
	})
	handler := s.spaNavigationSplit(api)
	for _, test := range []struct {
		name, uri, accept, body string
	}{
		{"default curl page", "/vinos", "", "public page"},
		{"wildcard page", "/vinos", "*/*", "public page"},
		{"browser page", "/postres", "text/html", "public page"},
		{"explicit JSON page client", "/vinos", "application/json", `"source":"api"`},
		{"API wildcard", "/api/vinos?tipo=TINTO", "*/*", `"source":"api"`},
		{"API JSON", "/api/postres", "application/json", `"source":"api"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.uri, nil)
			req.Header.Set("Accept", test.accept)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.body) {
				t.Fatalf("returned %d and %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}
