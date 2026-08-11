package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Wiring smoke test for response compression: exercises the real mux built by
// Routes() so that if the `r.Use(middleware.Compress(5))` line is ever removed,
// this test fails. /healthz returns JSON (gzip-able) and needs no session.
// Uses a real DB (instatic's host-check middleware queries it) — skips without
// TEST_DB_DSN, same as the session-cache integration tests.
func TestGzipResponseCompression(t *testing.T) {
	db, _ := openCountingDB(t)
	srv := NewServer(db, testConfig())
	handler := srv.Routes()

	t.Run("gzip when accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusOK {
			t.Fatalf("healthz status %d", rr.Code)
		}
		if rr.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("expected Content-Encoding: gzip, got %q", rr.Header().Get("Content-Encoding"))
		}
		gr, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		body, _ := io.ReadAll(gr)
		if len(body) == 0 {
			t.Fatal("decoded body empty")
		}
	})

	t.Run("plain when not accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Header().Get("Content-Encoding") != "" {
			t.Fatalf("unexpected Content-Encoding %q", rr.Header().Get("Content-Encoding"))
		}
		if rr.Body.Len() == 0 {
			t.Fatal("body empty")
		}
	})
}
